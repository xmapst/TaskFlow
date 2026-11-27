package event

import (
	"context"
	"encoding/json/jsontext"
	"io"
	
	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/api/httpx"
	workerevent "github.com/xmapst/taskflow/internal/worker/event"
)

// Handler implements the worker event feed that the shared WebSocket exposes.
type Handler struct{}

// RegisterStream exposes the worker events as the "event" topic. Every frame
// carries one sequence number and the raw event line it belongs to; subscribing
// with {"since": n} first replays what was emitted after n, which is how a
// reconnecting client catches up on the events of its outage.
func (h *Handler) RegisterStream(reg *httpx.StreamRegistry) {
	reg.Topic(api.TopicEvent, func(params jsontext.Value) (httpx.StreamSession, error) {
		var request struct {
			Since uint64 `json:"since"`
		}
		if err := httpx.DecodeStreamParams(params, &request); err != nil {
			return nil, err
		}
		replay, err := workerevent.SubscribeSince(request.Since)
		if err != nil {
			return nil, err
		}
		return &session{backlog: replay.Backlog, stream: replay.Stream, id: replay.ID}, nil
	})
}

// Event is one entry of the feed.
type Event struct {
	// Seq is the position in the stream; a jump means events were dropped or
	// aged out of the replay window.
	Seq uint64 `json:"seq"`
	// Text is the event line, e.g. "<task> <step> Execute".
	Text string `json:"text"`
}

type session struct {
	// backlog holds the retained events that predate the subscription; it is
	// drained into frames before the live stream is read.
	backlog []workerevent.Sequenced[string]
	stream  workerevent.Stream[workerevent.Sequenced[string]]
	id      int64
}

func (s *session) Next(ctx context.Context) (api.StreamFrame, error) {
	if len(s.backlog) > 0 {
		event := s.backlog[0]
		s.backlog = s.backlog[1:]
		return frame(event), nil
	}
	select {
	case <-ctx.Done():
		return api.StreamFrame{}, io.EOF
	case event, ok := <-s.stream:
		if !ok {
			return api.StreamFrame{}, io.EOF
		}
		return frame(event), nil
	}
}

func (s *session) Close() {
	workerevent.Unsubscribe(s.id)
}

func frame(event workerevent.Sequenced[string]) api.StreamFrame {
	return api.StreamFrame{Data: Event{Seq: event.Seq, Text: event.Value}}
}
