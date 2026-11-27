package tunny

type Handler func() error

// workRequest is an invitation for one caller to hand a payload to a worker.
type workRequest struct {
	jobChan      chan<- Handler
	asyncJobChan chan<- Handler
	retChan      <-chan error

	// The caller closes abandonChan when it will not send a job or receive
	// the synchronous result. It is unique to this request.
	abandonChan chan<- struct{}

	// interruptFunc cancels a running job, not an unused request.
	interruptFunc func()
}

func (r workRequest) interrupt() {
	// Keep the worker from accepting another job before Interrupt returns.
	defer close(r.abandonChan)
	r.interruptFunc()
}

// workerWrapper manages the lifetime of a Worker and its goroutine.
type workerWrapper struct {
	pool   *Pool
	worker Worker

	// closeChan can be closed in order to cleanly shutdown this worker.
	closeChan chan struct{}

	// closedChan is closed by the run() goroutine when it exits.
	closedChan chan struct{}
}

func newWorkerWrapper(pool *Pool, worker Worker) *workerWrapper {
	w := &workerWrapper{
		pool:       pool,
		worker:     worker,
		closeChan:  make(chan struct{}),
		closedChan: make(chan struct{}),
	}
	go w.run()
	return w
}

func (w *workerWrapper) run() {
	jobChan, retChan, asyncJobChan := make(chan Handler), make(chan error), make(chan Handler)
	defer func() {
		w.worker.Terminate()
		close(retChan)
		close(w.closedChan)
	}()

	for {
		select {
		case <-w.closeChan:
			return
		default:
		}
		// NOTE: Blocking here will prevent the worker from closing down.
		w.worker.BlockUntilReady()
		abandonChan := make(chan struct{})
		nextReq := workRequest{
			jobChan:       jobChan,
			retChan:       retChan,
			asyncJobChan:  asyncJobChan,
			abandonChan:   abandonChan,
			interruptFunc: w.worker.Interrupt,
		}

		select {
		case w.pool.reqChan <- nextReq:
		case <-w.closeChan:
			return
		}

		// Once a request has been received, its caller must either deliver
		// the payload or abandon the request before this worker can stop.
		select {
		case payload := <-jobChan:
			result := w.worker.Process(payload)
			select {
			case retChan <- result:
			case <-abandonChan:
			}
		case payload := <-asyncJobChan:
			_ = w.worker.Process(payload)
			w.pool.queuedJobs.Add(-1)
		case <-abandonChan:
		}
	}
}

func (w *workerWrapper) stop() {
	close(w.closeChan)
}

func (w *workerWrapper) join() {
	<-w.closedChan
}
