package client

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/coder/websocket"
)

const shellReadLimit = 4 << 20

// Shell is an interactive terminal session (/api/v1/terminal). Read, Write and
// Resize may be used from different goroutines, but each of them is not safe
// for concurrent use with itself.
type Shell struct {
	ws *websocket.Conn
	// ZModem reports whether the server announced ZMODEM support. It is known
	// after Open returns.
	ZModem bool
}

// ShellSize is the initial terminal size; zero values mean 80x24.
type ShellSize struct{ Cols, Rows int }

func (c *Client) wsURL(path string, q url.Values) string {
	u := c.url(path, q)
	switch {
	case strings.HasPrefix(u, "https://"):
		return "wss://" + u[len("https://"):]
	default:
		return "ws://" + u[len("http://"):]
	}
}

func (c *Client) dialWS(ctx context.Context, path string, q url.Values) (*websocket.Conn, error) {
	h := c.header.Clone()
	h.Del("Origin") // the server allows missing Origin only
	ws, resp, err := websocket.Dial(ctx, c.wsURL(path, q), &websocket.DialOptions{
		HTTPClient: c.hc,
		HTTPHeader: h,
	})
	if err != nil {
		if resp != nil {
			return nil, c.readAPIError(resp)
		}
		return nil, err
	}
	return ws, nil
}

// OpenShell opens an interactive shell. The server's {"zmodem":true} greeting
// is consumed here.
func (c *Client) OpenShell(ctx context.Context, size ShellSize) (*Shell, error) {
	q := url.Values{}
	if size.Cols > 0 {
		q.Set("cols", strconv.Itoa(size.Cols))
	}
	if size.Rows > 0 {
		q.Set("rows", strconv.Itoa(size.Rows))
	}
	ws, err := c.dialWS(ctx, "/api/v1/terminal", q)
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(shellReadLimit)
	s := &Shell{ws: ws}
	kind, msg, err := ws.Read(ctx)
	if err != nil {
		_ = ws.CloseNow()
		return nil, err
	}
	if kind == websocket.MessageText {
		var hello struct {
			ZModem bool `json:"zmodem"`
		}
		if json.Unmarshal(msg, &hello) == nil {
			s.ZModem = hello.ZModem
		}
	} else {
		_ = ws.CloseNow()
		return nil, errors.New("client: unexpected first terminal frame")
	}
	return s, nil
}

// Read returns the next chunk of terminal output. It returns io.EOF-like close
// errors (see websocket.CloseStatus) when the shell exits.
func (s *Shell) Read(ctx context.Context) ([]byte, error) {
	for {
		kind, data, err := s.ws.Read(ctx)
		if err != nil {
			return nil, err
		}
		if kind == websocket.MessageBinary {
			return data, nil
		}
	}
}

// Write sends keyboard input to the shell.
func (s *Shell) Write(ctx context.Context, p []byte) error {
	if len(p) > 0 && p[0] == 0 {
		// The server reads a leading zero byte as ZMODEM data.
		return errors.New("client: input must not start with a NUL byte; use WriteTransfer")
	}
	return s.ws.Write(ctx, websocket.MessageBinary, p)
}

// WriteTransfer sends ZMODEM protocol data (e.g. the sender side of an rz
// upload) to the shell's PTY.
func (s *Shell) WriteTransfer(ctx context.Context, p []byte) error {
	return s.ws.Write(ctx, websocket.MessageBinary, append([]byte{0}, p...))
}

// Resize changes the terminal size (each dimension 1..1000).
func (s *Shell) Resize(ctx context.Context, cols, rows int) error {
	if cols < 1 || rows < 1 || cols > 1000 || rows > 1000 {
		return errors.New("client: terminal size must be within 1..1000")
	}
	msg, err := json.Marshal(struct{ Cols, Rows int }{cols, rows})
	if err != nil {
		return err
	}
	return s.ws.Write(ctx, websocket.MessageText, msg)
}

// Close ends the session normally.
func (s *Shell) Close() error { return s.ws.Close(websocket.StatusNormalClosure, "") }
