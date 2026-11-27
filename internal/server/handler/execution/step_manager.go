package execution

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/api/httpx"
	"github.com/xmapst/taskflow/api/iproto"
	"github.com/xmapst/taskflow/internal/chanrpc"
	"github.com/xmapst/taskflow/internal/storage"
	"github.com/xmapst/taskflow/internal/storage/models"
)

// ManageStep 向运行中的任务发送步骤控制指令。
// @Summary		管理
// @Description	管理指定任务的指定步骤, 支持暂停、恢复、终止、超时暂停自动恢复
// @Tags		步骤
// @Produce		application/json,application/yaml
// @Param		task path string true "任务名称"
// @Param		step path string true "步骤名称"
// @Param		action query string true "操作" Enums(kill,pause,resume)
// @Param		duration query string false "pause 时的 Go 时长（如 30s），-1 表示手动恢复" default(-1)
// @Success		200 {object} api.Response[any]
// @Router		/api/v1/task/{task}/step/{step} [put]
func (h *Handler) ManageStep(w http.ResponseWriter, r *http.Request) {
	taskName := r.PathValue("task")
	if taskName == "" {
		httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(errors.New("task does not exist")))
		return
	}
	stepName := r.PathValue("step")
	if stepName == "" {
		httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(errors.New("step does not exist")))
		return
	}
	action := r.URL.Query().Get("action")
	duration := httpx.QueryDefault(r, "duration", "-1")
	err := h.manageStep(r.Context(), taskName, stepName, action, duration)
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

func (h *Handler) manageStep(ctx context.Context, taskName, stepName string, action string, duration string) error {
	if action != "pause" && action != "resume" && action != "kill" {
		return errors.New("unknown step control action")
	}
	task, err := storage.Task(taskName).Get()
	if err != nil {
		slog.Error(
			"step manager",
			slog.String("task", taskName),
			slog.String("step", stepName),
			slog.Any("error", err),
		)
		return errors.New("task not found")
	}
	if state := task.State.OrUnknown(); state != models.StateRunning && state != models.StatePending && state != models.StatePaused {
		return errors.New("task is no running")
	}
	step, err := storage.Task(taskName).Step(stepName).Get()
	if err != nil {
		slog.Error(
			"step manager",
			slog.String("task", taskName),
			slog.String("step", stepName),
			slog.Any("error", err),
		)
		return errors.New("step not found")
	}
	if state := step.State.OrUnknown(); state != models.StateRunning && state != models.StatePending && state != models.StatePaused {
		return errors.New("step is no running")
	}
	return chanrpc.Submit(ctx, iproto.TaskControl{
		Name:     taskName,
		ID:       task.ID,
		Step:     stepName,
		Action:   action,
		Duration: duration,
	})
}
