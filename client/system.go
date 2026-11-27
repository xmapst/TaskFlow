package client

import (
	"context"
	"net/http"

	"github.com/xmapst/taskflow/api/iproto"
)

// System returns the worker pool size and the host resource metrics. Resizing
// the pool is a stream call: see Stream.ResizePool.
func (c *Client) System(ctx context.Context) (*iproto.SSystemConfig, error) {
	return c.data(c.call[*iproto.SSystemConfig](ctx, http.MethodGet, "/api/v1/system", nil, nil, ""))
}

// Version returns the server build information.
func (c *Client) Version(ctx context.Context) (*iproto.SVersion, error) {
	return c.data(c.call[*iproto.SVersion](ctx, http.MethodGet, "/version", nil, nil, ""))
}

// Healthz returns the server health report.
func (c *Client) Healthz(ctx context.Context) (*iproto.SHealthyz, error) {
	return c.data(c.call[*iproto.SHealthyz](ctx, http.MethodGet, "/healthyz", nil, nil, ""))
}

// Heartbeat succeeds when the server answers its liveness probe.
func (c *Client) Heartbeat(ctx context.Context) error {
	resp, cancel, err := c.roundTrip(ctx, http.MethodGet, "/heartbeat", nil, nil, "", "")
	if err != nil {
		return err
	}
	defer cancel()
	return resp.Body.Close()
}

// ApprovalCallback reports the decision of an external approval platform, as
// the platform itself would POST it to the callback URL.
func (c *Client) ApprovalCallback(ctx context.Context, decision iproto.ApprovalDecision) error {
	body, err := c.marshal(decision)
	if err != nil {
		return err
	}
	return c.ok(c.call[any](ctx, http.MethodPost, "/api/v1/approval/callback", nil, body, "application/json"))
}
