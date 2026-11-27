package api

import "encoding/json/jsontext"

// Server-to-client frame events.
const (
	// StreamUpdate carries a fresh value for a subscription.
	StreamUpdate = "update"
	// StreamDone ends a subscription that will not produce anything else.
	StreamDone = "done"
	// StreamError reports a failed request or a subscription that broke.
	StreamError = "error"
	// StreamResult carries the reply to a call.
	StreamResult = "result"
)

// Client-to-server actions of a StreamRequest.
const (
	// ActionSubscribe opens a long-lived subscription to a topic.
	ActionSubscribe = "subscribe"
	// ActionUnsubscribe closes the subscription opened by the request with the same ID.
	ActionUnsubscribe = "unsubscribe"
	// ActionCall invokes a one-shot method and is answered by a result or error frame.
	ActionCall = "call"
	// ActionPing is answered by an empty result frame; it checks the link is alive.
	ActionPing = "ping"
)

// StreamFrame is one server-to-client frame. Sessions fill Code, Message and
// Data; the transport adds ID, Topic and Event.
type StreamFrame struct {
	ID      string `json:"id,omitempty"`
	Topic   string `json:"topic,omitempty"`
	Event   string `json:"event"`
	Code    Code   `json:"code,omitzero"`
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty"`
}

// StreamRequest is one client-to-server frame. Params is passed verbatim to the
// topic or method that the request addresses.
type StreamRequest struct {
	ID     string         `json:"id"`
	Action string         `json:"action"`
	Topic  string         `json:"topic"`
	Method string         `json:"method"`
	Params jsontext.Value `json:"params"`
}

// Topics a client can subscribe to over the multiplexed stream. The values are
// the wire names; the web UI and any other client send them verbatim.
const (
	// TopicEvent streams worker events.
	TopicEvent = "event"
	// TopicSystem streams resource metrics.
	TopicSystem = "system"
	// TopicTasks streams the paged task list.
	TopicTasks = "tasks"
	// TopicTask streams one task's detail.
	TopicTask = "task"
	// TopicSteps streams the step list of a task.
	TopicSteps = "steps"
	// TopicStep streams one step's detail.
	TopicStep = "step"
	// TopicStepLog streams one step's log output.
	TopicStepLog = "step-log"
	// TopicTerminalSessions streams the live terminal sessions.
	TopicTerminalSessions = "terminal-sessions"
	// TopicTerminalWatch streams the output of one live terminal session.
	TopicTerminalWatch = "terminal-watch"
	// TopicTerminalReplay streams a recorded terminal session.
	TopicTerminalReplay = "terminal-replay"
)

// One-shot calls a client can make over the multiplexed stream.
const (
	// MethodTaskCreate creates a task.
	MethodTaskCreate = "task.create"
	// MethodTaskManage pauses, resumes or kills a task.
	MethodTaskManage = "task.manage"
	// MethodTaskDelete deletes a task.
	MethodTaskDelete = "task.delete"
	// MethodTaskDump exports a task definition.
	MethodTaskDump = "task.dump"
	// MethodStepManage pauses, resumes or kills a step.
	MethodStepManage = "step.manage"
	// MethodSystemUpdate resizes the worker pool.
	MethodSystemUpdate = "system.update"
	// MethodTerminalDisconnect closes a live terminal session.
	MethodTerminalDisconnect = "terminal.disconnect"
	// MethodReplayList lists the recorded terminal sessions.
	MethodReplayList = "replay.list"
)
