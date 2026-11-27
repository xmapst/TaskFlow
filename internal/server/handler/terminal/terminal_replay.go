package terminal

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
	"uuid"

	"gorm.io/gorm"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/api/httpx"
	"github.com/xmapst/taskflow/api/iproto"
	"github.com/xmapst/taskflow/internal/storage"
	"github.com/xmapst/taskflow/internal/storage/models"
)

const castSuffix = ".cast.gz"

// The recorder budgets 64 MiB before JSON escaping (up to six bytes per byte).
const maxCastBytes = (6 * 64 << 20) + 4096

// replayChunkSize is how much recording text one frame carries: enough to keep
// the frame count low, small enough to stay well inside a write deadline.
const replayChunkSize = 64 << 10

func (h *Handler) listReplays(limit, offset int) (iproto.ReplayList, error) {
	rows, total, err := storage.ReplayList(int64(offset), int64(limit))
	if err != nil {
		return iproto.ReplayList{}, err
	}
	result := iproto.ReplayList{Items: make([]iproto.Replay, 0, len(rows)), Total: total}
	for _, row := range rows {
		result.Items = append(result.Items, iproto.Replay{Date: row.Date, ID: row.ReplayID, Size: row.Size})
	}
	result.HasMore = int64(len(rows)) < total-int64(offset)
	return result, nil
}

func (h *Handler) replayQuery(r *http.Request, key string, fallback, min, max int) (int, bool) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback, true
	}
	n, err := strconv.Atoi(raw)
	return n, err == nil && n >= min && n <= max
}

// ListTerminalReplays lists finalized WebShell recordings, newest first.
//
// @Summary		列出终端录像
// @Description	按录像日期倒序列出已完成的 WebShell 录像。
// @Tags		终端
// @Produce		json
// @Param		limit query int false "每页数量（1 至 100）" default(50)
// @Param		offset query int false "跳过的条数" default(0)
// @Success		200 {object} iproto.ReplayList
// @Failure		400 {string} string "分页参数无效"
// @Failure		403 {string} string "跨源请求被拒绝"
// @Failure		500 {string} string "无法读取录像"
// @Router		/api/v1/terminal/replays [get]
func (h *Handler) ListTerminalReplays(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	limit, okLimit := h.replayQuery(r, "limit", 50, 1, 100)
	offset, okOffset := h.replayQuery(r, "offset", 0, 0, int(^uint(0)>>1))
	if !okLimit || !okOffset {
		http.Error(w, "invalid limit or offset", http.StatusBadRequest)
		return
	}
	result, err := h.listReplays(limit, offset)
	if err != nil {
		slog.ErrorContext(r.Context(), "list WebShell recordings", slog.Any("error", err))
		http.Error(w, "could not list recordings", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// GetTerminalReplay streams a finalized asciinema v2 cast as NDJSON.
//
// @Summary		读取终端录像
// @Description	按录像 ID 查库获取录像日期和文件路径，返回解压后的 asciinema v2 录像，第一行为 JSON 头，后续每行是带时间戳的终端输出或尺寸事件。
// @Tags		终端
// @Produce		application/x-ndjson
// @Param		id path string true "录像 UUID"
// @Success		200 {string} string "asciinema v2 NDJSON"
// @Failure		403 {string} string "跨源请求被拒绝"
// @Failure		404 {string} string "录像不存在"
// @Failure		500 {string} string "无法读取录像"
// @Router		/api/v1/terminal/replays/{id} [get]
func (h *Handler) GetTerminalReplay(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id := r.PathValue("id")
	if !h.validReplayID(id) {
		http.NotFound(w, r)
		return
	}
	row, err := storage.Default().ReplayGet(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "lookup WebShell recording", slog.Any("error", err))
		http.Error(w, "could not open recording", http.StatusInternalServerError)
		return
	}
	cast, err := h.openReplay(h.ReplayDir, row.Date, id, row.FilePath)
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "open WebShell recording", slog.Any("error", err))
		http.Error(w, "could not open recording", http.StatusInternalServerError)
		return
	}
	defer cast.Close()
	reader, err := gzip.NewReader(cast)
	if err != nil {
		http.Error(w, "invalid recording", http.StatusInternalServerError)
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Detect oversize or corrupt streams instead of returning a successful partial cast.
	n, err := io.Copy(w, io.LimitReader(reader, maxCastBytes))
	if err != nil || n == maxCastBytes {
		slog.WarnContext(r.Context(), "incomplete WebShell replay stream", slog.Int64("bytes", n), slog.Any("error", err))
		panic(http.ErrAbortHandler)
	}
}

// openCast opens one finalized recording for replay. The cast is decompressed
// while it is read, so a large recording never sits in memory whole.
func (h *Handler) openCast(id string) (httpx.StreamSession, error) {
	if !h.validReplayID(id) {
		return nil, errors.New("replay not found")
	}
	row, err := storage.Default().ReplayGet(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errors.New("replay not found")
	}
	if err != nil {
		return nil, errors.New("could not open recording")
	}
	cast, err := h.openReplay(h.ReplayDir, row.Date, id, row.FilePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("replay not found")
	}
	if err != nil {
		return nil, errors.New("could not open recording")
	}
	reader, err := gzip.NewReader(cast)
	if err != nil {
		_ = cast.Close()
		return nil, errors.New("invalid recording")
	}
	return &replayStream{cast: &castReader{Reader: reader, file: cast}}, nil
}

// castReader closes the decompressor and the file underneath it together.
type castReader struct {
	*gzip.Reader
	file *os.File
}

func (c *castReader) Close() error {
	err := c.Reader.Close()
	if closeErr := c.file.Close(); err == nil {
		err = closeErr
	}
	return err
}

// replayStream forwards one recording as chunks of its NDJSON text. Lines are
// split by the client, so a chunk can end anywhere; the terminal frame rate
// stays low even for a recording with hundreds of thousands of events.
type replayStream struct {
	cast  io.ReadCloser
	bytes int64
	once  sync.Once
}

func (r *replayStream) Next(ctx context.Context) (api.StreamFrame, error) {
	for {
		if err := ctx.Err(); err != nil {
			return api.StreamFrame{}, err
		}
		chunk := make([]byte, replayChunkSize)
		n, err := r.cast.Read(chunk)
		if n > 0 {
			r.bytes += int64(n)
			if r.bytes >= maxCastBytes {
				r.Close()
				return api.StreamFrame{}, errors.New("recording exceeds the replay size limit")
			}
			return api.StreamFrame{Data: string(chunk[:n])}, nil
		}
		if err != nil {
			r.Close()
			if errors.Is(err, io.EOF) {
				return api.StreamFrame{}, io.EOF
			}
			return api.StreamFrame{}, errors.New("could not read recording")
		}
	}
}

func (r *replayStream) Close() {
	r.once.Do(func() { _ = r.cast.Close() })
}

func (h *Handler) openReplay(dir, date, id, name string) (*os.File, error) {
	if !h.validReplayDate(date) || name != path.Join(date, id+castSuffix) {
		return nil, os.ErrNotExist
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dayInfo, err := root.Lstat(date)
	if err != nil {
		return nil, err
	}
	if !dayInfo.IsDir() {
		return nil, os.ErrNotExist
	}
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, os.ErrNotExist
	}
	return root.Open(name)
}

// ImportReplays indexes finalized recordings missing from the database.
// The database, not the filesystem, serves list requests.
func (h *Handler) ImportReplays() error {
	if h.ReplayDir == "" {
		return nil
	}
	root, err := os.OpenRoot(h.ReplayDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	rows := make([]models.SReplay, 0, 100)
	flush := func() error {
		err := storage.Default().ReplayImport(rows)
		rows = rows[:0]
		return err
	}
	for _, day := range entries {
		if !day.IsDir() || !h.validReplayDate(day.Name()) {
			continue
		}
		files, err := fs.ReadDir(root.FS(), day.Name())
		if err != nil {
			return err
		}
		for _, file := range files {
			id, ok := strings.CutSuffix(file.Name(), castSuffix)
			if !ok || !file.Type().IsRegular() || !h.validReplayID(id) {
				continue
			}
			info, err := file.Info()
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			rows = append(rows, models.SReplay{Date: day.Name(), ReplayID: id, FilePath: path.Join(day.Name(), file.Name()), Size: info.Size()})
			if len(rows) == cap(rows) {
				if err := flush(); err != nil {
					return err
				}
			}
		}
	}
	return flush()
}

func (h *Handler) validReplayDate(date string) bool {
	t, err := time.Parse(time.DateOnly, date)
	return err == nil && t.Format(time.DateOnly) == date
}

func (h *Handler) validReplayID(id string) bool {
	value, err := uuid.Parse(id)
	return err == nil && value.String() == id
}
