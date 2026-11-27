package httpx

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/xmapst/taskflow/api"
)

const (
	// streamQueueSize bounds the frames waiting for one connection. A client
	// that falls this far behind is disconnected instead of buffered forever.
	streamQueueSize = 256
	streamReadLimit = 1 << 20
	// streamWriteLimit bounds every single frame, so one stalled client cannot
	// hold the writer forever.
	streamWriteLimit = 10 * time.Second
	// streamPingEvery is the idle heartbeat of the multiplexed connection.
	streamPingEvery = 30 * time.Second
)

// StreamSession produces the frames of one subscription. Next blocks until a
// frame is ready and returns io.EOF once the subscription is complete. Close
// may run concurrently with Next and must not block.
type StreamSession interface {
	Next(ctx context.Context) (api.StreamFrame, error)
	Close()
}

// StreamTopic opens a session for the parameters a client subscribed with.
type StreamTopic func(params jsontext.Value) (StreamSession, error)

// StreamCall answers a request that is not a subscription.
type StreamCall func(ctx context.Context, params jsontext.Value) (any, error)

// StreamRegistry is the routing table of the multiplexed WebSocket: topics are
// long-lived subscriptions, methods are one-shot calls.
type StreamRegistry struct {
	topics map[string]StreamTopic
	calls  map[string]StreamCall
}

func NewStreamRegistry() *StreamRegistry {
	return &StreamRegistry{
		topics: make(map[string]StreamTopic),
		calls:  make(map[string]StreamCall),
	}
}

// Topic registers a subscription topic.
func (g *StreamRegistry) Topic(name string, topic StreamTopic) {
	g.topics[name] = topic
}

// Method registers a one-shot method.
func (g *StreamRegistry) Method(name string, call StreamCall) {
	g.calls[name] = call
}

// DecodeStreamParams decodes parameters using the exact JSON field names.
// Empty or null parameters leave value unchanged.
func DecodeStreamParams(params jsontext.Value, value any) error {
	if len(params) == 0 || params.Kind() == jsontext.KindNull {
		return nil
	}
	return json.Unmarshal(params, value, json.RejectUnknownMembers(true))
}

// Serve upgrades the request and multiplexes every subscription and call of the
// connection over that single socket. All writes are owned by one goroutine, so
// frames from different sessions cannot interleave.
func (g *StreamRegistry) Serve(w http.ResponseWriter, r *http.Request) {
	ws, err := Upgrade(w, r)
	if err != nil {
		// Upgrade has already written the handshake failure.
		slog.ErrorContext(r.Context(), "websocket upgrade failed", slog.Any("error", err))
		return
	}
	ws.SetReadLimit(streamReadLimit)

	ctx, cancel := context.WithCancel(r.Context())
	sink := &streamSink{frames: make(chan api.StreamFrame, streamQueueSize), cancel: cancel}
	sink.wg.Add(1)
	go sink.write(ctx, ws)

	conn := &streamConn{
		registry: g,
		ws:       ws,
		sink:     sink,
		ctx:      ctx,
		sessions: make(map[string]*streamSubscription),
	}
	conn.read(ctx)

	// Stop the sessions first so their last frames do not race the writer.
	conn.closeAll()
	cancel()
	sink.wg.Wait()
	CloseWs(ws, "connection closed")
}

// streamSink owns the single writer of a connection.
type streamSink struct {
	frames chan api.StreamFrame
	wg     sync.WaitGroup
	cancel context.CancelFunc
}

func (s *streamSink) push(frame api.StreamFrame) bool {
	select {
	case s.frames <- frame:
		return true
	default:
		// The client stopped consuming its own subscriptions. Drop the socket
		// and let it reconnect rather than grow the queue without bound.
		s.cancel()
		return false
	}
}

func (s *streamSink) reject(id, topic string, err error) {
	s.push(api.StreamFrame{ID: id, Topic: topic, Event: api.StreamError, Code: api.CodeFailed, Message: err.Error()})
}

func (s *streamSink) write(ctx context.Context, ws *websocket.Conn) {
	defer s.wg.Done()
	ping := time.NewTicker(streamPingEvery)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			if PingWs(ctx, ws) != nil {
				s.cancel()
				return
			}
		case frame := <-s.frames:
			data, err := json.Marshal(frame, api.JSONDuration)
			if err != nil {
				slog.ErrorContext(ctx, "encode stream frame", slog.Any("error", err))
				continue
			}
			writeCtx, cancel := context.WithTimeout(ctx, streamWriteLimit)
			err = ws.Write(writeCtx, websocket.MessageText, data)
			cancel()
			if err != nil {
				s.cancel()
				return
			}
		}
	}
}

// streamConn is the state of one multiplexed connection.
type streamConn struct {
	registry *StreamRegistry
	ws       *websocket.Conn
	sink     *streamSink
	ctx      context.Context

	mu       sync.Mutex
	sessions map[string]*streamSubscription
}

type streamSubscription struct {
	id      string
	topic   string
	session StreamSession
	cancel  context.CancelFunc
}

func (c *streamConn) read(ctx context.Context) {
	for {
		kind, data, err := c.ws.Read(ctx)
		if err != nil {
			return
		}
		if kind != websocket.MessageText {
			continue
		}
		var req api.StreamRequest
		if err := json.Unmarshal(data, &req); err != nil {
			c.sink.reject("", "", errors.New("invalid request frame"))
			continue
		}
		c.dispatch(req)
	}
}

func (c *streamConn) dispatch(req api.StreamRequest) {
	switch req.Action {
	case api.ActionSubscribe:
		c.subscribe(req)
	case api.ActionUnsubscribe:
		c.unsubscribe(req.ID)
	case api.ActionCall:
		// Run calls aside so the reader keeps processing control frames and
		// further requests while the method works.
		go c.invoke(req)
	case api.ActionPing:
		// 原样回一个 result 帧：客户端靠它区分「链路还在」和「连接已经半开」。
		c.sink.push(api.StreamFrame{ID: req.ID, Event: api.StreamResult})
	default:
		c.sink.reject(req.ID, "", fmt.Errorf("unsupported action %q", req.Action))
	}
}

func (c *streamConn) subscribe(req api.StreamRequest) {
	if req.ID == "" {
		c.sink.reject("", req.Topic, errors.New("subscribe requires an id"))
		return
	}
	topic, ok := c.registry.topics[req.Topic]
	if !ok {
		c.sink.reject(req.ID, req.Topic, fmt.Errorf("unknown topic %q", req.Topic))
		return
	}
	session, err := topic(req.Params)
	if err != nil {
		c.sink.reject(req.ID, req.Topic, err)
		return
	}
	// Reusing an id replaces the previous subscription.
	c.unsubscribe(req.ID)

	ctx, cancel := context.WithCancel(c.ctx)
	sub := &streamSubscription{id: req.ID, topic: req.Topic, session: session, cancel: cancel}
	c.mu.Lock()
	c.sessions[req.ID] = sub
	c.mu.Unlock()
	go c.pump(ctx, sub)
}

func (c *streamConn) unsubscribe(id string) {
	c.mu.Lock()
	sub, ok := c.sessions[id]
	delete(c.sessions, id)
	c.mu.Unlock()
	if ok {
		sub.cancel()
		sub.session.Close()
	}
}

func (c *streamConn) closeAll() {
	c.mu.Lock()
	sessions := c.sessions
	c.sessions = make(map[string]*streamSubscription)
	c.mu.Unlock()
	for _, sub := range sessions {
		sub.cancel()
		sub.session.Close()
	}
}

// pump 在单独的 goroutine 里跑，拿到的数据来自各业务 handler。
// 任何一个订阅方的 panic 都会带走整个进程，所以这里必须兜住：
// 崩掉一条订阅就把这条订阅以错误告终，别影响其他连接。
func (c *streamConn) pump(ctx context.Context, sub *streamSubscription) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		slog.ErrorContext(ctx, "stream subscription panicked",
			slog.String("topic", sub.topic), slog.String("id", sub.id), slog.Any("panic", recovered))
		c.sink.push(api.StreamFrame{ID: sub.id, Topic: sub.topic, Event: api.StreamError, Code: api.CodeFailed, Message: fmt.Sprint(recovered)})
		c.sink.push(api.StreamFrame{ID: sub.id, Topic: sub.topic, Event: api.StreamDone})
		c.unsubscribe(sub.id)
	}()
	var err error
	for {
		var frame api.StreamFrame
		frame, err = sub.session.Next(ctx)
		if err != nil {
			break
		}
		frame.ID, frame.Topic = sub.id, sub.topic
		if frame.Event == "" {
			frame.Event = api.StreamUpdate
		}
		if !c.sink.push(frame) {
			return
		}
	}
	if ctx.Err() != nil {
		return
	}
	// Sessions end either normally or with a reason; report the reason before
	// the terminal frame so the client can show it.
	if !errors.Is(err, io.EOF) {
		c.sink.push(api.StreamFrame{ID: sub.id, Topic: sub.topic, Event: api.StreamError, Code: api.CodeFailed, Message: err.Error()})
	}
	c.sink.push(api.StreamFrame{ID: sub.id, Topic: sub.topic, Event: api.StreamDone})
	c.unsubscribe(sub.id)
}

func (c *streamConn) invoke(req api.StreamRequest) {
	call, ok := c.registry.calls[req.Method]
	if !ok {
		c.sink.reject(req.ID, "", fmt.Errorf("unknown method %q", req.Method))
		return
	}
	data, err := call(c.ctx, req.Params)
	if err != nil {
		c.sink.reject(req.ID, "", err)
		return
	}
	c.sink.push(api.StreamFrame{ID: req.ID, Event: api.StreamResult, Data: data})
}
