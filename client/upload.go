package client

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	tusVersion       = "1.0.0"
	tusContentType   = "application/offset+octet-stream"
	defaultChunkSize = 8 << 20
)

// Upload is a resumable tus upload into a task workspace.
type Upload struct {
	// URL is the absolute upload URL (the Location the server returned). Keep it
	// to resume the upload later with Client.ResumeUpload.
	URL string
	// Offset is the number of bytes the server has stored.
	Offset int64
	// Length is the total size, or -1 if still deferred.
	Length int64

	c *Client
}

// UploadOptions describes a new upload.
type UploadOptions struct {
	// Task is the task whose workspace receives the file.
	Task string
	// Filename is the target path inside the workspace (forward slashes, local,
	// no ':' or '\\'). Empty uses the upload ID.
	Filename string
	// Size is the total size in bytes; negative defers it (the size is then
	// fixed with the last chunk).
	Size int64
	// ChunkSize is the PATCH size; default 8 MiB.
	ChunkSize int64
}

func (c *Client) tus(ctx context.Context, method, target string, hdr map[string]string, body io.Reader, n int64) (*http.Response, error) {
	req, err := c.newRequest(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Tus-Resumable", tusVersion)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.ContentLength = n
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, c.readAPIError(resp)
	}
	return resp, nil
}

func (c *Client) metadata(kv ...string) string {
	parts := make([]string, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] == "" {
			continue
		}
		parts = append(parts, kv[i]+" "+base64.StdEncoding.EncodeToString([]byte(kv[i+1])))
	}
	return strings.Join(parts, ",")
}

// CreateUpload registers a new upload on the server without sending data.
func (c *Client) CreateUpload(ctx context.Context, opts UploadOptions) (*Upload, error) {
	if opts.Task == "" {
		return nil, errors.New("client: upload task is required")
	}
	hdr := map[string]string{"Upload-Metadata": c.metadata("task_name", opts.Task, "filename", opts.Filename)}
	if opts.Size >= 0 {
		hdr["Upload-Length"] = strconv.FormatInt(opts.Size, 10)
	} else {
		hdr["Upload-Defer-Length"] = "1"
	}
	resp, err := c.tus(ctx, http.MethodPost, c.url("/api/v1/files", nil), hdr, nil, 0)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if loc == "" {
		return nil, errors.New("client: server sent no upload Location")
	}
	base, _ := url.Parse(c.url("/api/v1/files", nil))
	ref, err := url.Parse(loc)
	if err != nil {
		return nil, err
	}
	return &Upload{URL: base.ResolveReference(ref).String(), Length: opts.Size, c: c}, nil
}

// ResumeUpload attaches to an existing upload and reads its current offset.
func (c *Client) ResumeUpload(ctx context.Context, uploadURL string) (*Upload, error) {
	u := &Upload{URL: uploadURL, c: c}
	return u, u.Refresh(ctx)
}

// Refresh reads the server-side offset and length (HEAD).
func (u *Upload) Refresh(ctx context.Context) error {
	resp, err := u.c.tus(ctx, http.MethodHead, u.URL, nil, nil, 0)
	if err != nil {
		return err
	}
	resp.Body.Close()
	off, err := strconv.ParseInt(resp.Header.Get("Upload-Offset"), 10, 64)
	if err != nil {
		return fmt.Errorf("client: bad Upload-Offset: %w", err)
	}
	u.Offset, u.Length = off, -1
	if l := resp.Header.Get("Upload-Length"); l != "" {
		if u.Length, err = strconv.ParseInt(l, 10, 64); err != nil {
			return fmt.Errorf("client: bad Upload-Length: %w", err)
		}
	}
	return nil
}

// Patch appends one chunk at the current offset. setLength fixes a deferred
// total size with this request.
func (u *Upload) Patch(ctx context.Context, chunk io.Reader, n int64, setLength int64) error {
	hdr := map[string]string{
		"Content-Type":  tusContentType,
		"Upload-Offset": strconv.FormatInt(u.Offset, 10),
	}
	if setLength >= 0 && u.Length < 0 {
		hdr["Upload-Length"] = strconv.FormatInt(setLength, 10)
	}
	resp, err := u.c.tus(ctx, http.MethodPatch, u.URL, hdr, chunk, n)
	if err != nil {
		return err
	}
	resp.Body.Close()
	off, err := strconv.ParseInt(resp.Header.Get("Upload-Offset"), 10, 64)
	if err != nil {
		return fmt.Errorf("client: bad Upload-Offset: %w", err)
	}
	u.Offset = off
	if setLength >= 0 && u.Length < 0 {
		u.Length = setLength
	}
	return nil
}

// Send uploads r from the current offset to the end in chunks. r must be
// positioned at u.Offset (use io.Seeker or skip bytes before calling). size is
// the total length, or -1 if unknown (read until EOF, deferring the length).
func (u *Upload) Send(ctx context.Context, r io.Reader, size, chunkSize int64) error {
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}
	buf := make([]byte, chunkSize)
	for {
		if size >= 0 && u.Offset >= size {
			if size == 0 && u.Length != 0 {
				break
			}
			return nil
		}
		n, err := io.ReadFull(r, buf)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return err
		}
		eof := err != nil
		set := int64(-1)
		if size < 0 && eof {
			set = u.Offset + int64(n)
		}
		if n > 0 || set >= 0 {
			if perr := u.Patch(ctx, strings.NewReader(string(buf[:n])), int64(n), set); perr != nil {
				return perr
			}
		}
		if eof {
			if size >= 0 && u.Offset < size {
				return io.ErrUnexpectedEOF
			}
			return nil
		}
	}
	return nil
}

// Terminate deletes the upload on the server.
func (u *Upload) Terminate(ctx context.Context) error {
	resp, err := u.c.tus(ctx, http.MethodDelete, u.URL, nil, nil, 0)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// UploadFile creates an upload and sends r in one go. For resumable behaviour
// use CreateUpload / ResumeUpload and Upload.Send directly.
func (c *Client) UploadFile(ctx context.Context, opts UploadOptions, r io.Reader) (*Upload, error) {
	u, err := c.CreateUpload(ctx, opts)
	if err != nil {
		return nil, err
	}
	if err := u.Send(ctx, r, opts.Size, opts.ChunkSize); err != nil {
		return u, err
	}
	return u, nil
}
