package httpx

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func Upgrade(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	// Use coder's default same-origin verification for browser connections.
	return websocket.Accept(w, r, nil)
}

func CloseWs(ws *websocket.Conn, message string) {
	if ws == nil {
		return
	}
	// Bound shutdown even when the peer never acknowledges the close frame.
	timer := time.AfterFunc(3*time.Second, func() { _ = ws.CloseNow() })
	defer timer.Stop()
	_ = ws.Close(websocket.StatusNormalClosure, message)
	_ = ws.CloseNow()
}

func WriteWsJSON(ctx context.Context, ws *websocket.Conn, value any) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return wsjson.Write(ctx, ws, value)
}

func PingWs(ctx context.Context, ws *websocket.Conn) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// Every connection keeps a concurrent reader to process pong frames.
	return ws.Ping(ctx)
}
