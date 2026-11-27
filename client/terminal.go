package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/xmapst/taskflow/api/iproto"
)

// ListSessions returns the live WebShell sessions.
func (c *Client) ListSessions(ctx context.Context) ([]iproto.WebShellSession, error) {
	resp, cancel, err := c.roundTrip(ctx, http.MethodGet, "/api/v1/terminal/sessions", nil, nil, "", "application/json")
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer resp.Body.Close()
	var out []iproto.WebShellSession
	if err := json.UnmarshalRead(io.LimitReader(resp.Body, maxBodyBytes), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DisconnectSession forcibly closes a live WebShell session.
func (c *Client) DisconnectSession(ctx context.Context, id string) error {
	resp, cancel, err := c.roundTrip(ctx, http.MethodDelete, "/api/v1/terminal/sessions/"+c.seg(id), nil, nil, "", "")
	if err != nil {
		return err
	}
	defer cancel()
	return resp.Body.Close()
}

// ListReplays lists the recorded sessions, newest first. limit is 1..100
// (0 = server default 50).
func (c *Client) ListReplays(ctx context.Context, limit, offset int) (*iproto.ReplayList, error) {
	q := url.Values{}
	if limit != 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset != 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	resp, cancel, err := c.roundTrip(ctx, http.MethodGet, "/api/v1/terminal/replays", q, nil, "", "application/json")
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer resp.Body.Close()
	out := new(iproto.ReplayList)
	if err := json.UnmarshalRead(io.LimitReader(resp.Body, maxBodyBytes), out); err != nil {
		return nil, err
	}
	return out, nil
}

// OpenReplay streams a recording as asciinema v2 NDJSON; the first line is the
// header. The caller closes the returned reader.
func (c *Client) OpenReplay(ctx context.Context, id string) (io.ReadCloser, error) {
	return c.openStream(ctx, "/api/v1/terminal/replays/"+c.seg(id), "application/x-ndjson")
}

func (c *Client) openStream(ctx context.Context, path, accept string) (io.ReadCloser, error) {
	req, err := c.newRequest(ctx, http.MethodGet, c.url(path, nil), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, c.readAPIError(resp)
	}
	return resp.Body, nil
}

// Lines reads a NDJSON stream line by line. It is not safe for concurrent use.
type Lines struct {
	body io.Closer
	r    *bufio.Reader
}

// WatchSession follows the output of a live session over plain HTTP as
// asciinema v2 NDJSON lines. Keep-alive blank lines are skipped. Next returns
// io.EOF when the session ends. For a multiplexed alternative see
// Stream.WatchTerminalSession.
func (c *Client) WatchSession(ctx context.Context, id string) (*Lines, error) {
	body, err := c.openStream(ctx, "/api/v1/terminal/sessions/"+c.seg(id)+"/watch", "application/x-ndjson")
	if err != nil {
		return nil, err
	}
	return &Lines{body: body, r: bufio.NewReaderSize(body, 64<<10)}, nil
}

// Next returns the next non-empty line without its newline.
func (l *Lines) Next() ([]byte, error) {
	for {
		line, err := l.r.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			return line, nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, io.EOF
			}
			return nil, err
		}
	}
}

// Close releases the connection.
func (l *Lines) Close() error { return l.body.Close() }
