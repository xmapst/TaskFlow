package terminal

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"sync"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/api/httpx"
	"github.com/xmapst/taskflow/api/iproto"
)

// sessionsFrame is one terminal-sessions payload. The list is always present,
// so a client can replace its whole state with a frame and never has to merge
// increments or reason about ordering.
type sessionsFrame struct {
	Sessions []iproto.WebShellSession `json:"sessions"`
}

// RegisterStream adds the terminal admin surface to the shared WebSocket:
//
//	主题 terminal-sessions  在线会话列表，连接时一次、此后每次增删一次
//	主题 terminal-watch     某个会话的实时输出（只读，无输入通道）
//	主题 terminal-replay    某份录像的 NDJSON 文本，按块推送
//	方法 terminal.disconnect 强制断开一个会话
//	方法 replay.list        录像列表
//
// The interactive shell is deliberately absent: it carries raw binary frames
// and owns its socket (/api/v1/terminal). Everything here is small JSON, so it
// rides the same connection as the orchestration feeds.
func (h *Handler) RegisterStream(reg *httpx.StreamRegistry) {
	reg.Topic(api.TopicTerminalSessions, func(jsontext.Value) (httpx.StreamSession, error) {
		snapshot, wake := h.subscribeSessions()
		return &sessionStream{handler: h, snapshot: snapshot, wake: wake}, nil
	})
	reg.Topic(api.TopicTerminalWatch, func(params jsontext.Value) (httpx.StreamSession, error) {
		var request struct {
			ID string `json:"id"`
		}
		if err := httpx.DecodeStreamParams(params, &request); err != nil {
			return nil, err
		}
		return h.watch(request.ID)
	})
	reg.Topic(api.TopicTerminalReplay, func(params jsontext.Value) (httpx.StreamSession, error) {
		var request struct {
			ID string `json:"id"`
		}
		if err := httpx.DecodeStreamParams(params, &request); err != nil {
			return nil, err
		}
		return h.openCast(request.ID)
	})
	reg.Method(api.MethodTerminalDisconnect, func(_ context.Context, params jsontext.Value) (any, error) {
		var request struct {
			ID string `json:"id"`
		}
		if err := httpx.DecodeStreamParams(params, &request); err != nil {
			return nil, err
		}
		if !h.DisconnectWebShell(request.ID) {
			return nil, errors.New("session not found")
		}
		return nil, nil
	})
	reg.Method(api.MethodReplayList, func(_ context.Context, params jsontext.Value) (any, error) {
		var request struct {
			Limit  int `json:"limit"`
			Offset int `json:"offset"`
		}
		if err := httpx.DecodeStreamParams(params, &request); err != nil {
			return nil, err
		}
		if request.Limit == 0 {
			request.Limit = 50
		}
		if request.Limit < 1 || request.Limit > 100 || request.Offset < 0 {
			return nil, errors.New("invalid limit or offset")
		}
		return h.listReplays(request.Limit, request.Offset)
	})
}

// sessionStream pushes the whole session list: once when the client subscribes,
// then again whenever a shell is added or removed.
type sessionStream struct {
	handler  *Handler
	snapshot []iproto.WebShellSession
	wake     chan struct{}
	once     sync.Once
}

func (s *sessionStream) Next(ctx context.Context) (api.StreamFrame, error) {
	if s.snapshot != nil {
		sessions := s.snapshot
		s.snapshot = nil
		return api.StreamFrame{Data: sessionsFrame{sessions}}, nil
	}
	select {
	case <-ctx.Done():
		return api.StreamFrame{}, ctx.Err()
	case <-s.wake:
		return api.StreamFrame{Data: sessionsFrame{s.handler.ListWebShellSessions()}}, nil
	}
}

func (s *sessionStream) Close() {
	s.once.Do(func() { s.handler.unsubscribeSessions(s.wake) })
}

// subscribeSessions registers a wake-up signal and returns the list as it is
// at that moment, both under the same lock so no change slips between them.
// Every later change sends on wake without a payload: the reader re-reads the
// list, which is cheap and always current.
func (h *Handler) subscribeSessions() ([]iproto.WebShellSession, chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subscribers == nil {
		h.subscribers = make(map[chan struct{}]struct{})
	}
	wake := make(chan struct{}, 1)
	h.subscribers[wake] = struct{}{}
	return h.sessionSnapshot(), wake
}

func (h *Handler) unsubscribeSessions(wake chan struct{}) {
	h.mu.Lock()
	delete(h.subscribers, wake)
	h.mu.Unlock()
}

// publishSessions requires h.mu. A wake-up that is still queued already covers
// every change made since, so a full buffer is a coalesced update, not a lost
// one — there is nothing to drop and no reason to close the connection.
func (h *Handler) publishSessions() {
	for wake := range h.subscribers {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}
