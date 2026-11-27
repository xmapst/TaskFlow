package client

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/api/iproto"
)

// ListWorkspace lists a directory of the task workspace; an empty path is the
// workspace root.
func (c *Client) ListWorkspace(ctx context.Context, task, path string) (*iproto.SFileListRes, error) {
	q := url.Values{}
	if path != "" {
		q.Set("path", path)
	}
	resp, cancel, err := c.roundTrip(ctx, http.MethodGet, "/api/v1/task/"+c.seg(task)+"/workspace", q, nil, "", "application/json")
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer resp.Body.Close()
	if resp.Header.Get("Content-Disposition") != "" {
		return nil, errors.New("client: workspace path is a file; use DownloadWorkspaceFile")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, err
	}
	env := new(api.Response[*iproto.SFileListRes])
	if err := json.Unmarshal(body, env, api.JSONDuration); err != nil {
		return nil, fmt.Errorf("client: decode response: %w", err)
	}
	return c.data(env, nil)
}

// Download is a workspace file being downloaded. Close it when done.
type Download struct {
	// Name is the file name announced by the server.
	Name        string
	ContentType string
	// Size is the content length, or -1 when unknown.
	Size int64
	io.ReadCloser
}

// DownloadWorkspaceFile streams a file of the task workspace. It has no
// request timeout other than the context.
func (c *Client) DownloadWorkspaceFile(ctx context.Context, task, path string) (*Download, error) {
	q := url.Values{"path": {path}}
	req, err := c.newRequest(ctx, http.MethodGet, c.url("/api/v1/task/"+c.seg(task)+"/workspace", q), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, c.readAPIError(resp)
	}
	disposition := resp.Header.Get("Content-Disposition")
	if disposition == "" {
		// A directory or an error envelope.
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
		env := new(api.Response[any])
		if json.Unmarshal(body, env) == nil && env.Code != api.CodeSuccess {
			return nil, c.envelopeError(env)
		}
		return nil, errors.New("client: workspace path is a directory; use ListWorkspace")
	}
	_, params, _ := mime.ParseMediaType(disposition)
	return &Download{
		Name:        params["filename"],
		ContentType: resp.Header.Get("Content-Type"),
		Size:        resp.ContentLength,
		ReadCloser:  resp.Body,
	}, nil
}

// DeleteWorkspace removes files or directories (recursively) from the task
// workspace. The server validates all paths first and reports the paths that
// could not be removed in the error.
func (c *Client) DeleteWorkspace(ctx context.Context, task string, paths ...string) error {
	if len(paths) == 0 {
		return errors.New("client: at least one path is required")
	}
	q := url.Values{"path": paths}
	return c.ok(c.call[any](ctx, http.MethodDelete, "/api/v1/task/"+c.seg(task)+"/workspace", q, nil, ""))
}
