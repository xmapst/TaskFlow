package event

import (
	"errors"
	"sync"
	"sync/atomic"
)

// Package event provides a simple generic event emitter that reads events from
// a source channel and fans them out to multiple subscribers. It is safe for
// concurrent use: subscribers may subscribe and unsubscribe concurrently while
// events are being processed.
//
// Every event is stamped with a monotonically increasing sequence number and
// remembered in a bounded history, so a client that lost its connection can ask
// for everything emitted after the last number it saw instead of silently
// missing the events of the outage.
//
// Naming choices follow Go conventions:
// - Exported types and methods are capitalized (Emitter, Subscribe, Unsubscribe).
// - Implementation types are unexported (emitter, subscriber).
// - Interface name uses -er suffix (Emitter) rather than an I-prefix.
// - Avoid stuttering: package event + type Emitter -> event.Emitter.

// EventStream is a receive-only channel representing an event stream of T.
type Stream[T any] <-chan T

// Sequenced pairs an event with its position in the stream.
type Sequenced[T any] struct {
	// Seq starts at 1 and grows by one per emitted event, across all
	// subscribers: it identifies the event, not the delivery.
	Seq uint64
	// Value is the event itself.
	Value T
}

// Replay is what a subscriber receives: the events emitted after the requested
// cursor that are still retained, followed by the live stream. Both are handed
// out atomically, so no event can fall between the two.
type Replay[T any] struct {
	Backlog []Sequenced[T]
	Stream  Stream[Sequenced[T]]
	ID      int64
}

// Emitter is the public interface for the generic event emitter. It allows
// callers to subscribe to events and to unsubscribe by id.
type Emitter[T any] interface {
	// SubscribeSince registers a new subscriber. A cursor of 0 means "from now
	// on" and yields no backlog; a larger value replays the retained events
	// with a greater sequence number.
	SubscribeSince(since uint64) (Replay[T], error)

	// Unsubscribe removes the subscriber with the given id and closes the
	// subscriber's channel so the receiver can detect completion.
	Unsubscribe(id int64)
}

const (
	// defaultBufferSize is the default buffer size for subscriber channels.
	// Extracted as a constant to avoid magic numbers and to make it easier to
	// expose configurability in the future.
	defaultBufferSize = 200

	// Preserve the per-connection burst capacity of the event topic.
	subscriberBufferSize = 65534

	// historySize bounds the catch-up window. 4096 short event lines cost a
	// few hundred kilobytes and cover a reconnect that took minutes on a busy
	// worker.
	historySize = 4096
)

// emitter is the concrete (unexported) implementation of Emitter[T].
// It listens for events on an input stream and forwards them to all
// registered subscribers.
type emitter[T any] struct {
	stream      Stream[T]
	subscribers sync.Map // map[int64]*subscriber[T]
	done        bool
	mux         sync.Mutex
	nextID      atomic.Int64
	nextSeq     uint64
	history     *ring[T]
}

// New constructs a new generic emitter that reads events from the provided
// EventStream. It starts a background goroutine to forward events to
// subscribers and returns the public Emitter[T] interface.
func New[T any](s Stream[T]) Emitter[T] {
	e := &emitter[T]{stream: s, history: newRing[T](historySize)}
	go e.process()
	return e
}

// process continuously receives events from the source stream and forwards
// each event to all current subscribers. When the source stream is closed,
// process calls shutdown to close all subscriber channels.
func (e *emitter[T]) process() {
	for value := range e.stream {
		e.publish(value)
	}
	e.shutdown()
}

// publish stamps the event, records it in the history and hands it to every
// subscriber. All three steps happen under the emitter lock, which is what
// makes "backlog plus live stream" atomic for a concurrent SubscribeSince.
func (e *emitter[T]) publish(value T) {
	e.mux.Lock()
	defer e.mux.Unlock()

	if e.done {
		return
	}
	e.nextSeq++
	event := Sequenced[T]{Seq: e.nextSeq, Value: value}
	e.history.add(event)
	e.subscribers.Range(func(_, value any) bool {
		value.(*subscriber[Sequenced[T]]).send(event)
		return true
	})
}

// shutdown closes all subscribers and marks the emitter as done. It is safe
// to call multiple times; subsequent calls are no-ops.
func (e *emitter[T]) shutdown() {
	e.mux.Lock()
	defer e.mux.Unlock()

	if e.done {
		return
	}
	e.done = true

	e.subscribers.Range(func(key, value any) bool {
		sub := value.(*subscriber[Sequenced[T]])
		e.subscribers.Delete(key)
		sub.close()
		return true
	})
}

// SubscribeSince registers a new subscriber and returns its stream together
// with the retained events that follow the cursor. If the emitter has already
// been closed, SubscribeSince returns an error.
func (e *emitter[T]) SubscribeSince(since uint64) (Replay[T], error) {
	e.mux.Lock()
	defer e.mux.Unlock()

	if e.done {
		return Replay[T]{}, errors.New("emitter has been closed, cannot subscribe")
	}
	id := e.nextID.Add(1)
	sub := newSubscriber[Sequenced[T]](subscriberBufferSize)
	e.subscribers.Store(id, sub)
	replay := Replay[T]{Stream: sub.channel(), ID: id}
	// Sequence numbers start at 1, so 0 is "give me only what happens next".
	if since > 0 {
		replay.Backlog = e.history.after(since)
	}
	return replay, nil
}

// Unsubscribe removes the subscriber with the specified id if it exists and
// closes its channel so the subscriber goroutine can exit. If the id does
// not exist this is a no-op.
func (e *emitter[T]) Unsubscribe(id int64) {
	value, exist := e.subscribers.Load(id)
	if !exist {
		return
	}
	sub := value.(*subscriber[Sequenced[T]])
	e.subscribers.Delete(id)
	sub.close()
}

// ring is a fixed-size circular buffer of the most recent events.
type ring[T any] struct {
	items []Sequenced[T]
	// head indexes the oldest entry once the buffer is full.
	head int
	size int
}

func newRing[T any](capacity int) *ring[T] {
	return &ring[T]{items: make([]Sequenced[T], capacity)}
}

func (r *ring[T]) add(event Sequenced[T]) {
	if r.size < len(r.items) {
		r.items[(r.head+r.size)%len(r.items)] = event
		r.size++
		return
	}
	r.items[r.head] = event
	r.head = (r.head + 1) % len(r.items)
}

// after returns the retained events with a sequence number greater than seq,
// oldest first. Events older than the retained window are gone; their absence
// shows up as a jump in sequence numbers.
func (r *ring[T]) after(seq uint64) []Sequenced[T] {
	backlog := make([]Sequenced[T], 0, r.size)
	for i := range r.size {
		if event := r.items[(r.head+i)%len(r.items)]; event.Seq > seq {
			backlog = append(backlog, event)
		}
	}
	return backlog
}

// subscriber represents an individual subscriber. Each subscriber owns a
// buffered channel used to deliver events. The close operation is guarded by
// a mutex so sending and closing can safely run concurrently.
type subscriber[T any] struct {
	ch     chan T
	mu     sync.RWMutex
	closed bool
}

// send attempts to deliver the event to the subscriber's buffer in a
// non-blocking manner. If the buffer is full the event is dropped. This
// avoids a slow subscriber from blocking the emitter.
func (s *subscriber[T]) send(event T) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- event:
	default:
		// drop when buffer is full
	}
}

// channel returns the subscriber's receive-only channel.
func (s *subscriber[T]) channel() Stream[T] {
	return s.ch
}

// close closes the subscriber's channel (only once).
func (s *subscriber[T]) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
}

// newSubscriber creates a subscriber with the given buffer size.
func newSubscriber[T any](bufferSize int) *subscriber[T] {
	return &subscriber[T]{
		ch: make(chan T, bufferSize),
	}
}
