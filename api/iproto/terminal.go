package iproto

import (
	"time"
)

type WebShellSession struct {
	ID         string    `json:"id"`
	Connected  time.Time `json:"connectedAt"`
	RemoteAddr string    `json:"remoteAddr,omitempty"`
}
