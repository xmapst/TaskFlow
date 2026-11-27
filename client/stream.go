package client

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"iter"
	"math/rand/v2"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/api/iproto"
)

const (
	streamReadLimit  = 16 << 20
	subscriptionBuf  = 256
	defaultPingEvery = 30 * time.Second
)

// ErrStreamClosed is reported by calls and subscriptions once the Stream is
// closed, or when it lost the connection and reconnecting is disabled.
var ErrStreamClosed = errors.New("client: stream closed")

// Frame is one server frame with the payload left undecoded.
type Frame struct {
	ID      string         `json:"id"`
	Topic   string         `json:"topic"`
	Event   string         `json:"event"`
	Code    api.Code       `json:"code"`
	Message string         `json:"message"`
	Data    jsontext.Value `json:"data"`
}

// Update is one decoded subscription frame. Code carries the run state of what
// is watched (running, pending, paused, failed, ...).
type Update[T any] struct {
	Code    api.Code
	Message string
	Data    T
}

// StreamOption configures DialStream.
type StreamOption func(*Stream)

// WithReconnect sets the backoff between reconnect attempts. A max of zero
// disables reconnecting; the default is 500ms growing to 30s.
func WithReconnect(minDelay, maxDelay time.Duration) StreamOption {
	return func(s *Stream) { s.minDelay, s.maxDelay = minDelay, maxDelay }
}

// WithPingInterval sets how often the client probes the connection. Zero
// disables the probe; the default is 30 seconds.
func WithPingInterval(d time.Duration) StreamOption {
	return func(s *Stream) { s.pingEvery = d }
}

// WithConnectionHooks installs callbacks for connection state changes. Both
// run on the connection goroutine and must not block.
func WithConnectionHooks(onConnect func(reconnect bool), onDisconnect func(error)) StreamOption {
	return func(s *Stream) { s.onConnect, s.onDisconnect = onConnect, onDisconnect }
}

// Stream is the multiplexed WebSocket (/api/v1/ws): live subscriptions and
// one-shot calls over one connection that reconnects by itself and
// resubscribes. It is safe for concurrent use.
type Stream struct {
	c *Client

	minDelay, maxDelay time.Duration
	pingEvery          time.Duration
	onConnect          func(bool)
	onDisconnect       func(error)

	ctx    context.Context
	cancel context.CancelFunc
	finish chan struct{}

	mu    sync.Mutex
	conn  *websocket.Conn
	ready chan struct{} // closed while a connection is up; replaced on loss
	subs  map[string]*Subscription
	calls map[string]chan Frame
	err   error // why the stream ended

	nextID atomic.Uint64
}

// DialStream connects the multiplexed stream. The first connection attempt is
// synchronous, so a wrong address or credentials fail here.
func (c *Client) DialStream(ctx context.Context, opts ...StreamOption) (*Stream, error) {
	s := &Stream{
		c:         c,
		minDelay:  500 * time.Millisecond,
		maxDelay:  30 * time.Second,
		pingEvery: defaultPingEvery,
		finish:    make(chan struct{}),
		ready:     make(chan struct{}),
		subs:      make(map[string]*Subscription),
		calls:     make(map[string]chan Frame),
	}
	for _, o := range opts {
		o(s)
	}
	conn, err := c.dialStream(ctx)
	if err != nil {
		return nil, err
	}
	// The stream outlives the dial context.
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.attach(conn, false)
	go s.run(conn)
	return s, nil
}

func (c *Client) dialStream(ctx context.Context) (*websocket.Conn, error) {
	ws, err := c.dialWS(ctx, "/api/v1/ws", nil)
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(streamReadLimit)
	return ws, nil
}

func (s *Stream) attach(conn *websocket.Conn, reconnect bool) {
	s.mu.Lock()
	s.conn = conn
	close(s.ready)
	subs := make([]*Subscription, 0, len(s.subs))
	for _, sub := range s.subs {
		subs = append(subs, sub)
	}
	s.mu.Unlock()
	if s.onConnect != nil {
		s.onConnect(reconnect)
	}
	for _, sub := range subs {
		_ = s.send(s.ctx, sub.request())
	}
}

// run owns the connection: read until it breaks, then redial.
func (s *Stream) run(conn *websocket.Conn) {
	defer close(s.finish)
	for {
		stopPing := s.startPing(conn)
		err := s.readLoop(conn)
		stopPing()
		_ = conn.CloseNow()

		s.mu.Lock()
		s.conn = nil
		s.ready = make(chan struct{})
		for id, ch := range s.calls {
			close(ch)
			delete(s.calls, id)
		}
		s.mu.Unlock()
		if s.onDisconnect != nil {
			s.onDisconnect(err)
		}
		if s.ctx.Err() != nil || s.maxDelay <= 0 {
			s.shutdown(err)
			return
		}
		next, ok := s.redial()
		if !ok {
			s.shutdown(s.ctx.Err())
			return
		}
		conn = next
		s.attach(conn, true)
	}
}

func (s *Stream) redial() (*websocket.Conn, bool) {
	delay := s.minDelay
	for {
		wait := delay + rand.N(delay/2+1)
		select {
		case <-time.After(wait):
		case <-s.ctx.Done():
			return nil, false
		}
		dialCtx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
		conn, err := s.c.dialStream(dialCtx)
		cancel()
		if err == nil {
			return conn, true
		}
		var ae *APIError
		if errors.As(err, &ae) && (ae.Status == 401 || ae.Status == 403) {
			s.shutdown(err) // credentials will not fix themselves
			return nil, false
		}
		delay = min(delay*2, s.maxDelay)
	}
}

func (s *Stream) startPing(conn *websocket.Conn) (stop func()) {
	if s.pingEvery <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(s.pingEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				ctx, cancel := context.WithTimeout(s.ctx, s.pingEvery)
				err := conn.Ping(ctx)
				cancel()
				if err != nil {
					_ = conn.CloseNow()
					return
				}
			}
		}
	}()
	return func() { close(done) }
}

func (s *Stream) readLoop(conn *websocket.Conn) error {
	for {
		kind, data, err := conn.Read(s.ctx)
		if err != nil {
			return err
		}
		if kind != websocket.MessageText {
			continue
		}
		var f Frame
		if err := json.Unmarshal(data, &f); err != nil {
			continue
		}
		s.dispatch(f)
	}
}

func (s *Stream) dispatch(f Frame) {
	switch f.Event {
	case api.StreamResult, api.StreamError:
		s.mu.Lock()
		if ch, ok := s.calls[f.ID]; ok {
			delete(s.calls, f.ID)
			s.mu.Unlock()
			ch <- f // buffered 1
			close(ch)
			return
		}
		sub := s.subs[f.ID]
		s.mu.Unlock()
		if sub != nil && f.Event == api.StreamError {
			sub.end(&APIError{Code: f.Code, Message: f.Message})
		}
	case api.StreamUpdate:
		s.mu.Lock()
		sub := s.subs[f.ID]
		s.mu.Unlock()
		if sub != nil {
			sub.deliver(f)
		}
	case api.StreamDone:
		s.mu.Lock()
		sub := s.subs[f.ID]
		s.mu.Unlock()
		if sub != nil {
			sub.end(nil)
		}
	}
}

// shutdown ends the stream and everything attached to it.
func (s *Stream) shutdown(err error) {
	s.mu.Lock()
	if s.err == nil {
		if err == nil || errors.Is(err, context.Canceled) {
			err = ErrStreamClosed
		}
		s.err = err
	}
	subs := s.subs
	s.subs = make(map[string]*Subscription)
	s.mu.Unlock()
	s.cancel()
	for _, sub := range subs {
		sub.finish(s.err)
	}
}

// Close ends the stream, all subscriptions and pending calls.
func (s *Stream) Close() error {
	s.shutdown(ErrStreamClosed)
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}
	<-s.finish
	return nil
}

// Done is closed when the stream has ended for good.
func (s *Stream) Done() <-chan struct{} { return s.finish }

// Err is why the stream ended, or nil while it runs.
func (s *Stream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Stream) newID() string { return strconv.FormatUint(s.nextID.Add(1), 36) }

func (s *Stream) waitReady(ctx context.Context) (*websocket.Conn, error) {
	for {
		s.mu.Lock()
		conn, ready, err := s.conn, s.ready, s.err
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if conn != nil {
			return conn, nil
		}
		select {
		case <-ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.finish:
			return nil, s.Err()
		}
	}
}

func (s *Stream) send(ctx context.Context, req api.StreamRequest) error {
	conn, err := s.waitReady(ctx)
	if err != nil {
		return err
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, data)
}

func (s *Stream) rawParams(params any) (jsontext.Value, error) {
	if params == nil {
		return jsontext.Value("{}"), nil
	}
	b, err := s.c.marshal(params)
	if err != nil {
		return nil, err
	}
	return jsontext.Value(b), nil
}

// Call invokes a server method and decodes its result into out (nil to
// discard). A failing call returns *APIError.
func (s *Stream) Call(ctx context.Context, method string, params, out any) error {
	raw, err := s.rawParams(params)
	if err != nil {
		return err
	}
	id := s.newID()
	ch := make(chan Frame, 1)
	s.mu.Lock()
	if s.err != nil {
		err := s.err
		s.mu.Unlock()
		return err
	}
	s.calls[id] = ch
	s.mu.Unlock()
	forget := func() {
		s.mu.Lock()
		delete(s.calls, id)
		s.mu.Unlock()
	}
	req := api.StreamRequest{ID: id, Action: api.ActionCall, Method: method, Params: raw}
	if err := s.send(ctx, req); err != nil {
		forget()
		return err
	}
	select {
	case f, ok := <-ch:
		if !ok {
			return fmt.Errorf("client: connection lost during %s: %w", method, ErrStreamClosed)
		}
		if f.Event == api.StreamError {
			return &APIError{Code: f.Code, Message: f.Message}
		}
		if out == nil || len(f.Data) == 0 {
			return nil
		}
		return json.Unmarshal(f.Data, out, api.JSONDuration)
	case <-ctx.Done():
		forget()
		return ctx.Err()
	}
}

// Ping measures the round trip over the application protocol.
func (s *Stream) Ping(ctx context.Context) (time.Duration, error) {
	id := s.newID()
	ch := make(chan Frame, 1)
	s.mu.Lock()
	s.calls[id] = ch
	s.mu.Unlock()
	start := time.Now()
	if err := s.send(ctx, api.StreamRequest{ID: id, Action: api.ActionPing, Params: jsontext.Value("{}")}); err != nil {
		s.mu.Lock()
		delete(s.calls, id)
		s.mu.Unlock()
		return 0, err
	}
	select {
	case _, ok := <-ch:
		if !ok {
			return 0, ErrStreamClosed
		}
		return time.Since(start), nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// Subscription is a live topic subscription delivering raw frames. Use the
// typed Watch* methods unless you need the raw payload.
type Subscription struct {
	s      *Stream
	id     string
	topic  string
	frames chan Frame
	closed chan struct{}

	mu     sync.Mutex
	params jsontext.Value
	err    error
	once   sync.Once
	track  func(*Subscription, Frame)
}

func (sub *Subscription) request() api.StreamRequest {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	return api.StreamRequest{ID: sub.id, Action: api.ActionSubscribe, Topic: sub.topic, Params: sub.params}
}

func (sub *Subscription) deliver(f Frame) {
	if sub.track != nil {
		sub.track(sub, f)
	}
	select {
	case sub.frames <- f:
	case <-sub.closed:
	case <-sub.s.finish:
	}
}

// end finishes the subscription and unregisters it.
func (sub *Subscription) end(err error) {
	sub.s.mu.Lock()
	if sub.s.subs[sub.id] == sub {
		delete(sub.s.subs, sub.id)
	}
	sub.s.mu.Unlock()
	sub.finish(err)
}

func (sub *Subscription) finish(err error) {
	sub.once.Do(func() {
		sub.mu.Lock()
		sub.err = err
		sub.mu.Unlock()
		close(sub.closed)
	})
}

// Frames returns the delivery channel, closed when the subscription ended.
// Drain it promptly: a consumer that stalls blocks the whole connection.
func (sub *Subscription) Frames() <-chan Frame { return sub.frames }

// Err is why the subscription ended; nil for a normal end or while it runs.
func (sub *Subscription) Err() error {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	return sub.err
}

// Close unsubscribes.
func (sub *Subscription) Close(ctx context.Context) error {
	sub.end(nil)
	err := sub.s.send(ctx, api.StreamRequest{ID: sub.id, Action: api.ActionUnsubscribe, Params: jsontext.Value("{}")})
	if errors.Is(err, ErrStreamClosed) {
		return nil
	}
	return err
}

// next waits for a frame; ok is false when the subscription is over.
func (sub *Subscription) next(ctx context.Context) (Frame, bool, error) {
	select {
	case f := <-sub.frames:
		return f, true, nil
	case <-sub.closed:
		// Deliver what was queued before the end.
		select {
		case f := <-sub.frames:
			return f, true, nil
		default:
			return Frame{}, false, sub.Err()
		}
	case <-ctx.Done():
		return Frame{}, false, ctx.Err()
	}
}

// Subscribe opens a subscription to a topic with the given params.
func (s *Stream) Subscribe(ctx context.Context, topic string, params any) (*Subscription, error) {
	return s.subscribe(ctx, topic, params, nil)
}

func (s *Stream) subscribe(ctx context.Context, topic string, params any, track func(*Subscription, Frame)) (*Subscription, error) {
	raw, err := s.rawParams(params)
	if err != nil {
		return nil, err
	}
	sub := &Subscription{
		s: s, id: s.newID(), topic: topic, params: raw, track: track,
		frames: make(chan Frame, subscriptionBuf), closed: make(chan struct{}),
	}
	s.mu.Lock()
	if s.err != nil {
		err := s.err
		s.mu.Unlock()
		return nil, err
	}
	s.subs[sub.id] = sub
	s.mu.Unlock()
	if err := s.send(ctx, sub.request()); err != nil {
		sub.end(err)
		return nil, err
	}
	return sub, nil
}

// Feed is a typed subscription.
type Feed[T any] struct{ sub *Subscription }

// Subscription exposes the raw subscription.
func (f *Feed[T]) Subscription() *Subscription { return f.sub }

// Close unsubscribes.
func (f *Feed[T]) Close(ctx context.Context) error { return f.sub.Close(ctx) }

// Err is why the feed ended; nil for a normal end.
func (f *Feed[T]) Err() error { return f.sub.Err() }

// Next returns the next update. ok is false once the feed is over, with err
// set if it ended abnormally.
func (f *Feed[T]) Next(ctx context.Context) (u Update[T], ok bool, err error) {
	fr, ok, err := f.sub.next(ctx)
	if !ok {
		return u, false, err
	}
	u.Code, u.Message = fr.Code, fr.Message
	if len(fr.Data) > 0 {
		if err := json.Unmarshal(fr.Data, &u.Data, api.JSONDuration); err != nil {
			return u, true, fmt.Errorf("client: decode %s frame: %w", fr.Topic, err)
		}
	}
	return u, true, nil
}

// All iterates over the updates until the feed ends or ctx is cancelled. An
// abnormal end is yielded once as a final error.
func (f *Feed[T]) All(ctx context.Context) iter.Seq2[Update[T], error] {
	return func(yield func(Update[T], error) bool) {
		for {
			u, ok, err := f.Next(ctx)
			if !ok {
				if err != nil {
					yield(u, err)
				}
				return
			}
			if !yield(u, err) {
				return
			}
		}
	}
}

func (s *Stream) watch[T any](ctx context.Context, topic string, params any, track func(*Subscription, Frame)) (*Feed[T], error) {
	sub, err := s.subscribe(ctx, topic, params, track)
	if err != nil {
		return nil, err
	}
	return &Feed[T]{sub: sub}, nil
}

// Event is one worker event.
type Event struct {
	// Seq is the position in the event stream; a jump means events were dropped.
	Seq uint64 `json:"seq"`
	// Text is the event line, e.g. "<task> <step> Execute".
	Text string `json:"text"`
}

// SessionList is the payload of the terminal-sessions topic.
type SessionList struct {
	Sessions []iproto.WebShellSession `json:"sessions"`
}

// WatchEvents follows worker events. since replays what was emitted after that
// sequence number; after a reconnect the feed resumes from the last event it
// delivered.
func (s *Stream) WatchEvents(ctx context.Context, since uint64) (*Feed[Event], error) {
	track := func(sub *Subscription, f Frame) {
		var e Event
		if json.Unmarshal(f.Data, &e) != nil {
			return
		}
		raw, _ := s.rawParams(map[string]uint64{"since": e.Seq})
		sub.mu.Lock()
		sub.params = raw
		sub.mu.Unlock()
	}
	return s.watch[Event](ctx, api.TopicEvent, map[string]uint64{"since": since}, track)
}

// WatchSystem follows worker pool size and host metrics.
func (s *Stream) WatchSystem(ctx context.Context) (*Feed[iproto.SSystemConfig], error) {
	return s.watch[iproto.SSystemConfig](ctx, api.TopicSystem, nil, nil)
}

// WatchTasks follows one page of the task list.
func (s *Stream) WatchTasks(ctx context.Context, page iproto.SPageReq) (*Feed[*iproto.STaskListDetailRes], error) {
	return s.watch[*iproto.STaskListDetailRes](ctx, api.TopicTasks, page, nil)
}

type target struct {
	Task string `json:"task"`
	Step string `json:"step,omitempty"`
}

// WatchTask follows one task until it finishes.
func (s *Stream) WatchTask(ctx context.Context, task string) (*Feed[*iproto.STaskRes], error) {
	return s.watch[*iproto.STaskRes](ctx, api.TopicTask, target{Task: task}, nil)
}

// WatchSteps follows the step list of a task until it finishes.
func (s *Stream) WatchSteps(ctx context.Context, task string) (*Feed[iproto.SStepsRes], error) {
	return s.watch[iproto.SStepsRes](ctx, api.TopicSteps, target{Task: task}, nil)
}

// WatchStep follows one step until it finishes.
func (s *Stream) WatchStep(ctx context.Context, task, step string) (*Feed[*iproto.SStepRes], error) {
	return s.watch[*iproto.SStepRes](ctx, api.TopicStep, target{Task: task, Step: step}, nil)
}

// WatchStepLog replays and follows the output of a step until it finishes.
// After a reconnect the log is replayed from its start.
func (s *Stream) WatchStepLog(ctx context.Context, task, step string) (*Feed[iproto.SStepLogsRes], error) {
	return s.watch[iproto.SStepLogsRes](ctx, api.TopicStepLog, target{Task: task, Step: step}, nil)
}

type sessionID struct {
	ID string `json:"id"`
}

// WatchTerminalSessions follows the list of live terminal sessions; every
// update is the complete list.
func (s *Stream) WatchTerminalSessions(ctx context.Context) (*Feed[SessionList], error) {
	return s.watch[SessionList](ctx, api.TopicTerminalSessions, nil, nil)
}

// WatchTerminalSession follows the output of a live session read-only. Each
// update is one asciinema cast line.
func (s *Stream) WatchTerminalSession(ctx context.Context, id string) (*Feed[jsontext.Value], error) {
	return s.watch[jsontext.Value](ctx, api.TopicTerminalWatch, sessionID{id}, nil)
}

// WatchTerminalReplay streams a recording as chunks of NDJSON text.
func (s *Stream) WatchTerminalReplay(ctx context.Context, id string) (*Feed[string], error) {
	return s.watch[string](ctx, api.TopicTerminalReplay, sessionID{id}, nil)
}

// CreateTask submits a task definition.
func (s *Stream) CreateTask(ctx context.Context, task *iproto.STaskReq) (*iproto.STaskCreateRes, error) {
	body, err := s.c.marshal(task)
	if err != nil {
		return nil, err
	}
	return s.createTask(ctx, "json", string(body))
}

// CreateTaskYAML submits a task definition written as YAML.
func (s *Stream) CreateTaskYAML(ctx context.Context, definition string) (*iproto.STaskCreateRes, error) {
	return s.createTask(ctx, "yaml", definition)
}

func (s *Stream) createTask(ctx context.Context, format, content string) (*iproto.STaskCreateRes, error) {
	out := new(iproto.STaskCreateRes)
	err := s.Call(ctx, api.MethodTaskCreate, map[string]string{"format": format, "content": content}, out)
	return out, err
}

// ManageTask kills, pauses or resumes a task; see Client.ManageTask.
func (s *Stream) ManageTask(ctx context.Context, task string, action Action, pauseFor time.Duration) error {
	return s.Call(ctx, api.MethodTaskManage, map[string]string{
		"task": task, "action": string(action), "duration": pauseValue(pauseFor),
	}, nil)
}

// ManageStep kills, pauses or resumes a step.
func (s *Stream) ManageStep(ctx context.Context, task, step string, action Action, pauseFor time.Duration) error {
	return s.Call(ctx, api.MethodStepManage, map[string]string{
		"task": task, "step": step, "action": string(action), "duration": pauseValue(pauseFor),
	}, nil)
}

// DeleteTask removes a task.
func (s *Stream) DeleteTask(ctx context.Context, task string) error {
	return s.Call(ctx, api.MethodTaskDelete, target{Task: task}, nil)
}

// DumpTask exports a task definition as YAML.
func (s *Stream) DumpTask(ctx context.Context, task string) (string, error) {
	var out string
	err := s.Call(ctx, api.MethodTaskDump, target{Task: task}, &out)
	return out, err
}

// ResizePool changes the worker pool size. Shrinking may wait for workers to
// finish their current task, so allow the context enough time.
func (s *Stream) ResizePool(ctx context.Context, size int) error {
	return s.Call(ctx, api.MethodSystemUpdate, map[string]int{"size": size}, nil)
}

// DisconnectSession closes a live terminal session.
func (s *Stream) DisconnectSession(ctx context.Context, id string) error {
	return s.Call(ctx, api.MethodTerminalDisconnect, sessionID{id}, nil)
}

// ListReplays lists recordings; limit is 1..100 (0 = 50).
func (s *Stream) ListReplays(ctx context.Context, limit, offset int) (*iproto.ReplayList, error) {
	out := new(iproto.ReplayList)
	err := s.Call(ctx, api.MethodReplayList, map[string]int{"limit": limit, "offset": offset}, out)
	return out, err
}
