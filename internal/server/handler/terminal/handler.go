package terminal

import (
	"sort"
	"sync"
	"time"
	"uuid"

	"github.com/xmapst/taskflow/api/iproto"
)

// Handler implements terminal and replay endpoints.
type Handler struct {
	ReplayDir string

	mu          sync.Mutex
	sessions    map[string]*session
	subscribers map[chan struct{}]struct{}
}

func (h *Handler) addSession(s *session, remoteAddr string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions == nil {
		h.sessions = make(map[string]*session)
	}
	s.id = uuid.NewV7().String()
	s.info = iproto.WebShellSession{ID: s.id, Connected: time.Now().UTC(), RemoteAddr: remoteAddr}
	h.sessions[s.id] = s
	h.publishSessions()
	return s.id
}

// removeSession publishes at most one removal, even if a forced disconnect
// races with normal WebSocket cleanup.
func (h *Handler) removeSession(id string) *session {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.sessions[id]
	if s != nil {
		delete(h.sessions, id)
		h.publishSessions()
	}
	return s
}

func (h *Handler) ListWebShellSessions() []iproto.WebShellSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessionSnapshot()
}

// sessionSnapshot requires h.mu.
func (h *Handler) sessionSnapshot() []iproto.WebShellSession {
	result := make([]iproto.WebShellSession, 0, len(h.sessions))
	for _, s := range h.sessions {
		result = append(result, s.info)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID > result[j].ID })
	return result
}

func (h *Handler) DisconnectWebShell(id string) bool {
	s := h.removeSession(id)
	if s == nil {
		return false
	}
	s.cancel()
	s.live.close()
	_ = s.ws.CloseNow()
	return true
}
