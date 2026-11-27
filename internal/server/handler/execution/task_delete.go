package execution

import (
	"errors"
	"log/slog"
	"net/http"
	
	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/api/httpx"
	"github.com/xmapst/taskflow/internal/storage"
)

// DeleteTask 删除指定任务。
// @Summary		删除
// @Description	删除指定任务
// @Tags		任务
// @Produce		application/json,application/yaml
// @Param		task path string true "任务名称"
// @Success		200 {object} api.Response[any]
// @Router		/api/v1/task/{task} [delete]
func (h *Handler) DeleteTask(w http.ResponseWriter, r *http.Request) {
	taskName := r.PathValue("task")
	if taskName == "" {
		httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(errors.New("task does not exist")))
		return
	}
	err := h.deleteTask(taskName)
	if err != nil {
		slog.Error(
			"operation failed",
			slog.Any("error", err),
		)
		httpx.Send(w, r, api.WithCode[any](api.CodeFailed).WithError(err))
		return
	}
	httpx.Send(w, r, api.WithCode[any](api.CodeSuccess))
}

func (h *Handler) deleteTask(taskName string) error {
	_, err := storage.Task(taskName).Get()
	if err != nil {
		slog.Error(
			"task delete",
			slog.String("task", taskName),
			slog.Any("error", err),
		)
		return errors.New("task not found")
	}
	// No asynchronous name-only kill: a late control must not reach a replacement.
	return storage.Task(taskName).ClearAll()
}
