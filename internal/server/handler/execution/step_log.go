package execution

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/api/httpx"
	"github.com/xmapst/taskflow/api/iproto"
	"github.com/xmapst/taskflow/internal/storage"
	"github.com/xmapst/taskflow/internal/storage/models"
)

// GetStepLog 返回指定步骤的日志快照。
// @Summary		日志
// @Description	指定任务指定步骤的执行输出；实时候选订阅走 /api/v1/ws 的 step-log 主题
// @Tags		步骤
// @Produce		application/json,application/yaml
// @Param		task path string true "任务名称"
// @Param		step path string true "步骤名称"
// @Success		200 {object} api.Response[iproto.SStepLogsRes]
// @Router		/api/v1/task/{task}/step/{step}/log [get]
func (h *Handler) GetStepLog(w http.ResponseWriter, r *http.Request) {
	taskName := r.PathValue("task")
	stepName := r.PathValue("step")
	if taskName == "" || stepName == "" {
		httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(errors.New("task or step does not exist")))
		return
	}
	code, res, err := h.readStepLog(taskName, stepName)
	resp := api.WithData(res).WithCode(code)
	// 只追加消息，不能用 WithError：它会把成功码改写成失败码；err 也可能为 nil。
	if err != nil {
		resp = resp.WithMessage(err.Error())
	}
	httpx.Send(w, r, resp)
}

func (h *Handler) readStepLog(taskName, stepName string) (api.Code, iproto.SStepLogsRes, error) {
	step, err := storage.Task(taskName).Step(stepName).Get()
	if err != nil {
		slog.Error(
			"step log",
			slog.String("task", taskName),
			slog.String("step", stepName),
			slog.Any("error", err),
		)
		return api.CodeFailed, nil, errors.New("step not found")
	}
	switch step.State.OrUnknown() {
	case models.StatePending:
		return api.CodePending, iproto.SStepLogsRes{
			{
				Timestamp: time.Now().UnixNano(),
				Line:      1,
				Content:   step.Message,
			},
		}, errors.New(step.Message)
	case models.StatePaused:
		return api.CodePaused, iproto.SStepLogsRes{
			{
				Timestamp: time.Now().UnixNano(),
				Line:      1,
				Content:   "step is paused",
			},
		}, errors.New(step.Message)
	default:
		res, _ := h.readStepLogPage(taskName, stepName, nil)
		return h.convertState(step.State.OrUnknown()), res, errors.New(step.Message)
	}
}

func (h *Handler) readStepLogPage(taskName, stepName string, latestLine *int64) (res iproto.SStepLogsRes, done bool) {
	logs := storage.Task(taskName).Step(stepName).Log().List(latestLine)
	for _, v := range logs {
		if v.Content == api.ExecConsoleStart {
			continue
		}
		if v.Content == api.ExecConsoleDone {
			done = true
			continue
		}
		res = append(res, &iproto.SStepLogRes{
			Timestamp: v.Timestamp,
			Line:      *v.Line,
			Content:   v.Content,
		})
	}
	// 如果查询到有新日志，更新 latestLine 为最后一条日志的行号
	if len(logs) > 0 && latestLine != nil {
		*latestLine = *logs[len(logs)-1].Line // 更新 latestLine
	}
	return
}
