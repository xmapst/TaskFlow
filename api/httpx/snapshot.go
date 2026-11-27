package httpx

import (
	"context"
	"errors"
	"io"
	"reflect"
	"time"

	"github.com/xmapst/taskflow/api"
)

// Snapshot is one reading of a query: the value plus the code and error that
// describe it.
type Snapshot[T any] struct {
	Code api.Code
	Data T
	Err  error
}

// SnapshotFunc reads the current state. It must return fresh data, since the
// previous reading is retained to detect changes.
type SnapshotFunc[T any] func(ctx context.Context) (api.Code, T, error)

// SnapshotSession polls a query and emits a frame only when the reading
// changes. Frame decides whether a change is worth sending, Done decides
// whether the subscription is complete after that reading.
type SnapshotSession[T any] struct {
	Query    SnapshotFunc[T]
	Interval time.Duration
	Frame    func(Snapshot[T]) (api.StreamFrame, bool)
	Done     func(Snapshot[T]) bool

	last    Snapshot[T]
	started bool
	stopped bool
}

func (s *SnapshotSession[T]) Next(ctx context.Context) (api.StreamFrame, error) {
	for {
		if s.stopped || ctx.Err() != nil {
			return api.StreamFrame{}, io.EOF
		}
		current, changed := s.refresh(ctx)
		if changed {
			frame, send := s.Frame(current)
			s.stopped = s.Done != nil && s.Done(current)
			if send {
				return frame, nil
			}
		}
		if !WaitPoll(ctx, s.Interval) {
			return api.StreamFrame{}, io.EOF
		}
	}
}

// Close is a no-op: a polling session holds nothing that outlives the
// subscription context the transport cancels for it.
func (s *SnapshotSession[T]) Close() {}

func (s *SnapshotSession[T]) refresh(ctx context.Context) (Snapshot[T], bool) {
	var current Snapshot[T]
	current.Code, current.Data, current.Err = s.Query(ctx)
	changed := !s.started ||
		current.Code != s.last.Code ||
		!errors.Is(current.Err, s.last.Err) ||
		!reflect.DeepEqual(current.Data, s.last.Data)
	if changed {
		s.started = true
		s.last = current
	}
	return current, changed
}

// WaitPoll sleeps for the poll interval and reports whether the caller should
// continue.
func WaitPoll(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}
