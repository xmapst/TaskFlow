// Package chanrpc owns the application's process-wide ChanRPC dispatcher.
package chanrpc

import (
	"context"
	"errors"
	"sync"

	"github.com/xmapst/xhive/chanrpc"

	"github.com/xmapst/taskflow/pkg/tunny"
)

var active = newDispatcher()

var errUnavailable = errors.New("RPC dispatcher unavailable")

type dispatcher struct {
	server *chanrpc.Server
	client *chanrpc.Client
	mu     sync.RWMutex
	ctx    context.Context
}

func newDispatcher() *dispatcher {
	return &dispatcher{
		server: chanrpc.NewServer(chanrpc.WithChanLen(256)),
		client: chanrpc.NewClient(),
	}
}

// Start starts dispatching registered handlers. The returned channel closes
// once the context is canceled and the dispatcher and its worker pool stop.
func Start(ctx context.Context) (<-chan struct{}, error) {
	return active.start(ctx)
}

func (d *dispatcher) start(ctx context.Context) (<-chan struct{}, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ctx != nil {
		return nil, errors.New("RPC dispatcher already started")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.ctx = ctx
	pool := tunny.NewCallback(15)
	stop := make(chan struct{})
	done := make(chan struct{})
	context.AfterFunc(ctx, func() {
		// Keep dispatching until every admitted caller has released its read lock.
		d.mu.Lock()
		close(stop)
		d.mu.Unlock()
	})
	go func() {
		d.serve(pool, stop)
		pool.Close()
		d.server.Close()
		d.client.Close()
		close(done)
	}()
	return done, nil
}

func (d *dispatcher) serve(pool *tunny.Pool, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-d.client.NotEmpty():
			reply, ok := d.client.Pop()
			if !ok {
				continue
			}
			if err := pool.Submit(func() error {
				d.client.AsyncCallback(reply)
				return nil
			}); err != nil {
				d.client.AsyncCallback(reply)
			}
		case <-d.server.NotEmpty():
			call, ok := d.server.Pop()
			if !ok {
				continue
			}
			if err := pool.Submit(func() error {
				d.server.Exec(call)
				return nil
			}); err != nil {
				// Complete every popped call, even if handoff fails.
				d.server.Exec(call)
			}
		}
	}
}

// Register binds a request type to a handler before Start. Handlers run
// concurrently and must not call back into the same dispatcher.
func Register[Req, Ack any](handler func(Req) (Ack, error)) error {
	d := active
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ctx != nil {
		return errors.New("register RPC handlers before starting the dispatcher")
	}
	var prototype Req
	return d.server.Register(prototype, func(call *chanrpc.CallInfo) *chanrpc.RetInfo {
		request, ok := call.RequestAs[Req]()
		if !ok {
			return &chanrpc.RetInfo{Err: errors.New("unexpected RPC request type")}
		}
		ack, err := handler(request)
		return &chanrpc.RetInfo{Ack: ack, Err: err}
	})
}

func (d *dispatcher) call(ctx context.Context, request any) *chanrpc.RetInfo {
	if err := ctx.Err(); err != nil {
		return &chanrpc.RetInfo{Err: err}
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.ctx == nil || d.ctx.Err() != nil {
		return &chanrpc.RetInfo{Err: errUnavailable}
	}
	return d.client.CallWithContext(ctx, d.server, request)
}

// Call waits for a typed reply from the handler.
func Call[Ack any](ctx context.Context, request any) (Ack, error) {
	var zero Ack
	ret := active.call(ctx, request)
	if ret.Err != nil {
		return zero, ret.Err
	}
	ack, ok := ret.AckAs[Ack]()
	if !ok {
		return zero, errors.New("unexpected RPC response type")
	}
	return ack, nil
}

// Submit checks cancellation before dispatch. Once dispatched, it waits for a
// definitive acknowledgement: a canceled wait could otherwise make a caller
// roll back data for a command the worker has already accepted.
func Submit(ctx context.Context, request any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := Call[struct{}](context.WithoutCancel(ctx), request)
	return err
}
