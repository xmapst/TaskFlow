package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/xmapst/taskflow/api/iproto"
)

// Action is a control action for a task or a step.
type Action string

const (
	ActionKill   Action = "kill"
	ActionPause  Action = "pause"
	ActionResume Action = "resume"
)

// pauseValue renders the "duration" parameter: a Go duration for a timed
// pause, "-1" for a pause that waits for a manual resume.
func pauseValue(d time.Duration) string {
	if d > 0 {
		return d.String()
	}
	return "-1"
}

func controlQuery(action Action, pauseFor time.Duration) url.Values {
	q := url.Values{"action": {string(action)}}
	if action == ActionPause {
		q.Set("duration", pauseValue(pauseFor))
	}
	return q
}

// ListTasksOptions pages the task list. Page -1 disables paging; zero values
// use the server defaults (page 1, 15 per page).
type ListTasksOptions struct {
	Page   int64
	Size   int64
	Prefix string
}

// ListTasks returns one page of tasks.
func (c *Client) ListTasks(ctx context.Context, opts ListTasksOptions) (*iproto.STaskListDetailRes, error) {
	q := url.Values{}
	if opts.Page != 0 {
		q.Set("page", strconv.FormatInt(opts.Page, 10))
	}
	if opts.Size != 0 {
		q.Set("size", strconv.FormatInt(opts.Size, 10))
	}
	if opts.Prefix != "" {
		q.Set("prefix", opts.Prefix)
	}
	return c.data(c.call[*iproto.STaskListDetailRes](ctx, http.MethodGet, "/api/v1/task", q, nil, ""))
}

// CreateTask submits a task definition.
func (c *Client) CreateTask(ctx context.Context, task *iproto.STaskReq) (*iproto.STaskCreateRes, error) {
	if task == nil {
		return nil, errors.New("client: nil task")
	}
	body, err := c.marshal(task)
	if err != nil {
		return nil, err
	}
	return c.data(c.call[*iproto.STaskCreateRes](ctx, http.MethodPost, "/api/v1/task", nil, body, "application/json"))
}

// CreateTaskYAML submits a task definition written as YAML.
func (c *Client) CreateTaskYAML(ctx context.Context, definition []byte) (*iproto.STaskCreateRes, error) {
	return c.data(c.call[*iproto.STaskCreateRes](ctx, http.MethodPost, "/api/v1/task", nil, definition, "application/yaml"))
}

// GetTask returns a task. The result code is the run state of the task.
func (c *Client) GetTask(ctx context.Context, task string) (*Result[*iproto.STaskRes], error) {
	return c.state(c.call[*iproto.STaskRes](ctx, http.MethodGet, "/api/v1/task/"+c.seg(task), nil, nil, ""))
}

// ManageTask kills, pauses or resumes a task. pauseFor only applies to
// ActionPause: a positive value resumes the task by itself after that long,
// zero waits for an explicit resume.
func (c *Client) ManageTask(ctx context.Context, task string, action Action, pauseFor time.Duration) error {
	return c.ok(c.call[any](ctx, http.MethodPut, "/api/v1/task/"+c.seg(task), controlQuery(action, pauseFor), nil, ""))
}

// DeleteTask removes a task and its records.
func (c *Client) DeleteTask(ctx context.Context, task string) error {
	return c.ok(c.call[any](ctx, http.MethodDelete, "/api/v1/task/"+c.seg(task), nil, nil, ""))
}

// DumpTask exports the task definition as YAML that CreateTaskYAML accepts.
func (c *Client) DumpTask(ctx context.Context, task string) (string, error) {
	return c.data(c.call[string](ctx, http.MethodGet, "/api/v1/task/"+c.seg(task)+"/dump", nil, nil, ""))
}

// ListSteps returns the steps of a task. The result code is the task state.
func (c *Client) ListSteps(ctx context.Context, task string) (*Result[iproto.SStepsRes], error) {
	return c.state(c.call[iproto.SStepsRes](ctx, http.MethodGet, "/api/v1/task/"+c.seg(task)+"/step", nil, nil, ""))
}

// GetStep returns one step. The result code is the run state of the step.
func (c *Client) GetStep(ctx context.Context, task, step string) (*Result[*iproto.SStepRes], error) {
	return c.state(c.call[*iproto.SStepRes](ctx, http.MethodGet, "/api/v1/task/"+c.seg(task)+"/step/"+c.seg(step), nil, nil, ""))
}

// ManageStep kills, pauses or resumes one step; see ManageTask for pauseFor.
func (c *Client) ManageStep(ctx context.Context, task, step string, action Action, pauseFor time.Duration) error {
	return c.ok(c.call[any](ctx, http.MethodPut, "/api/v1/task/"+c.seg(task)+"/step/"+c.seg(step), controlQuery(action, pauseFor), nil, ""))
}

// StepLog returns a snapshot of the step output. The result code is the run
// state of the step; use Stream.WatchStepLog to follow a running step.
func (c *Client) StepLog(ctx context.Context, task, step string) (*Result[iproto.SStepLogsRes], error) {
	return c.state(c.call[iproto.SStepLogsRes](ctx, http.MethodGet, "/api/v1/task/"+c.seg(task)+"/step/"+c.seg(step)+"/log", nil, nil, ""))
}
