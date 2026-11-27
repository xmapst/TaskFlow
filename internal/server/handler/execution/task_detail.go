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

// GetTask 返回指定任务的详情。
// @Summary		详情
// @Description	获取任务详情；实时订阅走 /api/v1/ws 的 task 主题
// @Tags		任务
// @Produce		application/json,application/yaml
// @Param		task path string true "任务名称"
// @Success		200 {object} api.Response[iproto.STaskRes]
// @Router		/api/v1/task/{task} [get]
func (h *Handler) GetTask(w http.ResponseWriter, r *http.Request) {
	taskName := r.PathValue("task")
	if taskName == "" {
		httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(errors.New("task does not exist")))
		return
	}
	code, task, err := h.queryTask(taskName)
	if err != nil {
		httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(err))
		return
	}
	w.Header().Set(iproto.XTaskState, task.State)
	
	httpx.Send(w, r, api.WithData(task).WithCode(code).WithMessage(task.Message))
}

func (h *Handler) queryTask(taskName string) (api.Code, *iproto.STaskRes, error) {
	db := storage.Task(taskName)
	var task *models.STask
	var err error
	task, err = db.Get()
	if err != nil {
		slog.Error(
			"task detail",
			slog.String("task", taskName),
			slog.Any("error", err),
		)
		return api.CodeFailed, nil, errors.New("task not found")
	}
	
	data := &iproto.STaskRes{
		Name:    task.Name,
		Workdir: task.Workdir,
		Cleanup: task.Cleanup,
		State:   models.StateMap[task.State.OrUnknown()],
		Message: task.Message,
		Timeout: task.Timeout,
		Time: &iproto.STimeRes{
			Start: task.STimeStr(),
			End:   task.ETimeStr(),
		},
	}
	for _, env := range db.Env().List() {
		data.Env = append(data.Env, &iproto.SEnv{
			Name:   env.Name,
			Value:  env.Value,
			Secret: env.Secret,
		})
	}
	
	// Report the steps that are currently running.
	steps := db.StepStateList(storage.All)
	data.Count = int64(len(steps))
	var groups = make(map[models.State][]string)
	for name, state := range steps {
		groups[state] = append(groups[state], name)
	}
	data.Message = h.generateStateMessage(data.Message, groups)
	return h.convertState(task.State.OrUnknown()), data, nil
}
