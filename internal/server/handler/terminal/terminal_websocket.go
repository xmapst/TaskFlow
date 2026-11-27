package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"github.com/xmapst/taskflow/api/iproto"
	"github.com/xmapst/taskflow/pkg/xterm"
	"github.com/xmapst/taskflow/pkg/xterm/zmodem"
)

const terminalWSReadLimit = 3 << 20 // 3 MiB per inbound WebSocket message.

// OpenTerminal upgrades a WebShell connection. Istio handles authentication and authorization.
// @Summary		连接 WebShell
// @Description	通过 WebSocket 连接终端。可用查询参数 cols、rows（各 1 至 1000）指定初始尺寸，缺省或无效时为 80x24。普通二进制帧传输终端数据，客户端使用 JSON 文本帧（cols、rows）调整尺寸。服务端宣布 {"zmodem":true}，ZMODEM 帧会经过 PTY 交给 Shell 中的 rz/sz 程序；协议流由终端审计过滤器识别并从录像文本中剔除。
// @Tags		终端
// @Param		cols query int false "初始列数（1 至 1000）"
// @Param		rows query int false "初始行数（1 至 1000）"
// @Success		101 "WebSocket 升级成功"
// @Failure		400 {string} string "WebSocket 握手参数无效"
// @Failure		403 {string} string "WebSocket Origin 校验失败"
// @Failure		426 {string} string "需要 WebSocket 升级请求"
// @Router		/api/v1/terminal [get]
func (h *Handler) OpenTerminal(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(terminalWSReadLimit)
	s := &session{ws: ws}
	s.ctx, s.cancel = context.WithTimeout(r.Context(), time.Hour)
	defer s.cancel()

	h.addSession(s, r.RemoteAddr)
	defer h.removeSession(s.id)
	defer s.live.close()

	go s.keepAlive()
	if err := ws.Write(s.ctx, websocket.MessageText, []byte(`{"zmodem":true}`)); err != nil {
		slog.DebugContext(s.ctx, "WebShell ZMODEM capability announcement failed", slog.Any("error", err))
		return
	}

	var recording *replayFile
	if h.ReplayDir != "" {
		recording, err = h.newReplayFile()
		if err != nil {
			slog.Warn("open WebShell recording", slog.Any("error", err))
			_ = ws.Close(websocket.StatusInternalError, "terminal recording failed")
			return
		}
	}
	opts := []xterm.Option{xterm.WithCommandAuditor(h.auditCommand), xterm.WithFileAuditor(s.auditFile)}
	if cols, rows, ok := initialSize(r); ok {
		opts = append(opts, xterm.WithSize(cols, rows))
	}
	var sink io.Writer = &s.live
	if recording != nil {
		sink = io.MultiWriter(recording, &s.live)
	}
	opts = append(opts, xterm.WithRecording(sink))
	err = xterm.Run(s.ctx, s, opts...)
	if recording != nil {
		if finishErr := recording.finish(!errors.Is(err, xterm.ErrRecording)); finishErr != nil {
			slog.Warn("finalize WebShell recording", slog.Any("error", finishErr))
		}
	}
	if errors.Is(err, zmodem.ErrFileRejected) {
		slog.InfoContext(s.ctx, "WebShell ZMODEM transfer rejected", slog.String("session_id", s.id), slog.Any("error", err))
		return
	}
	if err != nil && s.ctx.Err() == nil && websocket.CloseStatus(err) == -1 {
		slog.Warn("WebShell session ended with an error", slog.Any("error", err))
		_ = ws.Close(websocket.StatusInternalError, "terminal session failed")
	}
}

// maxTerminalDim bounds a client-supplied dimension; it matches the limit for
// later resize messages.
const maxTerminalDim = 1000

// initialSize reads the browser's terminal size from the cols and rows query
// parameters. Both must be present and within 1..maxTerminalDim; otherwise the
// default size applies, so a stale or hostile client cannot break the session.
func initialSize(r *http.Request) (cols, rows int16, ok bool) {
	q := r.URL.Query()
	c, errC := strconv.Atoi(q.Get("cols"))
	n, errR := strconv.Atoi(q.Get("rows"))
	if errC != nil || errR != nil || c < 1 || n < 1 || c > maxTerminalDim || n > maxTerminalDim {
		return 0, 0, false
	}
	return int16(c), int16(n), true
}

type session struct {
	live   liveCast
	id     string
	info   iproto.WebShellSession
	ctx    context.Context
	cancel context.CancelFunc
	ws     *websocket.Conn
}

func (h *Handler) auditCommand(_ context.Context, command string) (bool, error) {
	slog.Info("auditor command", slog.String("input", command))
	return true, nil
}

func (s *session) auditFile(event zmodem.FileAuditEvent) (allow bool, err error) {
	slog.InfoContext(s.ctx, "WebShell ZMODEM file audit",
		slog.String("session_id", s.id), slog.String("remote_addr", s.info.RemoteAddr),
		slog.Uint64("transfer_id", event.TransferID), slog.String("direction", event.Direction),
		slog.String("filename", event.Filename), slog.Int64("declared_size_bytes", event.DeclaredSize),
		slog.Time("offered_at", event.OfferedAt), slog.String("outcome", string(event.Outcome)), slog.String("reason", event.Reason))
	return true, nil
}

func (s *session) keepAlive() {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			ping, stop := context.WithTimeout(s.ctx, 5*time.Second)
			err := s.ws.Ping(ping)
			stop()
			if err != nil {
				s.cancel()
				return
			}
		}
	}
}

func (s *session) OnReceive(ctx context.Context) (xterm.Input, error) {
	for {
		kind, data, err := s.ws.Read(ctx)
		if err != nil {
			slog.DebugContext(ctx, "WebShell WebSocket read stopped", slog.Int("close_code", int(websocket.CloseStatus(err))),
				slog.Int("read_limit_bytes", terminalWSReadLimit), slog.Any("error", err))
			return xterm.Input{}, err
		}
		if kind == websocket.MessageBinary {
			if len(data) > 0 && data[0] == 0 {
				return xterm.Input{Data: data[1:], Transfer: true}, nil
			}
			return xterm.Input{Data: data}, nil
		}
		var size struct{ Cols, Rows int16 }
		if json.Unmarshal(data, &size) == nil && size.Cols >= 1 && size.Rows >= 1 && size.Cols <= maxTerminalDim && size.Rows <= maxTerminalDim {
			return xterm.Input{Cols: size.Cols, Rows: size.Rows}, nil
		}
	}
}

func (s *session) OnSend(ctx context.Context, data []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.ws.Write(ctx, websocket.MessageBinary, data)
}

func (s *session) OnFinish(err error) {
	switch {
	case err == nil:
		_ = s.ws.Close(websocket.StatusNormalClosure, "terminal exited")
	case errors.Is(err, zmodem.ErrFileRejected):
		_ = s.ws.Close(websocket.StatusPolicyViolation, "ZMODEM file transfer rejected")
	default:
		_ = s.ws.Close(websocket.StatusInternalError, "terminal recording stopped")
	}
}
