package worker

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/xmapst/xdag"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/internal/storage/models"
)

// Scheduler owns all execution gates and cancellation contexts. This lock only
// serializes database bookkeeping and optional auto-resume timers with cleanup.
type schedulerControl struct {
	mu        sync.Mutex
	scheduler *xdag.Scheduler
	timers    map[string]*time.Timer
	pauses    map[string]bool
	closed    bool
	started   bool
}

func (t *sTask) clearTimersLocked() {
	clear(t.pauses)
	for name, timer := range t.timers {
		timer.Stop()
		delete(t.timers, name)
	}
}

func (t *sTask) stopTimerLocked(name string) {
	if timer := t.timers[name]; timer != nil {
		timer.Stop()
		delete(t.timers, name)
	}
}

func (t *sTask) control(name, action, duration string) error {
	t.mu.Lock()
	if t.closed || t.scheduler == nil {
		t.mu.Unlock()
		return errors.New("task is no longer controllable")
	}
	if action == "kill" && name == "" {
		t.closed = true
		t.clearTimersLocked()
		scheduler := t.scheduler
		started := t.started
		t.mu.Unlock()
		// Execute owns active terminal writes and cleanup. Waiting only for xdag
		// is insufficient: its outer persistence may not have completed yet.
		if err := scheduler.Cancel(context.Background(), xdag.WithCause(api.ErrExecKilled)); err != nil {
			return err
		}
		if started {
			if t.done != nil {
				<-t.done
			}
		} else {
			t.discardQueued()
		}
		return nil
	}
	defer t.mu.Unlock()
	return t.controlLocked(name, action, duration)
}

func (t *sTask) controlLocked(name, action, duration string) error {
	if action != "pause" && action != "resume" && action != "kill" {
		return nil
	}
	if name == "" {
		if t.scheduler.Canceled() {
			return errors.New("task has been canceled")
		}
		row, err := t.stg.Get()
		if err != nil {
			return err
		}
		if action == "pause" {
			if t.scheduler.Suspended() {
				return nil
			}
			t.scheduler.Suspend()
			if err = t.stg.Update(&models.STaskUpdate{
				State:    new(models.StatePaused),
				OldState: row.State,
				Message:  "has been paused",
			}); err != nil {
				t.scheduler.Resume()
				return err
			}
		} else {
			t.scheduler.Resume()
			t.stopTimerLocked(name)
			delete(t.pauses, name)
			if row.State != nil && *row.State == models.StatePaused {
				if err = t.stg.Update(&models.STaskUpdate{
					State:    row.OldState,
					OldState: row.State,
					Message:  "has been resumed",
				}); err != nil {
					return err
				}
			}
		}
	} else {
		step, ok := t.dagTasks[name].(*sStep)
		if !ok {
			return errors.New("step not found")
		}
		row, err := step.stg.Get()
		if err != nil {
			return err
		}
		switch action {
		case "pause":
			if t.pauses[name] {
				return nil
			}
			if err = t.scheduler.SuspendTask(name); err != nil {
				return err
			}
			if err = step.stg.Update(&models.SStepUpdate{
				State:    new(models.StatePaused),
				OldState: row.State,
				Message:  "has been paused",
			}); err != nil {
				_ = t.scheduler.ResumeTask(name)
				return err
			}
		case "resume":
			t.stopTimerLocked(name)
			delete(t.pauses, name)
			if err = t.scheduler.ResumeTask(name); err != nil {
				return err
			}
			if row.State != nil && *row.State == models.StatePaused {
				if err = step.stg.Update(&models.SStepUpdate{
					State:    row.OldState,
					OldState: row.State,
					Message:  "has been resumed",
				}); err != nil {
					return err
				}
			}
		case "kill":
			t.stopTimerLocked(name)
			delete(t.pauses, name)
			if err = t.scheduler.CancelTask(name, xdag.WithCause(api.ErrExecKilled)); err != nil {
				return err
			}
			return step.stg.Update(&models.SStepUpdate{
				State:    new(models.StateFailed),
				OldState: row.State,
				Code:     new(api.ExecCodeKilled),
				ETime:    new(time.Now()),
				Message:  "has been killed",
			})
		}
	}
	if action == "pause" {
		if t.pauses == nil {
			t.pauses = make(map[string]bool)
		}
		t.pauses[name] = true
		t.stopTimerLocked(name)
		if d, err := time.ParseDuration(duration); err == nil && d > 0 {
			if t.timers == nil {
				t.timers = make(map[string]*time.Timer)
			}
			var timer *time.Timer
			timer = time.AfterFunc(d, func() {
				t.mu.Lock()
				defer t.mu.Unlock()
				if t.closed || t.timers[name] != timer {
					return
				}
				delete(t.timers, name)
				_ = t.controlLocked(name, "resume", "")
			})
			t.timers[name] = timer
		}
	}
	return nil
}
