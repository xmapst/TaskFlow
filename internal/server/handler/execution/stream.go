package execution

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/api/httpx"
	"github.com/xmapst/taskflow/api/iproto"
	"github.com/xmapst/taskflow/internal/config"
	"github.com/xmapst/taskflow/internal/storage"
	"github.com/xmapst/taskflow/internal/storage/models"
)

const stepLogPollInterval = 300 * time.Millisecond

// RegisterStream wires the task topics and calls into the shared WebSocket
// connection.
func (h *Handler) RegisterStream(reg *httpx.StreamRegistry) {
	reg.Topic(api.TopicTasks, h.streamTasks)
	reg.Topic(api.TopicTask, h.streamTask)
	reg.Topic(api.TopicSteps, h.streamSteps)
	reg.Topic(api.TopicStep, h.streamStep)
	reg.Topic(api.TopicStepLog, h.streamStepLog)
	reg.Method(api.MethodTaskCreate, h.callCreateTask)
	reg.Method(api.MethodTaskManage, h.callManageTask)
	reg.Method(api.MethodTaskDelete, h.callDeleteTask)
	reg.Method(api.MethodTaskDump, h.callDumpTask)
	reg.Method(api.MethodStepManage, h.callManageStep)
}

// streamTarget names the task and step a subscription watches.
type streamTarget struct {
	Task string `json:"task"`
	Step string `json:"step"`
}

// finished reports whether a task or step state can no longer change on its
// own, so a subscription for it has nothing left to report.
func (h *Handler) finished(code api.Code) bool {
	switch code {
	case api.CodeSuccess, api.CodeFailed, api.CodeSkipped, api.CodeNoData:
		return true
	default:
		return false
	}
}

func (h *Handler) streamTasks(params jsontext.Value) (httpx.StreamSession, error) {
	req := &iproto.SPageReq{Page: 1, Size: 15}
	if err := httpx.DecodeStreamParams(params, req); err != nil {
		return nil, err
	}
	return &httpx.SnapshotSession[*iproto.STaskListDetailRes]{
		Query: func(context.Context) (api.Code, *iproto.STaskListDetailRes, error) {
			// listTasks normalizes its argument, so it always receives a copy.
			page := *req
			return api.CodeSuccess, h.listTasks(&page), nil
		},
		Interval: 300 * time.Millisecond,
		Frame: func(snapshot httpx.Snapshot[*iproto.STaskListDetailRes]) (api.StreamFrame, bool) {
			frame := api.StreamFrame{Code: snapshot.Code}
			if snapshot.Data != nil {
				frame.Data = snapshot.Data
			}
			return frame, true
		},
	}, nil
}

func (h *Handler) streamTask(params jsontext.Value) (httpx.StreamSession, error) {
	var target streamTarget
	if err := httpx.DecodeStreamParams(params, &target); err != nil {
		return nil, err
	}
	if target.Task == "" {
		return nil, errors.New("task is required")
	}
	return &httpx.SnapshotSession[*iproto.STaskRes]{
		Query: func(context.Context) (api.Code, *iproto.STaskRes, error) {
			return h.queryTask(target.Task)
		},
		Interval: time.Second,
		Frame: func(snapshot httpx.Snapshot[*iproto.STaskRes]) (api.StreamFrame, bool) {
			frame := api.StreamFrame{Code: snapshot.Code}
			if snapshot.Data != nil {
				frame.Data = snapshot.Data
			}
			if snapshot.Err != nil {
				frame.Message = snapshot.Err.Error()
			}
			return frame, true
		},
		Done: func(snapshot httpx.Snapshot[*iproto.STaskRes]) bool {
			return h.finished(snapshot.Code)
		},
	}, nil
}

func (h *Handler) streamSteps(params jsontext.Value) (httpx.StreamSession, error) {
	var target streamTarget
	if err := httpx.DecodeStreamParams(params, &target); err != nil {
		return nil, err
	}
	if target.Task == "" {
		return nil, errors.New("task is required")
	}
	// querySteps reports the task summary as the error, the same way the HTTP
	// response does. Keep it aside so it does not count as a change: otherwise
	// every poll would look different and resend an unchanged list.
	var message string
	return &httpx.SnapshotSession[iproto.SStepsRes]{
		Query: func(context.Context) (api.Code, iproto.SStepsRes, error) {
			code, list, err := h.querySteps(target.Task)
			message = ""
			if err != nil {
				message = err.Error()
			}
			return code, list, nil
		},
		Interval: 500 * time.Millisecond,
		Frame: func(snapshot httpx.Snapshot[iproto.SStepsRes]) (api.StreamFrame, bool) {
			frame := api.StreamFrame{Code: snapshot.Code, Message: message}
			if snapshot.Data != nil {
				frame.Data = snapshot.Data
			}
			return frame, true
		},
		Done: func(snapshot httpx.Snapshot[iproto.SStepsRes]) bool {
			return h.finished(snapshot.Code)
		},
	}, nil
}

func (h *Handler) streamStep(params jsontext.Value) (httpx.StreamSession, error) {
	var target streamTarget
	if err := httpx.DecodeStreamParams(params, &target); err != nil {
		return nil, err
	}
	if target.Task == "" || target.Step == "" {
		return nil, errors.New("task and step are required")
	}
	return &httpx.SnapshotSession[*iproto.SStepRes]{
		Query: func(context.Context) (api.Code, *iproto.SStepRes, error) {
			return h.queryStep(target.Task, target.Step)
		},
		Interval: time.Second,
		Frame: func(snapshot httpx.Snapshot[*iproto.SStepRes]) (api.StreamFrame, bool) {
			frame := api.StreamFrame{Code: snapshot.Code}
			if snapshot.Data != nil {
				frame.Data = snapshot.Data
			}
			if snapshot.Err != nil {
				frame.Message = snapshot.Err.Error()
			}
			return frame, true
		},
		Done: func(snapshot httpx.Snapshot[*iproto.SStepRes]) bool {
			return h.finished(snapshot.Code)
		},
	}, nil
}

// callCreateTask submits a task definition sent over the WebSocket. The
// definition travels as text so the client can send YAML or JSON without
// encoding it twice.
func (h *Handler) callCreateTask(ctx context.Context, params jsontext.Value) (any, error) {
	var req struct {
		Format  string `json:"format"`
		Content string `json:"content"`
	}
	if err := httpx.DecodeStreamParams(params, &req); err != nil {
		return nil, err
	}
	format := strings.ToLower(strings.TrimSpace(req.Format))
	if format == "" {
		format = "yaml"
	}
	task := new(iproto.STaskReq)
	if err := decodeTask("application/"+format, strings.NewReader(req.Content), task); err != nil {
		slog.Error("task create", slog.Any("error", err))
		return nil, err
	}
	name, err := h.createTask(ctx, task, config.App.ExecTimeout)
	if err != nil {
		return nil, err
	}
	return &iproto.STaskCreateRes{Name: name, Count: int64(len(task.Step))}, nil
}

// callManageTask sends a control action to a task over the WebSocket. Duration
// only matters for pause: it is a Go duration after which the task resumes by
// itself, and -1 means it waits for a manual resume.
func (h *Handler) callManageTask(ctx context.Context, params jsontext.Value) (any, error) {
	var req struct {
		Task     string `json:"task"`
		Action   string `json:"action"`
		Duration string `json:"duration"`
	}
	if err := httpx.DecodeStreamParams(params, &req); err != nil {
		return nil, err
	}
	if req.Task == "" {
		return nil, errors.New("task does not exist")
	}
	if req.Duration == "" {
		req.Duration = "-1"
	}
	if err := h.manageTask(ctx, req.Task, req.Action, req.Duration); err != nil {
		slog.Error("operation failed", slog.Any("error", err))
		return nil, err
	}
	return nil, nil
}

// callManageStep sends a control action to one step of a running task. Like
// callManageTask, duration only matters for pause.
func (h *Handler) callManageStep(ctx context.Context, params jsontext.Value) (any, error) {
	var req struct {
		Task     string `json:"task"`
		Step     string `json:"step"`
		Action   string `json:"action"`
		Duration string `json:"duration"`
	}
	if err := httpx.DecodeStreamParams(params, &req); err != nil {
		return nil, err
	}
	if req.Task == "" {
		return nil, errors.New("task does not exist")
	}
	if req.Step == "" {
		return nil, errors.New("step does not exist")
	}
	if req.Duration == "" {
		req.Duration = "-1"
	}
	if err := h.manageStep(ctx, req.Task, req.Step, req.Action, req.Duration); err != nil {
		slog.Error("operation failed", slog.Any("error", err))
		return nil, err
	}
	return nil, nil
}

// callDeleteTask removes a task and its records over the WebSocket.
func (h *Handler) callDeleteTask(_ context.Context, params jsontext.Value) (any, error) {
	var req streamTarget
	if err := httpx.DecodeStreamParams(params, &req); err != nil {
		return nil, err
	}
	if req.Task == "" {
		return nil, errors.New("task does not exist")
	}
	if err := h.deleteTask(req.Task); err != nil {
		slog.Error("operation failed", slog.Any("error", err))
		return nil, err
	}
	return nil, nil
}

// callDumpTask returns the task definition as YAML, the same text
// GET /api/v1/task/{task}/dump puts in data, so it can be resubmitted verbatim.
func (h *Handler) callDumpTask(_ context.Context, params jsontext.Value) (any, error) {
	var req streamTarget
	if err := httpx.DecodeStreamParams(params, &req); err != nil {
		return nil, err
	}
	if req.Task == "" {
		return nil, errors.New("task does not exist")
	}
	task, err := h.dumpTask(req.Task)
	if err != nil {
		return nil, err
	}
	data, err := yaml.Marshal(task)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

func (h *Handler) streamStepLog(params jsontext.Value) (httpx.StreamSession, error) {
	var target streamTarget
	if err := httpx.DecodeStreamParams(params, &target); err != nil {
		return nil, err
	}
	if target.Task == "" || target.Step == "" {
		return nil, errors.New("task and step are required")
	}
	if _, err := storage.Task(target.Task).Step(target.Step).Get(); err != nil {
		slog.Error(
			"step log stream",
			slog.String("task", target.Task),
			slog.String("step", target.Step),
			slog.Any("error", err),
		)
		return nil, errors.New("step not found")
	}
	return newStepLogSession(h, target.Task, target.Step), nil
}

// stepLogSession replays the log of one step and follows it until the step
// reaches a terminal state. All state is owned by the subscription goroutine of
// the transport, so no locking is needed.
type stepLogSession struct {
	handler *Handler
	task    string
	step    string

	latest  int64
	marks   map[models.State]*sync.Once
	rounds  map[models.State]func() (bool, error)
	pending []api.StreamFrame
	last    time.Time
	done    bool
}

func newStepLogSession(handler *Handler, taskName, stepName string) *stepLogSession {
	s := &stepLogSession{
		handler: handler,
		task:    taskName,
		step:    stepName,
		latest:  -1,
		marks: map[models.State]*sync.Once{
			models.StatePending: new(sync.Once),
			models.StatePaused:  new(sync.Once),
			models.StateUnknown: new(sync.Once),
		},
	}
	s.rounds = map[models.State]func() (bool, error){
		models.StatePending: s.first(s.marks[models.StatePending], api.CodePending, "step is pending"),
		models.StatePaused:  s.first(s.marks[models.StatePaused], api.CodePaused, "step is paused"),
		models.StateUnknown: s.first(s.marks[models.StateUnknown], api.CodeNoData, "step status unknown"),
		models.StateRunning: s.running,
		models.StateStopped: s.final(api.CodeSuccess),
		models.StateFailed:  s.final(api.CodeFailed),
		models.StateSkipped: s.final(api.CodeSkipped),
	}
	return s
}

func (s *stepLogSession) Next(ctx context.Context) (api.StreamFrame, error) {
	for {
		if len(s.pending) > 0 {
			frame := s.pending[0]
			s.pending = s.pending[1:]
			return frame, nil
		}
		if s.done || ctx.Err() != nil {
			return api.StreamFrame{}, io.EOF
		}
		// Keep the log replay at a fixed pace even while the step is running.
		if wait := stepLogPollInterval - time.Since(s.last); !s.last.IsZero() && wait > 0 {
			if !httpx.WaitPoll(ctx, wait) {
				return api.StreamFrame{}, io.EOF
			}
		}
		s.last = time.Now()
		if err := s.round(); err != nil {
			return api.StreamFrame{}, err
		}
	}
}

// Close is a no-op: the session holds no resource beyond the fields above.
func (s *stepLogSession) Close() {}

func (s *stepLogSession) round() error {
	step, err := storage.Task(s.task).Step(s.step).Get()
	if err != nil {
		slog.Error(
			"step log stream",
			slog.String("task", s.task),
			slog.String("step", s.step),
			slog.Any("error", err),
		)
		return errors.New("step not found")
	}
	next, ok := s.rounds[step.State.OrUnknown()]
	if !ok {
		return errors.New("unhandled step state")
	}
	more, err := next()
	if err != nil {
		slog.Error(
			"step log stream",
			slog.String("task", s.task),
			slog.String("step", s.step),
			slog.Any("error", err),
		)
		return err
	}
	if !more {
		s.done = true
	}
	return nil
}

func (s *stepLogSession) output(code api.Code, data iproto.SStepLogsRes, err error) {
	frame := api.StreamFrame{Code: code}
	if len(data) > 0 {
		frame.Data = data
	}
	if err != nil {
		frame.Message = err.Error()
	}
	s.pending = append(s.pending, frame)
}

// first reports a state that only ever produces a single line: the step is
// waiting, paused or its state is unknown. Further rounds stay silent until the
// state changes.
func (s *stepLogSession) first(once *sync.Once, code api.Code, message string) func() (bool, error) {
	return func() (bool, error) {
		once.Do(func() {
			s.output(code, iproto.SStepLogsRes{{
				Timestamp: time.Now().UnixNano(),
				Line:      1,
				Content:   message,
			}}, nil)
		})
		return true, nil
	}
}

func (s *stepLogSession) running() (bool, error) {
	res, done := s.handler.readStepLogPage(s.task, s.step, &s.latest)
	// New lines are the only news while the step runs; an idle poll says
	// nothing the client does not already know.
	if len(res) > 0 {
		s.output(api.CodeRunning, res, errors.New("in progress"))
	}
	return !done, nil
}

// final drains whatever the step logged after its last page and reports the
// terminal state.
func (s *stepLogSession) final(code api.Code) func() (bool, error) {
	return func() (bool, error) {
		step, err := storage.Task(s.task).Step(s.step).Get()
		if err != nil {
			return false, errors.New("step not found")
		}
		// A final state may already have more than one page. Drain the cursor
		// before sending the terminal response, including marker-only pages
		// (which do not contribute to res).
		var res iproto.SStepLogsRes
		for {
			previous := s.latest
			page, done := s.handler.readStepLogPage(s.task, s.step, &s.latest)
			res = append(res, page...)
			if done || previous == s.latest {
				break
			}
		}
		var errMsg error
		if code == api.CodeFailed {
			errMsg = errors.New(step.Message)
			if step.Message == "" {
				errMsg = fmt.Errorf("exit code: %d", step.Code)
			}
		}
		s.output(code, res, errMsg)
		return false, nil
	}
}
