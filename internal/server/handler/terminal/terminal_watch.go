package terminal

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/api/httpx"
)

// liveCast mirrors the recorder's filtered NDJSON, never the raw WebSocket
// frames. Subscribers start at the live edge, with the header and last resize.
type liveCast struct {
	mu             sync.Mutex
	header, resize string
	partial        []byte // bytes of the line still being written
	viewers        map[chan string]struct{}
	closed         bool
}

// Write may receive a line in several pieces: the JSON encoder emits the cast
// header as two writes, the second being only "}\n". Only complete lines are
// recorded and broadcast, so a late viewer never starts from a fragment.
func (l *liveCast) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return len(p), nil
	}
	l.partial = append(l.partial, p...)
	for {
		end := bytes.IndexByte(l.partial, '\n')
		if end < 0 {
			break
		}
		l.publish(string(l.partial[:end+1]))
		l.partial = l.partial[end+1:]
	}
	if len(l.partial) == 0 {
		l.partial = nil // release the backing array
	}
	return len(p), nil
}

// publish sends one complete line to every viewer. The caller holds l.mu.
func (l *liveCast) publish(line string) {
	if l.header == "" {
		l.header = line
	} else if _, rest, ok := strings.Cut(line, ","); ok && strings.HasPrefix(rest, `"r",`) {
		// Recorder events are compact JSON arrays: [time,"r","80x24"].
		l.resize = line
	}
	for ch := range l.viewers {
		select {
		case ch <- line:
		default:
			delete(l.viewers, ch)
			close(ch)
		}
	}
}

func (l *liveCast) subscribe() (chan string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || len(l.viewers) >= 8 {
		return nil, false
	}
	if l.viewers == nil {
		l.viewers = make(map[chan string]struct{})
	}
	ch := make(chan string, 32)
	if l.header != "" {
		ch <- l.header
	}
	if l.resize != "" {
		ch <- l.resize
	}
	l.viewers[ch] = struct{}{}
	return ch, true
}

func (l *liveCast) unsubscribe(ch chan string) {
	l.mu.Lock()
	delete(l.viewers, ch)
	l.mu.Unlock()
}

func (l *liveCast) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	for ch := range l.viewers {
		close(ch)
	}
	clear(l.viewers)
}

// WatchWebShell streams output only. The observer has no input channel.
//
// The shared WebSocket carries the same cast lines per frame (topic
// terminal-watch); this endpoint is the plain-HTTP equivalent for clients that
// would rather read a line stream than speak the multiplexed protocol.
//
// @Summary		观看终端会话
// @Description	以 NDJSON 实时推送指定会话的 asciinema v2 录像行；只读，没有输入通道。首行固定为空行，随后每行是一个录像事件，空闲时每 15 秒发一个空行保活。
// @Tags		终端
// @Produce		application/x-ndjson
// @Param		id path string true "会话 ID"
// @Success		200 {string} string "asciinema v2 NDJSON"
// @Failure		403 {string} string "跨源请求被拒绝"
// @Failure		404 {string} string "会话不存在"
// @Failure		503 {string} string "会话已结束或观看人数已满"
// @Router		/api/v1/terminal/sessions/{id}/watch [get]
func (h *Handler) WatchWebShell(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	s := h.sessions[r.PathValue("id")]
	h.mu.Unlock()
	if s == nil {
		http.NotFound(w, r)
		return
	}
	ch, ok := s.live.subscribe()
	if !ok {
		http.Error(w, "session ended or viewer limit reached", http.StatusServiceUnavailable)
		return
	}
	defer s.live.unsubscribe(ch)
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	send := func(line string) bool {
		if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return false
		}
		if _, err := w.Write([]byte(line)); err != nil {
			return false
		}
		err := controller.Flush()
		_ = controller.SetWriteDeadline(time.Time{})
		return err == nil
	}
	// Leading newline flushes the headers before the first event arrives.
	if !send("\n") {
		return
	}
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if !send("\n") {
				return
			}
		case line, ok := <-ch:
			if !ok || !send(line) {
				return
			}
		}
	}
}

// watch opens a read-only subscription to a live session's recorded output.
func (h *Handler) watch(id string) (httpx.StreamSession, error) {
	h.mu.Lock()
	s := h.sessions[id]
	h.mu.Unlock()
	if s == nil {
		return nil, errors.New("session not found")
	}
	lines, ok := s.live.subscribe()
	if !ok {
		return nil, errors.New("session ended or viewer limit reached")
	}
	return &watchStream{live: &s.live, lines: lines}, nil
}

// watchStream forwards the recorded cast lines of one session as they happen.
// Each line is already JSON, so it travels as a raw value rather than a string
// that the client would have to parse twice.
type watchStream struct {
	live  *liveCast
	lines chan string
	once  sync.Once
}

func (w *watchStream) Next(ctx context.Context) (api.StreamFrame, error) {
	select {
	case <-ctx.Done():
		return api.StreamFrame{}, ctx.Err()
	case line, ok := <-w.lines:
		if !ok {
			return api.StreamFrame{}, io.EOF
		}
		return api.StreamFrame{Data: jsontext.Value(line)}, nil
	}
}

func (w *watchStream) Close() {
	w.once.Do(func() { w.live.unsubscribe(w.lines) })
}
