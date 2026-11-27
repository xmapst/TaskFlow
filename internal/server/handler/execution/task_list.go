package execution

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/api/httpx"
	"github.com/xmapst/taskflow/api/iproto"
	"github.com/xmapst/taskflow/internal/storage"
	"github.com/xmapst/taskflow/internal/storage/models"
)

// ListTasks 分页列出任务。
// @Summary		列表
// @Description	获取任务列表；实时列表订阅走 /api/v1/ws 的 tasks 主题
// @Tags		任务
// @Produce		application/json,application/yaml
// @Param		page query int false "页码，-1 为不分页" default(1)
// @Param		size query int false "每页任务数" default(15)
// @Param prefix query string false "任务名前缀"
// @Success		200 {object} api.Response[iproto.STaskListDetailRes]
// @Router		/api/v1/task [get]
func (h *Handler) ListTasks(w http.ResponseWriter, r *http.Request) {
	var req = &iproto.SPageReq{
		Page: 1,
		Size: 15,
	}
	query := r.URL.Query()
	for _, field := range []struct {
		name  string
		value *int64
	}{{"page", &req.Page}, {"size", &req.Size}} {
		if values := query[field.name]; len(values) != 0 {
			value := int64(0)
			if values[0] != "" {
				var err error
				value, err = strconv.ParseInt(values[0], 10, 64)
				if err != nil {
					httpx.Send(w, r, api.WithError[any](fmt.Errorf("invalid %s: %w", field.name, err)))
					return
				}
			}
			*field.value = value
		}
	}
	req.Prefix = query.Get("prefix")
	httpx.Send(w, r, api.WithData(h.listTasks(req)))
}

func (h *Handler) listTasks(req *iproto.SPageReq) *iproto.STaskListDetailRes {
	// Normalize a copy: callers may retain req.
	page := iproto.SPageReq{
		Page: 1,
		Size: 10,
	}
	if req != nil {
		page = *req
	}
	if page.Page < 1 && page.Page != -1 {
		page.Page = 1
	}
	if page.Size <= 0 {
		page.Size = 10
	} else if page.Size > 500 {
		page.Size = 500
	}
	req = &page
	tasks, total := storage.TaskList(req.Page, req.Size, req.Prefix)
	if tasks == nil {
		return nil
	}
	pageTotal := total / req.Size
	if total%req.Size != 0 {
		pageTotal += 1
	}
	var list = &iproto.STaskListDetailRes{
		Page: &iproto.SPageRes{
			Current: req.Page,
			Size:    req.Size,
			Total:   pageTotal,
		},
	}
	for _, task := range tasks {
		res := &iproto.STaskRes{
			Name:    task.Name,
			State:   models.StateMap[*task.State],
			Cleanup: task.Cleanup,
			Message: task.Message,
			Time: &iproto.STimeRes{
				Start: task.STimeStr(),
				End:   task.ETimeStr(),
			},
		}
		st := storage.Task(task.Name)

		// Report the steps that are currently running.
		steps := st.StepStateList(storage.All)
		res.Count = int64(len(steps))
		var groups = make(map[models.State][]string)
		for name, state := range steps {
			groups[state] = append(groups[state], name)
		}
		res.Message = h.generateStateMessage(res.Message, groups)
		list.Tasks = append(list.Tasks, res)
	}
	return list
}
