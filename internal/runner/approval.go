package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/xmapst/xdag"
	"golang.org/x/net/http/httpguts"
	"sigs.k8s.io/yaml"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/api/iproto"
	"github.com/xmapst/taskflow/internal/storage"
)

var (
	errApprovalRejected = errors.New("approval rejected")
	// errCreateFailed marks failures of the creation request. Only these are
	// retryable: any later failure means the platform already holds the request.
	errCreateFailed = errors.New("approval creation failed")
)

var approvalClient = &http.Client{
	Timeout: 15 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

type approvalRunner struct {
	storage *storage.StepStore
}

func newApprovalRunner(stg *storage.StepStore, _, _ string) (IRunner, error) {
	return &approvalRunner{storage: stg}, nil
}

// Run marks every failure non-retryable except a failed creation request: once
// the platform holds the request, retrying would duplicate it or override a
// human rejection. A canceled parent context is never retried either.
func (r *approvalRunner) Run(ctx context.Context) (api.ExecCode, error) {
	code, err := r.run(ctx)
	if err != nil && (!errors.Is(err, errCreateFailed) || ctx.Err() != nil) {
		err = fmt.Errorf("%w: %w", xdag.ErrNonRetryable, err)
	}
	return code, err
}

func (r *approvalRunner) run(ctx context.Context) (api.ExecCode, error) {
	inbox := InboxFrom(ctx)
	if inbox == nil {
		return api.ExecCodeSystemErr, errors.New("approval step requires an inbox")
	}
	content, err := r.storage.Content()
	if err != nil {
		return api.ExecCodeSystemErr, err
	}
	config, err := r.parseApprovalConfig(content)
	if err != nil {
		return api.ExecCodeSystemErr, err
	}
	timeout, err := r.storage.Timeout()
	if err != nil {
		return api.ExecCodeSystemErr, err
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if err = r.await(ctx, inbox, config); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			// A timeout while creating the request stays retryable; one while
			// waiting for the decision is not.
			if errors.Is(err, errCreateFailed) {
				err = fmt.Errorf("%w: %w", errCreateFailed, api.ErrExecTimeOut)
			} else {
				err = api.ErrExecTimeOut
			}
			return api.ExecCodeTimeout, err
		}
		return api.ExecCodeFailed, err
	}
	r.storage.Log().Write("External approval granted")
	return api.ExecCodeSuccess, nil
}

func (*approvalRunner) Clear() error { return nil }

// await sends the creation request and blocks for the decision. The inbox is
// armed before the request goes out: the platform can answer before its creation
// response arrives. Only a successful creation response releases the step, and
// the token is fresh per call so decisions for earlier attempts are ignored.
func (r *approvalRunner) await(ctx context.Context, inbox *Inbox, config iproto.ApprovalConfig) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", errCreateFailed, err)
	}
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	identity := iproto.ApprovalRequest{
		ID:       hex.EncodeToString(nonce[:]),
		TaskName: r.storage.TaskName(),
		StepName: r.storage.Name(),
	}
	req, err := r.newApprovalRequest(ctx, config, identity)
	if err != nil {
		return err
	}
	decision, err := inbox.Await(ctx,
		func(s Signal) bool {
			return s.Token == identity.ID && (s.Status == config.Callback.Approved || s.Status == config.Callback.Rejected)
		},
		func() error {
			if err := r.create(ctx, req); err != nil {
				return err
			}
			r.storage.Log().Write("Waiting for external approval")
			return nil
		},
	)
	if err != nil {
		return err
	}
	if decision.Status != config.Callback.Approved {
		return errApprovalRejected
	}
	return nil
}

func (*approvalRunner) create(ctx context.Context, req *http.Request) error {
	resp, err := approvalClient.Do(req)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("%w: %w", errCreateFailed, cerr)
		}
		return fmt.Errorf("%w: request failed", errCreateFailed)
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: HTTP %d", errCreateFailed, resp.StatusCode)
	}
	return nil
}

func (r *approvalRunner) parseApprovalConfig(content string) (iproto.ApprovalConfig, error) {
	var c iproto.ApprovalConfig
	raw := []byte(content)
	// Preserve numeric spelling and precision for JSON input. YAML input is
	// normalized to JSON before applying the same schema.
	if !jsontext.Value(raw).IsValid(jsontext.AllowDuplicateNames(true)) {
		var err error
		raw, err = yaml.YAMLToJSONStrict(raw)
		if err != nil {
			return c, errors.New("invalid approval content: expected YAML or JSON")
		}
	}
	if err := json.Unmarshal(raw, &c, json.RejectUnknownMembers(true)); err != nil {
		return c, errors.New("invalid approval content: check request and callback fields and use scalar header/query values")
	}
	if _, err := r.parseApprovalURL(c.Request.URL); err != nil {
		return c, errors.New("approval request.url must be an absolute HTTP(S) URL without user info or fragment")
	}
	if _, err := r.parseApprovalURL(c.Callback.URL); err != nil {
		return c, errors.New("approval callback.url must be an absolute HTTP(S) URL without user info or fragment")
	}
	c.Request.Method = strings.ToUpper(strings.TrimSpace(c.Request.Method))
	if c.Request.Method == "" {
		c.Request.Method = http.MethodPost
	}
	if c.Request.Method != http.MethodPost && c.Request.Method != http.MethodPut {
		return c, errors.New("approval request.method must be POST or PUT")
	}
	seen := make(map[string]bool)
	for key, value := range c.Request.Headers {
		name := http.CanonicalHeaderKey(key)
		if !httpguts.ValidHeaderFieldName(key) || !httpguts.ValidHeaderFieldValue(string(value)) || seen[name] {
			return c, errors.New("invalid or duplicate approval request header")
		}
		switch name {
		case "Content-Type":
			mediaType, _, err := mime.ParseMediaType(string(value))
			if err != nil || mediaType != "application/json" {
				return c, errors.New("approval request Content-Type must be application/json")
			}
		}
		seen[name] = true
	}
	if c.Callback.Approved == "" {
		c.Callback.Approved = "approved"
	}
	if c.Callback.Rejected == "" {
		c.Callback.Rejected = "rejected"
	}
	if c.Callback.Approved == c.Callback.Rejected {
		return c, errors.New("approval callback.approved and callback.rejected must differ")
	}
	return c, nil
}

func (r *approvalRunner) parseApprovalURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || strings.Contains(raw, "#") {
		return nil, errors.New("invalid HTTP URL")
	}
	if _, err := url.ParseQuery(u.RawQuery); err != nil {
		return nil, errors.New("invalid URL query")
	}
	return u, nil
}

func (r *approvalRunner) newApprovalRequest(ctx context.Context, c iproto.ApprovalConfig, message iproto.ApprovalRequest) (*http.Request, error) {
	target, err := r.parseApprovalURL(c.Request.URL)
	if err != nil {
		return nil, err
	}
	q := target.Query()
	for key, value := range c.Request.Query {
		q.Set(key, string(value))
	}
	target.RawQuery = q.Encode()
	body, err := json.Marshal(iproto.ApprovalCreate{ApprovalRequest: message, Callback: c.Callback})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, c.Request.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid approval creation request")
	}
	for key, value := range c.Request.Headers {
		req.Header.Set(key, string(value))
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}
