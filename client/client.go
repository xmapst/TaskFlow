package client

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/xmapst/taskflow/api"
)

const (
	defaultUserAgent      = "taskflow-client/v1"
	defaultRequestTimeout = 30 * time.Second
	maxBodyBytes          = 64 << 20
	maxErrorBody          = 4 << 10
)

// Client talks to one TaskFlow server. It is safe for concurrent use.
type Client struct {
	root    string // base URL without trailing slash
	base    *url.URL
	hc      *http.Client
	header  http.Header
	timeout time.Duration
	retries int
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the default http.Client, e.g. to set TLS or proxy
// options. Do not set http.Client.Timeout on it: it would also cut streaming
// responses; use WithRequestTimeout instead.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.hc = hc
		}
	}
}

// WithHeader adds a header to every request, including WebSocket handshakes.
func WithHeader(key, value string) Option {
	return func(c *Client) { c.header.Add(key, value) }
}

// WithBearerToken sends "Authorization: Bearer <token>" with every request.
func WithBearerToken(token string) Option {
	return func(c *Client) { c.header.Set("Authorization", "Bearer "+token) }
}

// WithBasicAuth sends HTTP basic credentials with every request.
func WithBasicAuth(user, password string) Option {
	return func(c *Client) {
		r := &http.Request{Header: http.Header{}}
		r.SetBasicAuth(user, password)
		c.header.Set("Authorization", r.Header.Get("Authorization"))
	}
}

// WithUserAgent overrides the User-Agent header.
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.header.Set("User-Agent", ua) }
}

// WithRequestTimeout bounds every non-streaming request whose context has no
// deadline of its own. Zero disables the bound. The default is 30 seconds.
func WithRequestTimeout(d time.Duration) Option {
	return func(c *Client) { c.timeout = d }
}

// WithRetries retries idempotent requests (GET, HEAD, DELETE) up to n more times
// after a transport error or a 502/503/504 answer, with exponential backoff.
func WithRetries(n int) Option {
	return func(c *Client) { c.retries = max(n, 0) }
}

// New returns a client for the server at baseURL, e.g. "http://host:2376". A
// path prefix is kept, so a server mounted behind a reverse proxy works too.
func New(baseURL string, opts ...Option) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("client: invalid base URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("client: base URL %q must be http(s)://host[:port]", baseURL)
	}
	u.RawQuery, u.Fragment = "", ""
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = strings.TrimRight(u.RawPath, "/")
	c := &Client{
		base:    u,
		root:    u.String(),
		hc:      &http.Client{},
		header:  http.Header{"User-Agent": {defaultUserAgent}},
		timeout: defaultRequestTimeout,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// APIError is returned when the server reports a failure: either a non-2xx
// HTTP answer or a response envelope / stream frame with a failing code.
type APIError struct {
	// Status is the HTTP status; zero for errors that arrived over the stream.
	Status int
	// Code is the TaskFlow code from the envelope; zero if none was sent.
	Code    api.Code
	Message string
}

func (e *APIError) Error() string {
	var b strings.Builder
	b.WriteString("taskflow")
	if e.Status != 0 {
		_, _ = fmt.Fprintf(&b, ": http %d", e.Status)
	}
	if e.Code != 0 {
		_, _ = fmt.Fprintf(&b, ": code %d", e.Code)
	}
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	return b.String()
}

// IsNotFound reports whether err is a server answer meaning "no such thing".
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && (ae.Status == http.StatusNotFound || ae.Code == api.CodeNoData)
}

// Result is a response whose Code is meaningful even on success: it carries the
// run state of a task or step (running, pending, paused, failed, ...).
type Result[T any] struct {
	Code    api.Code
	Message string
	Data    T
}

func (c *Client) url(path string, q url.Values) string {
	u := c.root + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func (c *Client) newRequest(ctx context.Context, method, target string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	for k, v := range c.header {
		req.Header[k] = append([]string(nil), v...)
	}
	return req, nil
}

func (c *Client) idempotent(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodDelete
}

// roundTrip sends one buffered request. A response is only returned for
// non-error HTTP statuses; 4xx/5xx are converted to *APIError.
func (c *Client) roundTrip(ctx context.Context, method, path string, q url.Values, body []byte, contentType, accept string) (*http.Response, context.CancelFunc, error) {
	cancel := context.CancelFunc(func() {})
	if _, ok := ctx.Deadline(); !ok && c.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
	}
	target := c.url(path, q)
	attempts := 1
	if c.idempotent(method) {
		attempts += c.retries
	}
	var lastErr error
	for i := range attempts {
		if i > 0 {
			delay := min(200*time.Millisecond<<(i-1), 5*time.Second)
			delay += rand.N(delay/2 + 1)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				cancel()
				return nil, nil, ctx.Err()
			}
		}
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		req, err := c.newRequest(ctx, method, target, rd)
		if err != nil {
			cancel()
			return nil, nil, err
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				break
			}
			continue
		}
		if resp.StatusCode >= 400 {
			apiErr := c.readAPIError(resp)
			if i < attempts-1 && (resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504) {
				lastErr = apiErr
				continue
			}
			cancel()
			return nil, nil, apiErr
		}
		return resp, cancel, nil
	}
	cancel()
	return nil, nil, lastErr
}

// readAPIError consumes and closes resp.
func (c *Client) readAPIError(resp *http.Response) *APIError {
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	ae := &APIError{Status: resp.StatusCode}
	var env api.Response[any]
	if json.Unmarshal(data, &env) == nil && (env.Code != 0 || len(env.Message) > 0) {
		ae.Code, ae.Message = env.Code, env.Message.String()
		return ae
	}
	ae.Message = strings.TrimSpace(string(data))
	if ae.Message == "" {
		ae.Message = http.StatusText(resp.StatusCode)
	}
	return ae
}

// call performs a request answered with the standard envelope and returns it
// without judging the code.
func (c *Client) call[T any](ctx context.Context, method, path string, q url.Values, body []byte, contentType string) (*api.Response[T], error) {
	resp, cancel, err := c.roundTrip(ctx, method, path, q, body, contentType, "application/json")
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, err
	}
	env := new(api.Response[T])
	if err := json.Unmarshal(data, env, api.JSONDuration); err != nil {
		return nil, fmt.Errorf("client: decode response: %w", err)
	}
	return env, nil
}

func (c *Client) envelopeError[T any](env *api.Response[T]) *APIError {
	return &APIError{Code: env.Code, Message: env.Message.String()}
}

// data returns the payload of an envelope that must be successful.
func (c *Client) data[T any](env *api.Response[T], err error) (T, error) {
	if err != nil {
		var zero T
		return zero, err
	}
	if env.Code != api.CodeSuccess {
		var zero T
		return zero, c.envelopeError(env)
	}
	return env.Data, nil
}

// ok requires a successful envelope and discards its payload.
func (c *Client) ok(env *api.Response[any], err error) error {
	_, err = c.data(env, err)
	return err
}

// state returns the envelope as a Result. A non-success code is only an error
// when the server sent no data with it.
func (c *Client) state[T any](env *api.Response[T], err error) (*Result[T], error) {
	if err != nil {
		return nil, err
	}
	if env.Code != api.CodeSuccess && reflect.ValueOf(&env.Data).Elem().IsZero() {
		return nil, c.envelopeError(env)
	}
	return &Result[T]{Code: env.Code, Message: env.Message.String(), Data: env.Data}, nil
}

func (c *Client) marshal(v any) ([]byte, error) {
	return json.Marshal(v, api.JSONDuration)
}

func (c *Client) seg(s string) string { return url.PathEscape(s) }
