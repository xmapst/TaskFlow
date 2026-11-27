package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/xmapst/xdag"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/api/iproto"
	"github.com/xmapst/taskflow/internal/runner"
	"github.com/xmapst/taskflow/internal/storage"
	"github.com/xmapst/taskflow/internal/storage/models"
	"github.com/xmapst/taskflow/internal/worker/event"
)

type sStep struct {
	owner       *sTask
	lastResult  *models.SStepUpdate
	retryPolicy *iproto.SRetryPolicy
	routing     *iproto.SStepRouting
	inbox       atomic.Pointer[runner.Inbox]
	stg         *storage.StepStore
	kind        string
	taskName    string
	stepName    string
	workspace   string
	scriptDir   string
}

func (s *sStep) Name() string {
	return s.stepName
}

func (s *sStep) managerKey() string { return stepManagerKey(s.taskName, s.stepName) }

// signal forwards an external signal to the running attempt, if any.
func (s *sStep) signal(sig runner.Signal) bool {
	return s.inbox.Load().Deliver(sig)
}

func (s *sStep) Dependencies() xdag.DependencySpec {
	return iproto.DAGDependencies(s.routing)
}

func (s *sStep) RetryPolicy() xdag.RetryPolicy {
	retryPolicy := s.retryPolicy
	if retryPolicy == nil || retryPolicy.MaxAttempts == 0 {
		return xdag.RetryPolicy{}
	}
	return xdag.RetryPolicy{
		Interval:    retryPolicy.Interval,
		MaxAttempts: retryPolicy.MaxAttempts,
		MaxInterval: retryPolicy.MaxInterval,
		Multiplier:  retryPolicy.Multiplier,
		Jitter:      retryPolicy.Jitter,
	}
}

// Match xdag's retry decision when presenting the step's intermediate state.
func (s *sStep) willRetry(ctx context.Context, attempt int64, err error) bool {
	if s.retryPolicy == nil ||
		(s.retryPolicy.MaxAttempts >= 0 && attempt >= s.retryPolicy.MaxAttempts) ||
		ctx.Err() != nil || err == nil {
		return false
	}
	return !errors.Is(err, xdag.ErrNonRetryable) &&
		!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) &&
		!errors.Is(err, xdag.ErrTaskCanceled) && !errors.Is(err, xdag.ErrRunCanceled)
}

func (s *sStep) PreExecution(ctx context.Context, attempt int64, input xdag.Input) {
	slog.Info(
		"operation status",
		slog.String("task", s.taskName),
		slog.String("step", s.stepName),
		slog.String("workspace", s.workspace),
		slog.String("phase", "PreExecution"),
	)
	event.Sendf("%s %s PreExecution", s.taskName, s.stepName)
}

func (s *sStep) Execute(ctx context.Context, attempt int64, _ xdag.Input) (output xdag.Output, err error) {
	if err := ctx.Err(); err != nil {
		return xdag.Output{}, err
	}
	slog.Info(
		"operation status",
		slog.String("task", s.taskName),
		slog.String("step", s.stepName),
		slog.String("workspace", s.workspace),
		slog.String("phase", "Execute"),
	)
	event.Sendf("%s %s Execute", s.taskName, s.stepName)
	if err = s.stg.Update(&models.SStepUpdate{
		State:    new(models.StateRunning),
		OldState: new(models.StatePending),
		Message:  "step is running",
		STime:    new(time.Now()),
	}); err != nil {
		slog.Error(
			"operation failed",
			slog.String("task", s.taskName),
			slog.String("step", s.stepName),
			slog.Any("error", err),
		)
		event.Sendf("%s %s error %v", s.taskName, s.stepName, err)
		return xdag.Output{}, err
	}

	var res = new(models.SStepUpdate)
	defer func() {
		if _err := recover(); _err != nil {
			stack := string(debug.Stack())
			slog.Error(
				"operation failed",
				slog.String("stack", stack),
				slog.Any("error", _err),
			)
			err = fmt.Errorf("step panic: %v", _err)
			res.State = new(models.StateFailed)
			res.Code = new(api.ExecCodeSystemErr)
			res.Message = err.Error()
		}
		if ctx.Err() != nil {
			err = ctx.Err()
			res.State = new(models.StateFailed)
			res.Code = new(api.ExecCodeKilled)
			res.Message = "step has been canceled"
			if errors.Is(context.Cause(ctx), api.ErrExecTimeOut) {
				res.Code = new(api.ExecCodeTimeout)
				res.Message = api.ErrExecTimeOut.Error()
			}
		}
		res.ETime = new(time.Now())
		res.OldState = new(models.StateRunning)
		if s.owner != nil {
			s.owner.mu.Lock()
			defer s.owner.mu.Unlock()
			s.lastResult = res
		}
		update := res
		if s.willRetry(ctx, attempt, err) {
			update = &models.SStepUpdate{
				State:    new(models.StatePending),
				OldState: new(models.StateRunning),
				Message:  fmt.Sprintf("attempt %d failed; waiting to retry: %s", attempt, res.Message),
			}
			if s.owner != nil && s.owner.pauses[s.stepName] {
				update.State = new(models.StatePaused)
				update.OldState = new(models.StatePending)
			}
		}
		if _err := s.stg.Update(update); _err != nil {
			slog.Error(
				"operation failed",
				slog.Any("error", _err),
			)
		}
		event.Sendf("%s %s %v", s.taskName, s.stepName, update.Message)
	}()

	s.stg.Log().Write(api.ExecConsoleStart)
	defer s.stg.Log().Write(api.ExecConsoleDone)
	data, err := s.run(ctx, res)
	if err != nil {
		return xdag.Output{}, err
	}
	output = xdag.Output{Data: data}
	if s.routing != nil {
		for _, label := range s.routing.Emit {
			output.Emit = append(output.Emit, xdag.Trigger(label))
		}
	}
	return output, nil
}

// run creates the runner, executes it and records the outcome in res.
func (s *sStep) run(ctx context.Context, res *models.SStepUpdate) (map[string]any, error) {
	_runner, err := s.newExecutorRunner()
	if err != nil {
		slog.Error(
			"operation failed",
			slog.String("task", s.taskName),
			slog.String("step", s.stepName),
			slog.Any("error", err),
		)
		res.State = new(models.StateFailed)
		res.Message = err.Error()
		res.Code = new(api.ExecCodeSystemErr)
		return nil, err
	}
	defer func() {
		if cErr := _runner.Clear(); cErr != nil {
			slog.Warn(
				"operation warning",
				slog.Any("error", cErr),
			)
		}
	}()

	res.Message = "execution succeed"
	// Each attempt gets a fresh inbox so late signals for an earlier attempt are
	// dropped. Runners that do not wait for external signals ignore it.
	inbox := runner.NewInbox()
	s.inbox.Store(inbox)
	defer s.inbox.CompareAndSwap(inbox, nil)
	code, err := _runner.Run(runner.WithInbox(ctx, inbox))
	res.Code = new(code)
	res.State = new(models.StateStopped)
	if err != nil {
		res.State = new(models.StateFailed)
		res.Message = err.Error()
		return nil, err
	}
	if code != 0 {
		res.State = new(models.StateFailed)
		res.Message = fmt.Sprintf("execution failed with code: %d", code)
		return nil, errors.New(res.Message)
	}
	return s.stepOutput(models.StateStopped, code), nil
}

func (s *sStep) PostExecution(ctx context.Context, attempt int64, output xdag.Output, err error) {
	slog.Info(
		"operation status",
		slog.String("task", s.taskName),
		slog.String("step", s.stepName),
		slog.String("workspace", s.workspace),
		slog.String("phase", "PostExecution"),
	)
	event.Sendf("%s %s PostExecution", s.taskName, s.stepName)
}

func (s *sStep) Stop() {
	if s.owner != nil {
		_ = s.owner.control(s.stepName, "kill", "")
	}
}

func (s *sStep) stepOutput(state models.State, code api.ExecCode) map[string]any {
	return map[string]any{"state": models.StateMap[state], "code": int64(code)}
}

func (s *sStep) newExecutorRunner() (runner.IRunner, error) {
	executor, err := runner.Get(s.kind)
	if err != nil {
		return nil, err
	}
	return executor(s.stg, s.workspace, s.scriptDir)
}
