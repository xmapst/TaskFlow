package record

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Recorder encodes uncompressed asciinema v2 events. Construct it with New.
// Its methods are safe for concurrent use; the destination remains caller-owned.
//
// Each event is written with a single Write call and is not buffered, so a
// crash loses at most the event in flight. That matters more for an audit
// trail than the syscall savings a bufio.Writer would give.
type Recorder struct {
	mu      sync.Mutex
	w       io.Writer
	started time.Time
	line    []byte // reused across events to avoid per-event allocation
	pending []byte
	err     error
	closed  bool
}

// ErrMissingWriter indicates that no output destination was supplied.
var ErrMissingWriter = errors.New("recording requires a writer")

// Header holds the optional asciinema v2 header fields.
type Header struct {
	Title string
	// Env is written as the header's "env" object (asciinema records SHELL and
	// TERM). Keys are emitted in the given order.
	Env [][2]string
}

// New writes a cast header with the given initial dimensions.
func New(w io.Writer, cols, rows int16) (*Recorder, error) {
	return NewWithHeader(w, cols, rows, Header{})
}

// NewWithHeader is New with extra header fields. Strings may contain any
// bytes; invalid UTF-8 is replaced so the header stays valid JSON.
func NewWithHeader(w io.Writer, cols, rows int16, h Header) (*Recorder, error) {
	if nilRecordingWriter(w) {
		return nil, ErrMissingWriter
	}
	if cols <= 0 || rows <= 0 {
		return nil, errors.New("recording requires positive dimensions")
	}
	now := time.Now()
	r := &Recorder{w: w, started: now}
	r.line = append(r.line, `{"version":2,"width":`...)
	r.line = strconv.AppendInt(r.line, int64(cols), 10)
	r.line = append(r.line, `,"height":`...)
	r.line = strconv.AppendInt(r.line, int64(rows), 10)
	r.line = append(r.line, `,"timestamp":`...)
	r.line = strconv.AppendInt(r.line, now.Unix(), 10)
	if h.Title != "" {
		r.line = append(r.line, `,"title":`...)
		r.line = appendQuoted(r.line, []byte(strings.ToValidUTF8(h.Title, "\uFFFD")))
	}
	if len(h.Env) > 0 {
		r.line = append(r.line, `,"env":{`...)
		for i, kv := range h.Env {
			if i > 0 {
				r.line = append(r.line, ',')
			}
			r.line = appendQuoted(r.line, []byte(strings.ToValidUTF8(kv[0], "\uFFFD")))
			r.line = append(r.line, ':')
			r.line = appendQuoted(r.line, []byte(strings.ToValidUTF8(kv[1], "\uFFFD")))
		}
		r.line = append(r.line, '}')
	}
	r.line = append(r.line, "}\n"...)
	if err := r.flushLine(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Recorder) flushLine() error {
	n, err := r.w.Write(r.line)
	if err == nil && n != len(r.line) {
		err = io.ErrShortWrite
	}
	r.line = r.line[:0]
	r.err = err
	return err
}

// event appends one `[seconds,"kind","value"]` line. value must be valid UTF-8.
// Called with mu held.
func (r *Recorder) event(kind string, value []byte) error {
	if r.err != nil {
		return r.err
	}
	r.line = append(r.line, '[')
	r.line = strconv.AppendFloat(r.line, time.Since(r.started).Seconds(), 'f', 6, 64)
	r.line = append(r.line, `,"`...)
	r.line = append(r.line, kind...)
	r.line = append(r.line, `",`...)
	r.line = appendQuoted(r.line, value)
	r.line = append(r.line, "]\n"...)
	return r.flushLine()
}

// appendQuoted appends s as a JSON string. Only the characters JSON requires
// are escaped; U+2028/2029 and DEL are valid in JSON strings and stay literal.
func appendQuoted(dst, s []byte) []byte {
	const hex = "0123456789abcdef"
	dst = append(dst, '"')
	start := 0
	for i := range s {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' {
			continue
		}
		dst = append(dst, s[start:i]...)
		switch c {
		case '"', '\\':
			dst = append(dst, '\\', c)
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xf])
		}
		start = i + 1
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}

// Resize records a terminal size change.
func (r *Recorder) Resize(cols, rows int16) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return io.ErrClosedPipe
	}
	if cols <= 0 || rows <= 0 {
		return errors.New("recording requires positive dimensions")
	}
	size := strconv.AppendInt(nil, int64(cols), 10)
	size = append(size, 'x')
	size = strconv.AppendInt(size, int64(rows), 10)
	return r.event("r", size)
}

// Output records terminal output, preserving UTF-8 across calls.
func (r *Recorder) Output(data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return io.ErrClosedPipe
	}
	if r.err != nil {
		return r.err
	}
	if len(r.pending) > 0 {
		data = append(r.pending, data...)
	}
	// Hold back only a trailing incomplete sequence; everything before it is
	// complete (valid or not) and is emitted now.
	end := len(data)
	if tail := incompleteTail(data); tail > 0 {
		end -= tail
	}
	var err error
	if end > 0 {
		err = r.event("o", validUTF8(data[:end]))
	}
	// event has finished with data, so reusing pending's backing array is safe
	// even though data may alias it.
	r.pending = append(r.pending[:0], data[end:]...)
	return err
}

// incompleteTail returns the length of a truncated multi-byte sequence at the
// end of b, or 0 if b ends on a boundary.
func incompleteTail(b []byte) int {
	for n := 1; n <= min(utf8.UTFMax-1, len(b)); n++ {
		c := b[len(b)-n]
		if utf8.RuneStart(c) {
			if c >= utf8.RuneSelf && !utf8.FullRune(b[len(b)-n:]) {
				return n
			}
			return 0
		}
	}
	return 0
}

// validUTF8 returns b unchanged when it is valid, avoiding a copy on the
// overwhelmingly common path, and otherwise replaces invalid bytes.
func validUTF8(b []byte) []byte {
	if utf8.Valid(b) {
		return b
	}
	return bytes.ToValidUTF8(b, []byte("\uFFFD"))
}

// Close flushes a dangling partial sequence, but does not close the writer.
// Repeated calls return the same result.
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		if len(r.pending) > 0 {
			_ = r.event("o", []byte("\uFFFD"))
			r.pending = nil
		}
		r.closed = true
	}
	return r.err
}

func nilRecordingWriter(w io.Writer) bool {
	v := reflect.ValueOf(w)
	return w == nil || (v.Kind() == reflect.Pointer && v.IsNil())
}
