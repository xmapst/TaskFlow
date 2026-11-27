package worker

import (
	"errors"

	"github.com/xmapst/taskflow/api/iproto"
	"github.com/xmapst/taskflow/internal/chanrpc"
	"github.com/xmapst/taskflow/internal/runner"
)

func stepManagerKey(taskName, stepName string) string { return taskName + "/" + stepName }

func (w *Worker) startRPC() error {
	return errors.Join(
		chanrpc.Register(w.dispatchTask),
		chanrpc.Register(w.controlTask),
		chanrpc.Register(w.resizePool),
		chanrpc.Register(w.approveStep),
		chanrpc.Register(func(iproto.WorkerPoolSizeQuery) (int, error) {
			return w.pool.GetSize(), w.ctx.Err()
		}),
	)
}

// approveStep routes a decision to the step it names, like controlTask does. An
// unknown step or a rejected token is not an error: the caller cannot tell a
// stale decision from an accepted one, and must not be able to probe for steps.
func (w *Worker) approveStep(message iproto.ApprovalDecision) (struct{}, error) {
	if err := w.ctx.Err(); err != nil {
		return struct{}{}, err
	}
	if value, ok := stepManager.Load(stepManagerKey(message.TaskName, message.StepName)); ok {
		if step, ok := value.(*sStep); ok {
			step.signal(runner.Signal{Token: message.ID, Status: message.Status})
		}
	}
	return struct{}{}, nil
}

func (w *Worker) dispatchTask(message iproto.TaskSubmission) (struct{}, error) {
	if message.ID.String() == "" {
		return struct{}{}, errors.New("task submission requires a run ID")
	}
	// Finish claiming and handing off the task before shutdown stops schedulers.
	w.lifecycleMu.RLock()
	defer w.lifecycleMu.RUnlock()
	if err := w.ctx.Err(); err != nil {
		return struct{}{}, err
	}
	claimed := make(chan error, 1)
	if err := w.pool.TrySubmit(func() error {
		task, err := w.newTaskRun(message.Name, message.ID)
		claimed <- err
		if err != nil {
			return err
		}
		return task.Execute()
	}); err != nil {
		return struct{}{}, err
	}
	return struct{}{}, <-claimed
}

func (w *Worker) controlTask(message iproto.TaskControl) (struct{}, error) {
	w.lifecycleMu.RLock()
	if err := w.ctx.Err(); err != nil {
		w.lifecycleMu.RUnlock()
		return struct{}{}, err
	}
	w.controls.Add(1)
	w.lifecycleMu.RUnlock()
	defer w.controls.Done()
	value, ok := taskManager.Load(message.Name)
	task, valid := value.(*sTask)
	if !ok || !valid || task.runID != message.ID {
		return struct{}{}, errors.New("stale or unclaimed task control")
	}
	return struct{}{}, task.control(message.Step, message.Action, message.Duration)
}

func (w *Worker) resizePool(message iproto.WorkerPoolMessage) (struct{}, error) {
	if err := message.Validate(); err != nil {
		return struct{}{}, err
	}
	if err := w.ctx.Err(); err != nil {
		return struct{}{}, err
	}
	return struct{}{}, w.pool.TrySetSize(message.Size)
}
