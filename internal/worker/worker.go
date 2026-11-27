package worker

import (
	"context"
	"log/slog"
	"sync"

	"github.com/xmapst/taskflow/internal/runner"
	"github.com/xmapst/taskflow/pkg/tunny"
)

var (
	inflight    sync.Map
	taskManager sync.Map
	stepManager sync.Map
)

type Worker struct {
	ctx         context.Context
	cancel      context.CancelFunc
	pool        *tunny.Pool
	closeOnce   sync.Once
	lifecycleMu sync.RWMutex
	controls    sync.WaitGroup
}

func New(ctx context.Context) *Worker {
	ctx, cancel := context.WithCancel(ctx)
	w := &Worker{
		ctx:    ctx,
		cancel: cancel,
		pool:   tunny.NewCallback(1),
	}
	return w
}

func (w *Worker) Start() error {
	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()
	if err := w.ctx.Err(); err != nil {
		return err
	}
	slog.Info(
		"number of workers",
		slog.Int("workers", w.pool.GetSize()),
	)
	// 打印当前支持的runner
	slog.Info(
		"runner",
		slog.Any("runners", runner.ListAvailable()),
	)

	return w.startRPC()
}

func (w *Worker) SetSize(n int) {
	if w.ctx.Err() == nil {
		w.pool.SetSize(n)
	}
}

func (w *Worker) Shutdown() {
	w.closeOnce.Do(w.shutdown)
}

func (w *Worker) shutdown() {
	w.lifecycleMu.Lock()
	w.cancel()
	w.lifecycleMu.Unlock()

	// A suspended scheduler can otherwise keep a pool worker blocked forever.
	var wg sync.WaitGroup
	taskManager.Range(func(_, value any) bool {
		if task, ok := value.(*sTask); ok {
			wg.Go(task.Stop)
		}
		return true
	})
	wg.Wait()
	w.controls.Wait()
	w.pool.Close()
	// Clean up any claims that did not reach Execute.
	taskManager.Range(func(_, value any) bool {
		if task, ok := value.(*sTask); ok {
			task.discardQueued()
		}
		return true
	})
}
