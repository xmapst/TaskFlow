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

// ListSteps 返回任务的步骤列表。
// @Summary		列表
// @Description	获取指定任务的步骤列表；实时列表订阅走 /api/v1/ws 的 steps 主题
// @Tags		步骤
// @Produce		application/json,application/yaml
// @Param		task path string true "任务名称"
// @Success		200 {object} api.Response[iproto.SStepsRes]
// @Router		/api/v1/task/{task}/step [get]
func (h *Handler) ListSteps(w http.ResponseWriter, r *http.Request) {
	taskName := r.PathValue("task")
	if taskName == "" {
		httpx.Send(w, r, api.WithCode[any](api.CodeNoData).WithError(errors.New("task does not exist")))
		return
	}
	code, list, err := h.querySteps(taskName)
	httpx.Send(w, r, api.WithData(list).WithError(err).WithCode(code))
}

func (h *Handler) querySteps(taskName string) (code api.Code, data iproto.SStepsRes, err error) {
	db := storage.Task(taskName)
	task, err := db.Get()
	if err != nil {
		slog.Error(
			"task get steps",
			slog.String("task", taskName),
			slog.Any("error", err),
		)
		return api.CodeNoData, nil, err
	}

	steps := db.StepList(storage.All)
	if steps == nil {
		return api.CodeNoData, nil, errors.New("steps not found")
	}

	// 用于分组和构建任务数据
	var groups = make(map[models.State][]string)
	stepList := make(iproto.SStepsRes, 0, len(steps))
	for _, step := range steps {
		state := step.State.OrUnknown()
		groups[state] = append(groups[state], step.Name)
		res := &iproto.SStepRes{
			Name:    step.Name,
			State:   models.StateMap[step.State.OrUnknown()],
			Code:    step.Code.Int64(),
			Message: step.Message,
			Time: &iproto.STimeRes{
				Start: step.STimeStr(),
				End:   step.ETimeStr(),
			},
			Kind:    step.Kind,
			Options: iproto.SStepOptions(step.Options),
			Routing: step.Routing.Data(),
		}
		stepList = append(stepList, res)
	}

	data = h.orderStepsByDependency(stepList)

	// 生成任务的状态消息
	task.Message = h.generateStateMessage(task.Message, groups)

	return h.convertState(task.State.OrUnknown()), data, errors.New(task.Message)
}

// 按存储顺序遍历步骤，先输出每个步骤的依赖。
func (h *Handler) orderStepsByDependency(steps iproto.SStepsRes) iproto.SStepsRes {
	byName := make(map[string]*iproto.SStepRes, len(steps))
	for _, step := range steps {
		byName[step.Name] = step
	}
	visited := make(map[string]bool, len(steps))
	sorted := make(iproto.SStepsRes, 0, len(steps))

	var visit func(name string)
	visit = func(name string) {
		if visited[name] {
			return
		}
		visited[name] = true

		if step, exists := byName[name]; exists {
			for _, edge := range iproto.DAGDependencies(step.Routing).Edges {
				visit(edge.Task)
			}
			sorted = append(sorted, step)
		}
	}

	for _, step := range steps {
		visit(step.Name)
	}

	return sorted
}
