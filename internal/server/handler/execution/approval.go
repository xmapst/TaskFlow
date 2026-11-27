package execution

import (
	"io"
	"net/http"

	"github.com/xmapst/taskflow/api/iproto"
	"github.com/xmapst/taskflow/internal/chanrpc"
)

// ApprovalCallback validates the wire format and publishes a decision. Only the
// subscribed runner can decide whether it applies to its invocation.
// @Summary 接收外部审批结果
// @Description 校验消息格式后通过进程内 RPC 转交审批决策。204 不表示审批已采纳。
// @Tags 审批
// @Accept json
// @Param decision body iproto.ApprovalDecision true "审批结果"
// @Success 204 "无响应体"
// @Failure 400 "回调格式错误"
// @Failure 503 "审批服务不可用"
// @Router /api/v1/approval/callback [post]
// @Router /api/v1/approval/callback [put]
func (*Handler) ApprovalCallback(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	message, err := iproto.DecodeApproval(body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := chanrpc.Submit(r.Context(), message); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
