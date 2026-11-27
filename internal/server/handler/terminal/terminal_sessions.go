package terminal

import (
	"encoding/json"
	"net/http"
)

// ListWebShellSessionsHTTP returns the live session list as JSON.
//
// Subscribe to terminal-sessions on /api/v1/ws for live updates.
//
// @Summary		列出在线终端会话
// @Description	返回当前所有已连接的交互式 WebShell 会话。
// @Tags		终端
// @Produce		json
// @Success		200 {array} iproto.WebShellSession
// @Failure		403 {string} string "跨源请求被拒绝"
// @Router		/api/v1/terminal/sessions [get]
func (h *Handler) ListWebShellSessionsHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h.ListWebShellSessions())
}

// DisconnectWebShellHTTP forcibly closes one WebShell session.
//
// @Summary		断开终端会话
// @Description	强制关闭指定 ID 的 WebShell 会话；会话不存在时返回 404。
// @Tags		终端
// @Param		id path string true "会话 ID"
// @Success		204
// @Failure		403 {string} string "跨源请求被拒绝"
// @Failure		404 {string} string "会话不存在"
// @Router		/api/v1/terminal/sessions/{id} [delete]
func (h *Handler) DisconnectWebShellHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.DisconnectWebShell(r.PathValue("id")) {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
