package runner

import (
	"context"
	"sync"
)

// Signal is an external event addressed to a running step. Token proves the
// sender knows which invocation it is answering; Status carries the payload.
type Signal struct {
	Token  string
	Status string
}

// Inbox lets a running step wait for one external Signal. The worker creates a
// fresh Inbox for every attempt and hands it to the runner through the context,
// so runners need no global registry and stale signals cannot cross attempts.
type Inbox struct {
	mu     sync.Mutex
	accept func(Signal) bool // nil while nobody is waiting
	ch     chan Signal
}

func NewInbox() *Inbox {
	return &Inbox{ch: make(chan Signal, 1)}
}

type inboxKey struct{}

func WithInbox(ctx context.Context, in *Inbox) context.Context {
	return context.WithValue(ctx, inboxKey{}, in)
}

// InboxFrom returns the Inbox attached to ctx, or nil.
func InboxFrom(ctx context.Context) *Inbox {
	in, _ := ctx.Value(inboxKey{}).(*Inbox)
	return in
}

// Deliver offers a signal to the waiter. It never blocks and reports whether the
// signal was accepted: it is dropped when nobody waits, when accept rejects it,
// or when an earlier signal already decided the wait.
func (in *Inbox) Deliver(s Signal) bool {
	if in == nil {
		return false
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.accept == nil || !in.accept(s) {
		return false
	}
	select {
	case in.ch <- s:
		return true
	default:
		return false
	}
}

// Await arms the inbox with accept, runs ready, then blocks for the first
// accepted signal or ctx cancellation. Arming happens before ready so a signal
// that races ahead of ready's return is not lost. A ready error aborts the wait.
// Only one Await may be active per Inbox.
func (in *Inbox) Await(ctx context.Context, accept func(Signal) bool, ready func() error) (Signal, error) {
	in.mu.Lock()
	in.accept = accept
	select { // drop anything left from an earlier wait
	case <-in.ch:
	default:
	}
	in.mu.Unlock()
	defer func() {
		in.mu.Lock()
		in.accept = nil
		in.mu.Unlock()
	}()

	if ready != nil {
		if err := ready(); err != nil {
			return Signal{}, err
		}
	}
	select {
	case s := <-in.ch:
		return s, nil
	case <-ctx.Done():
		return Signal{}, ctx.Err()
	}
}
