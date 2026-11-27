package tunny

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Errors that are used throughout the Tunny API.
var (
	ErrResizeInProgress = errors.New("worker pool resize in progress")
	ErrPoolBusy         = errors.New("no execution worker is available")
	ErrPoolNotRunning   = errors.New("the pool is not running")
	ErrWorkerClosed     = errors.New("worker was closed")
	ErrJobTimedOut      = errors.New("job request timed out")
)

// Worker processes jobs synchronously and provides hooks for readiness,
// interruption, cleanup, and association with a pool.
type Worker interface {
	// Process will synchronously perform a job and return the result.
	Process(Handler) error

	// BlockUntilReady is called before each job is processed and must block the
	// calling goroutine until the Worker is ready to process the next job.
	BlockUntilReady()

	// Interrupt is called when a job is cancelled. The worker is responsible
	// for unblocking the Process implementation.
	Interrupt()

	// Terminate is called when a Worker is removed from the processing pool
	// and is responsible for cleaning up any held resources.
	Terminate()

	// BindPool associates the worker with p and returns the worker to use.
	BindPool(p *Pool) Worker
}

// closureWorker delegates each job to its processor function.
type closureWorker struct {
	pool      *Pool
	processor func(Handler) error
}

func (w *closureWorker) Process(payload Handler) error {
	return w.processor(payload)
}

func (w *closureWorker) BlockUntilReady()        {}
func (w *closureWorker) Interrupt()              {}
func (w *closureWorker) Terminate()              {}
func (w *closureWorker) BindPool(p *Pool) Worker { w.pool = p; return w }

// callbackWorker calls the Handler directly and returns its error.
type callbackWorker struct {
	pool *Pool
}

func (w *callbackWorker) Process(payload Handler) error {
	return payload()
}

func (w *callbackWorker) BlockUntilReady()        {}
func (w *callbackWorker) Interrupt()              {}
func (w *callbackWorker) Terminate()              {}
func (w *callbackWorker) BindPool(p *Pool) Worker { w.pool = p; return w }

// Pool manages a resizable set of workers, each running in its own goroutine.
type Pool struct {
	queuedJobs atomic.Int64
	size       atomic.Int64

	ctor    func() Worker
	workers []*workerWrapper
	reqChan chan workRequest

	workerMut sync.Mutex
	closed    bool
}

// New creates a new Pool of workers that starts with n workers. You must
// provide a constructor function that creates new Worker types and when you
// change the size of the pool the constructor will be called to create each new
// Worker.
func New(n int, ctor func() Worker) *Pool {
	p := &Pool{
		ctor:    ctor,
		reqChan: make(chan workRequest),
	}
	p.SetSize(n)

	return p
}

// NewFunc creates a new Pool of workers where each worker will process using
// the provided func.
func NewFunc(n int, f func(Handler) error) *Pool {
	return New(n, func() Worker {
		return &closureWorker{
			processor: f,
		}
	})
}

// NewCallback creates a pool of n workers that run each Handler directly.
func NewCallback(n int) *Pool {
	return New(n, func() Worker {
		return &callbackWorker{}
	})
}

// Process runs a job and waits for its result. It is safe for concurrent use
// and returns ErrPoolNotRunning if the pool has stopped.
func (p *Pool) Process(payload Handler) error {
	p.queuedJobs.Add(1)
	defer p.queuedJobs.Add(-1)

	request, open := <-p.reqChan
	if !open {
		return ErrPoolNotRunning
	}

	request.jobChan <- payload

	var ret error
	ret, open = <-request.retChan
	if !open {
		return ErrWorkerClosed
	}
	return ret
}

// ProcessTimed will use the Pool to process a payload and synchronously return
// the result. If the timeout occurs before the job has finished the worker will
// be interrupted and ErrJobTimedOut will be returned. ProcessTimed can be
// called safely by any goroutines.
func (p *Pool) ProcessTimed(
	payload Handler,
	timeout time.Duration,
) error {
	p.queuedJobs.Add(1)
	defer p.queuedJobs.Add(-1)

	tout := time.NewTimer(timeout)

	var request workRequest
	var open bool

	select {
	case request, open = <-p.reqChan:
		if !open {
			return ErrPoolNotRunning
		}
	case <-tout.C:
		return ErrJobTimedOut
	}

	select {
	case request.jobChan <- payload:
	case <-tout.C:
		close(request.abandonChan)
		return ErrJobTimedOut
	}

	var ret error
	select {
	case ret, open = <-request.retChan:
		if !open {
			return ErrWorkerClosed
		}
	case <-tout.C:
		request.interrupt()
		return ErrJobTimedOut
	}

	tout.Stop()
	return ret
}

// ProcessCtx uses the Pool to process a payload and synchronously return its
// result. If the context cancels, it returns ctx.Err() and interrupts work
// already handed to a worker. It is safe for concurrent use.
func (p *Pool) ProcessCtx(ctx context.Context, payload Handler) error {
	p.queuedJobs.Add(1)
	defer p.queuedJobs.Add(-1)

	var request workRequest
	var open bool

	select {
	case request, open = <-p.reqChan:
		if !open {
			return ErrPoolNotRunning
		}
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case request.jobChan <- payload:
	case <-ctx.Done():
		close(request.abandonChan)
		return ctx.Err()
	}

	var ret error
	select {
	case ret, open = <-request.retChan:
		if !open {
			return ErrWorkerClosed
		}
	case <-ctx.Done():
		request.interrupt()
		return ctx.Err()
	}

	return ret
}

// Submit hands a payload to an available worker without waiting for its result.
func (p *Pool) Submit(payload Handler) error {
	return p.SubmitCtx(context.Background(), payload)
}

// TrySubmit hands off a job only if a worker is ready. It does not queue the
// job or wait for its result.
func (p *Pool) TrySubmit(payload Handler) error {
	select {
	case request, open := <-p.reqChan:
		if !open {
			return ErrPoolNotRunning
		}
		// The worker has reserved this handoff and is waiting for the payload.
		p.queuedJobs.Add(1)
		request.asyncJobChan <- payload
		return nil
	default:
		return ErrPoolBusy
	}
}

// SubmitCtx waits for an available worker and hands it the payload. The context
// can cancel that wait or the handoff; it does not cancel a handed-off job or
// wait for its result. When cancellation and handoff race, either can win.
func (p *Pool) SubmitCtx(ctx context.Context, payload Handler) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	p.queuedJobs.Add(1)
	handedOff := false
	defer func() {
		if !handedOff {
			p.queuedJobs.Add(-1)
		}
	}()

	var request workRequest
	select {
	case req, open := <-p.reqChan:
		if !open {
			return ErrPoolNotRunning
		}
		request = req
	case <-ctx.Done():
		return ctx.Err()
	}

	if err := ctx.Err(); err != nil {
		close(request.abandonChan)
		return err
	}

	select {
	case request.asyncJobChan <- payload:
		// The worker now owns the queue count and releases it on completion.
		handedOff = true
		return nil
	case <-ctx.Done():
		close(request.abandonChan)
		return ctx.Err()
	}
}

// QueueLength returns the count of jobs waiting for or assigned to a worker.
func (p *Pool) QueueLength() int64 {
	return p.queuedJobs.Load()
}

// SetSize changes the number of workers. Calls after Close do nothing.
func (p *Pool) SetSize(n int) {
	p.workerMut.Lock()
	defer p.workerMut.Unlock()
	if !p.closed {
		p.setSize(n)
	}
}

// TrySetSize rejects overlapping adjustments without waiting for them.
func (p *Pool) TrySetSize(n int) error {
	if !p.workerMut.TryLock() {
		return ErrResizeInProgress
	}
	defer p.workerMut.Unlock()
	if p.closed {
		return ErrPoolNotRunning
	}
	p.setSize(n)
	return nil
}

func (p *Pool) setSize(n int) {
	lWorkers := len(p.workers)
	if lWorkers == n {
		return
	}

	// Add extra workers if N > len(workers)
	for i := lWorkers; i < n; i++ {
		p.workers = append(p.workers, newWorkerWrapper(p, p.ctor().BindPool(p)))
	}

	// Asynchronously stop all workers > N
	for i := n; i < lWorkers; i++ {
		p.workers[i].stop()
	}

	// Synchronously wait for all workers > N to stop
	for i := n; i < lWorkers; i++ {
		p.workers[i].join()
		p.workers[i] = nil
	}

	// Remove stopped workers from slice
	p.workers = p.workers[:n]
	p.size.Store(int64(n))
}

// GetSize returns the most recently applied size without waiting for a resize.
func (p *Pool) GetSize() int {
	return int(p.size.Load())
}

// Close terminates all workers and closes the job channel.
func (p *Pool) Close() {
	p.workerMut.Lock()
	defer p.workerMut.Unlock()
	if p.closed {
		return
	}
	p.setSize(0)
	close(p.reqChan)
	p.closed = true
}
