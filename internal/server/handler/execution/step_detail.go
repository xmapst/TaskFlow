package execution

import (
	"errors"
	"log/slog"
	"net/http"
	
	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/api/httpx"
	"github.com/xmapst/taskflow/api/iproto"
	"github.com/xmapst/taskflow/internal/storage"
	"github.com/xmapst/taskflow/internal/storage/models"
)

// GetStep 返回指定步骤的详情。
// @Summary		详情
// @Description	获取步骤详情；实时订阅走 /api/v1/ws 的 step 主题
// @Tags		步骤
// @Produce		application/json,application/yaml
// @Param		task path string true "任务名称"
// @Param		step path string true "步骤名称"
// @Success		200 {object} api.Response[iproto.SStepRes]
// @Router		/api/v1/task/{task}/step/{step} [get]
func (h *Handler) GetStep(w http.ResponseWriter, r *http.Request) {
	var taskName = r.PathValue("task")
	if taskName == "" {
		httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(errors.New("task does not exist")))
		return
	}
	var stepName = r.PathValue("step")
	if stepName == "" {
		httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(errors.New("step does not exist")))
		return
	}
	code, step, err := h.queryStep(taskName, stepName)
	if err != nil {
		httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(err))
		return
	}
	
	httpx.Send(w, r, api.WithData(step).WithCode(code).WithMessage(step.Message))
}

func (h *Handler) queryStep(taskName, stepName string) (api.Code, *iproto.SStepRes, error) {
	stepStorage := storage.Task(taskName).Step(stepName)
	step, err := stepStorage.Get()
	if err != nil {
		slog.Error(
			"step detail",
			slog.String("task", taskName),
			slog.String("step", stepName),
			slog.Any("error", err),
		)
		return api.CodeFailed, nil, errors.New("step not found")
	}
	data := &iproto.SStepRes{
		Name:        step.Name,
		Desc:        step.Desc,
		State:       models.StateMap[step.State.OrUnknown()],
		Code:        step.Code.Int64(),
		Message:     step.Message,
		Timeout:     step.Timeout,
		Kind:        step.Kind,
		Options:     iproto.SStepOptions(step.Options),
		Content:     step.Content,
		RetryPolicy: step.RetryPolicy.Data(),
		Time: &iproto.STimeRes{
			Start: step.STimeStr(),
			End:   step.ETimeStr(),
		},
	}
	data.Routing = step.Routing.Data()
	envs := stepStorage.Env().List()
	for _, env := range envs {
		data.Env = append(data.Env, &iproto.SEnv{
			Name:   env.Name,
			Value:  env.Value,
			Secret: env.Secret,
		})
	}
	return api.Code(data.Code), data, nil
}
