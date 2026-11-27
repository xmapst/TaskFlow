package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"
	"uuid"

	"github.com/xmapst/xdag"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/internal/config"
	"github.com/xmapst/taskflow/internal/storage"
	"github.com/xmapst/taskflow/internal/storage/models"
	"github.com/xmapst/taskflow/pkg/utility"
)

type sTask struct {
	schedulerControl

	runID       uuid.UUID
	done        chan struct{}
	cleanupOnce sync.Once
	queuedOnce  sync.Once

	stg           *storage.TaskStore
	taskName      string
	workspace     string
	scriptDir     string
	removeWorkdir bool
	dagTasks      map[string]xdag.ITask
}

func (w *Worker) newTaskRun(taskName string, runID uuid.UUID) (_ *sTask, err error) {
	t := &sTask{
		stg:       storage.Task(taskName),
		taskName:  taskName,
		dagTasks:  make(map[string]xdag.ITask),
		scriptDir: filepath.Join(config.App.ScriptDir(), taskName),
		done:      make(chan struct{}),
	}
	if _, exists := inflight.LoadOrStore(taskName, t); exists {
		return nil, errors.New("task already claimed")
	}
	taskRow, err := t.stg.Get()
	if err != nil || (runID.String() != "" && taskRow.ID != runID) ||
		taskRow.State == nil || *taskRow.State != models.StatePending {
		inflight.CompareAndDelete(taskName, t)
		return nil, errors.New("stale or non-pending task delivery")
	}
	t.runID = taskRow.ID
	t.workspace = filepath.Join(config.App.WorkspaceDir(), taskRow.Workdir)
	t.removeWorkdir = taskRow.Cleanup
	defer func() {
		if err != nil {
			t.Stop()
			_ = t.stg.Update(&models.STaskUpdate{
				Message:  err.Error(),
				State:    new(models.StateFailed),
				OldState: new(models.StatePending),
				STime:    new(time.Now()),
				ETime:    new(time.Now()),
			})
			// Release ownership only after the final database write and directory cleanup.
			t.cleanup()
		}
	}()

	for _, stepRow := range t.stg.StepList(storage.All) {
		t.dagTasks[stepRow.Name] = t.newStep(stepRow)
	}
	// New validates and freezes dependencies and installs native control handles
	// before the task becomes visible to management requests.
	t.scheduler, err = xdag.New(t.dagTasks)
	if err != nil {
		return nil, err
	}
	// 加入管理
	for _, task := range t.dagTasks {
		step := task.(*sStep)
		stepManager.Store(step.managerKey(), step)
	}
	taskManager.Store(t.taskName, t)
	return t, nil
}

func (t *sTask) Execute() (err error) {
	defer func() {
		// 清理资源
		t.cleanup()
	}()
	// Preserve pre-start suspension in the database; native xdag owns the gate.
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		// A canceled queued task can still be dispatched while the pool drains.
		// Persist its terminal rows before cleanup removes the manager entry.
		t.discardQueued()
		return api.ErrExecKilled
	}
	t.started = true
	initial := &models.STaskUpdate{
		State:    new(models.StateRunning),
		OldState: new(models.StatePending),
		STime:    new(time.Now()),
		Message:  "task is running",
	}
	if t.scheduler.Suspended() {
		initial.State = new(models.StatePaused)
		initial.OldState = new(models.StateRunning)
		initial.Message = "task is paused"
	}
	err = t.stg.Update(initial)
	t.mu.Unlock()
	if err != nil {
		t.Stop()
		return err
	}

	res := new(models.STaskUpdate)
	executionCtx := context.Background()
	executed := false
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			slog.Error(
				"operation failed",
				slog.Any("panic", r),
				slog.String("stack", string(stack)),
			)
			err = fmt.Errorf("task panic: %v", r)
		}
		if !executed {
			// Setup failures still need native terminal states for every step.
			_ = t.scheduler.Cancel(context.Background(), xdag.WithCause(err))
			_, _ = t.scheduler.Execute(context.Background())
		}
		if !executed {
			t.finalizeSteps(executionCtx)
		}
		res.State = new(models.StateStopped)
		res.Message = "task has stopped"
		res.ETime = new(time.Now())
		res.OldState = new(models.StateRunning)
		if err != nil {
			res.State = new(models.StateFailed)
			res.Message = err.Error()
		}
		if updErr := t.stg.Update(res); updErr != nil {
			slog.Warn(
				"operation warning",
				slog.String("task", t.taskName),
				slog.Any("error", updErr),
			)
		}
	}()

	if err = t.initDir(); err != nil {
		slog.Error(
			"operation failed",
			slog.String("task", t.taskName),
			slog.Any("error", err),
		)
		return
	}

	timeout, err := t.stg.Timeout()
	if err != nil {
		slog.Error(
			"operation failed",
			slog.String("task", t.taskName),
			slog.Any("error", err),
		)
		return
	}
	var ctx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeoutCause(context.Background(), timeout+time.Minute, api.ErrExecTimeOut)
	} else {
		ctx, cancel = context.Background(), func() {}
	}
	defer cancel()

	executionCtx = ctx
	executed = true
	_, err = t.scheduler.Execute(ctx)
	t.finalizeSteps(ctx)
	if err != nil {
		slog.Error(
			"operation failed",
			slog.String("task", t.taskName),
			slog.Any("error", err),
		)
		return
	}
	return nil
}

// Stop cancels through xdag and waits until task bodies have released resources.
func (t *sTask) Stop() {
	t.mu.Lock()
	t.closed = true
	t.clearTimersLocked()
	scheduler := t.scheduler
	t.mu.Unlock()
	if scheduler != nil {
		_ = scheduler.Cancel(context.Background(), xdag.WithCause(api.ErrExecKilled))
	}
}

// discardQueued finalizes tasks that never reached a pool worker. Stop marks
// them closed first, so a concurrent late dispatch cannot start their bodies.
func (t *sTask) discardQueued() {
	t.Stop()
	t.mu.Lock()
	started := t.started
	t.mu.Unlock()
	if started {
		return
	}
	t.queuedOnce.Do(func() {
		t.finalizeSteps(context.Background())
		if err := t.stg.Update(&models.STaskUpdate{
			State:   new(models.StateFailed),
			ETime:   new(time.Now()),
			Message: "task canceled before execution",
		}); err != nil {
			slog.Error(
				"finalize queued task",
				slog.String("task", t.taskName),
				slog.Any("error", err),
			)
		}
		t.cleanup()
	})
}

func (t *sTask) cleanup() {
	t.cleanupOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		t.clearTimersLocked()
		t.mu.Unlock()
		taskManager.CompareAndDelete(t.taskName, t)
		for _, task := range t.dagTasks {
			if step, ok := task.(*sStep); ok {
				stepManager.CompareAndDelete(step.managerKey(), step)
			}
		}
		t.clearDir()
		inflight.CompareAndDelete(t.taskName, t)
		if t.done != nil {
			close(t.done)
		}
	})
}

// xdag deliberately does not invoke hooks for skipped or pre-canceled tasks.
// Persist these terminal states here instead of leaving their rows pending.
func (t *sTask) finalizeSteps(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	t.clearTimersLocked()
	results := t.scheduler.Results()
	for name, state := range t.scheduler.States() {
		step, ok := t.dagTasks[name].(*sStep)
		if !ok {
			continue
		}
		update := &models.SStepUpdate{
			ETime: new(time.Now()),
		}
		switch state {
		case xdag.StatePending:
			if !t.scheduler.Canceled() {
				continue
			}
			fallthrough
		case xdag.StateCanceled:
			update.State = new(models.StateFailed)
			update.Code = new(api.ExecCodeKilled)
			update.Message = "step has been canceled"
			if errors.Is(context.Cause(ctx), api.ErrExecTimeOut) ||
				errors.Is(results[name].Err, api.ErrExecTimeOut) {
				update.Code = new(api.ExecCodeTimeout)
				update.Message = api.ErrExecTimeOut.Error()
			}
		case xdag.StateSkipped:
			update.State = new(models.StateSkipped)
			update.Code = new(api.ExecCodeSkipped)
			update.Message = "skipped because dependency triggers did not match"
		case xdag.StateSuccess, xdag.StateFailed:
			if step.lastResult != nil {
				stepUpdate := *step.lastResult
				update = &stepUpdate
			} else {
				update.State = new(models.StateFailed)
				update.Code = new(api.ExecCodeSystemErr)
				update.Message = "step failed before execution completed"
			}
			if state == xdag.StateFailed && results[name].Err != nil {
				update.State = new(models.StateFailed)
				update.Message = results[name].Err.Error()
			}
		default:
			continue
		}
		if err := step.stg.Update(update); err != nil {
			slog.Error(
				"persist scheduler terminal state",
				slog.String("task", t.taskName),
				slog.String("step", name),
				slog.Any("error", err),
			)
		}
	}
}

func (t *sTask) initDir() error {
	if err := utility.EnsureDirExist(t.workspace); err != nil {
		slog.Error(
			"operation failed",
			slog.String("task", t.taskName),
			slog.String("workspace", t.workspace),
			slog.String("script_dir", t.scriptDir),
			slog.Any("error", err),
		)
		return err
	}
	if err := utility.EnsureDirExist(t.scriptDir); err != nil {
		slog.Error(
			"operation failed",
			slog.String("task", t.taskName),
			slog.Any("error", err),
		)
		return err
	}
	return nil
}

func (t *sTask) clearDir() {
	if err := os.RemoveAll(t.scriptDir); err != nil {
		slog.Error(
			"operation failed",
			slog.String("task", t.taskName),
			slog.Any("error", err),
		)
	}
	if !t.removeWorkdir {
		return
	}
	if err := os.RemoveAll(t.workspace); err != nil {
		slog.Error(
			"operation failed",
			slog.String("task", t.taskName),
			slog.Any("error", err),
		)
	}
}

func (t *sTask) newStep(stepRow *models.SStep) *sStep {
	return &sStep{
		kind:        stepRow.Kind,
		retryPolicy: stepRow.RetryPolicy.Data(),
		routing:     stepRow.Routing.Data(),
		taskName:    t.taskName,
		stepName:    stepRow.Name,
		stg:         t.stg.StepFor(stepRow),
		owner:       t,
		workspace:   t.workspace,
		scriptDir:   t.scriptDir,
	}
}
