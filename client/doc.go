// Package client is the Go client of the TaskFlow API.
//
// Use [New] to create a client for tasks, steps, workspaces, approval callbacks,
// system status, terminal administration, and resumable uploads. Use
// [Client.DialStream] for subscriptions and calls over the shared WebSocket,
// or [Client.OpenShell] for an interactive terminal session.
package client
