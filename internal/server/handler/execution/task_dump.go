package execution

import (
	"errors"
	"log/slog"
	"net/http"

	"sigs.k8s.io/yaml"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/api/httpx"
	"github.com/xmapst/taskflow/api/iproto"
	"github.com/xmapst/taskflow/internal/storage"
)

// DumpTask 导出可重新提交的任务定义。
// @Summary		导出
// @Description	导出任务
// @Tags		任务
// @Produce		application/json,application/yaml
// @Param		task path string true "任务名称"
// @Success		200 {object} api.Response[string] "data 字段为 YAML 字符串"
// @Router		/api/v1/task/{task}/dump [get]
func (h *Handler) DumpTask(w http.ResponseWriter, r *http.Request) {
	taskName := r.PathValue("task")
	if taskName == "" {
		httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(errors.New("task does not exist")))
		return
	}
	res, err := h.dumpTask(taskName)
	if err != nil {
		httpx.Send(w, r, api.WithCode[any](api.CodeFailed).WithError(err))
		return
	}
	data, err := yaml.Marshal(res)
	if err != nil {
		httpx.Send(w, r, api.WithCode[any](api.CodeFailed).WithError(err))
		return
	}
	httpx.Send(w, r, api.WithData[string](string(data)))
}

func (h *Handler) dumpTask(taskName string) (*iproto.STaskReq, error) {
	task, err := storage.Task(taskName).Get()
	if err != nil {
		slog.Error(
			"task dump",
			slog.String("task", taskName),
			slog.Any("error", err),
		)
		return nil, errors.New("task not found")
	}
	res := &iproto.STaskReq{
		Workdir: task.Workdir,
		Timeout: task.Timeout,
		Cleanup: task.Cleanup,
	}
	for _, env := range storage.Task(taskName).Env().List() {
		res.Env = append(res.Env, &iproto.SEnv{
			Name:   env.Name,
			Value:  env.Value,
			Secret: env.Secret,
		})
	}
	steps := storage.Task(taskName).StepList(storage.All)
	for _, step := range steps {
		stepRes := &iproto.SStepReq{
			Name:        step.Name,
			Desc:        step.Desc,
			Kind:        step.Kind,
			Options:     iproto.SStepOptions(step.Options),
			Content:     step.Content,
			Timeout:     step.Timeout,
			RetryPolicy: step.RetryPolicy.Data(),
		}
		envs := storage.Task(taskName).Step(step.Name).Env().List()
		for _, env := range envs {
			stepRes.Env = append(stepRes.Env, &iproto.SEnv{
				Name:   env.Name,
				Value:  env.Value,
				Secret: env.Secret,
			})
		}
		stepRes.Routing = step.Routing.Data()
		res.Step = append(res.Step, stepRes)
	}
	return res, nil
}
