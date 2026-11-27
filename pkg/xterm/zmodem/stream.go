package zmodem

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

var zmodemSequence atomic.Uint64

// Stream observes the PTY output to keep protocol bytes out of recordings.
// With an auditor, it also holds each sender offer until authorized.
type Stream struct {
	mu      sync.Mutex
	parser  *ZmodemParser
	pending []byte
	can     int
	oo      int
	closed  bool

	auditSink Auditor
	audit     fileAudit
	gate      *offerGate
	blocked   error

	id                  uint64
	started             time.Time
	direction           string
	fromPTY, fromClient int64
	dropReason          string

	idleTimeout time.Duration
}

// DefaultIdleTimeout is how long an active transfer may stay silent before
// browser frames are dropped.
const DefaultIdleTimeout = 2 * time.Minute

// SetIdleTimeout changes the silence limit of an active transfer; d <= 0
// restores DefaultIdleTimeout. Call it before the stream is shared.
func (z *Stream) SetIdleTimeout(d time.Duration) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.idleTimeout = d
}

// NewStream creates a classifier and input gate for one terminal session.
func NewStream() *Stream { return &Stream{parser: New()} }

// NewAuditedStream additionally reports protocol-observed per-file outcomes.
// The sink runs synchronously outside parser locks; calls can be concurrent.
func NewAuditedStream(sink Auditor) *Stream {
	z := NewStream()
	z.auditSink = sink
	if sink != nil {
		z.parser.observeHeader = func(h *ZmodemHeader) { z.auditHeader(z.direction == TypeDownload, h.Type) }
		z.parser.observeOffer = func(info *ZFileInfo) {
			if z.direction == TypeDownload {
				z.auditOffer(info)
			}
		}
	}
	return z
}

// Called with mu held. Counts describe protocol traffic, not file size.
func (z *Stream) logEnd(reason string) {
	if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		return
	}
	slog.Debug("WebShell ZMODEM transfer ended", slog.Uint64("transfer_id", z.id), slog.String("direction", z.direction),
		slog.String("reason", reason), slog.Int64("pty_bytes_observed", z.fromPTY), slog.Int64("client_bytes_accepted", z.fromClient),
		slog.Int64("elapsed_ms", time.Since(z.started).Milliseconds()))
}

// Output returns the bytes allowed to reach the browser and the text to record.
func (z *Stream) Output(data []byte) (forward, text []byte, err error) {
	z.mu.Lock()
	defer z.unlockAndPublish()
	if z.blocked != nil {
		return nil, nil, z.blocked
	}
	if z.closed {
		return data, data, nil
	}
	if z.parser.IsStartSession() {
		return z.outputTransfer(data)
	}
	// 快路径：握手头以 ZPAD 开头，没有残留前缀且本块不含 ZPAD，就不可能是握手
	// (也不可能以握手前缀结尾)，原样透传，避免对每个输出块做拷贝和扫描。
	if len(z.pending) == 0 && bytes.IndexByte(data, ZPAD) < 0 {
		return data, data, nil
	}
	raw := data
	previous := len(z.pending) // Earlier prefix bytes have already reached the browser.
	data = append(z.pending, data...)
	z.pending = nil
	var plain []byte
	prefix := HexHeaderPrefix
	for len(data) > 0 {
		i := bytes.Index(data, prefix)
		if i < 0 {
			for n := min(len(prefix)-1, len(data)); n > 0; n-- {
				if bytes.HasSuffix(data, prefix[:n]) {
					z.pending = bytes.Clone(data[len(data)-n:])
					data = data[:len(data)-n]
					break
				}
			}
			return raw, append(plain, data...), nil
		}
		plain = append(plain, data[:i]...)
		data = data[i:]
		h, end, ok := DecodeHexFrameHeader(data)
		if !ok && len(data) < 64 && bytes.IndexByte(data, '\n') < 0 && bytes.IndexByte(data, 0x8a) < 0 {
			z.pending = bytes.Clone(data)
			return raw, plain, nil
		}
		if ok && (h.Type == ZRQINIT || h.Type == ZRINIT) {
			z.can, z.oo = 0, 0
			z.id, z.started = zmodemSequence.Add(1), time.Now()
			z.direction = TypeDownload
			if h.Type == ZRINIT {
				z.direction = TypeUpload
			}
			z.startFileAudit()
			z.fromPTY, z.fromClient, z.dropReason = int64(end+1), 0, ""
			slog.Debug("WebShell ZMODEM PTY handshake detected", slog.Uint64("transfer_id", z.id),
				slog.String("direction", z.direction), slog.String("frame", FrameType(h.Type).String()), slog.Int("bytes", len(data)))
			z.parser.Parse(data[:end+1])
			wire, trailing, err := z.outputTransfer(data[end+1:])
			if err != nil {
				return nil, nil, err
			}
			n := len(plain) + end + 1 - previous
			return append(bytes.Clone(raw[:n]), wire...), append(plain, trailing...), nil
		}
		plain = append(plain, data[0])
		data = data[1:]
	}
	return raw, plain, nil
}

func (z *Stream) idleLimit() time.Duration {
	if z.idleTimeout > 0 {
		return z.idleTimeout
	}
	return DefaultIdleTimeout
}

func (z *Stream) outputTransfer(data []byte) (wire, text []byte, err error) {
	z.fromPTY += int64(len(data))
	wire = data
	if z.gate != nil && z.direction == TypeDownload {
		wire, err = z.filterOffer(data)
		if err != nil {
			return nil, nil, err
		}
	}
	if len(wire) == 0 {
		return wire, nil, nil
	}
	text = z.parser.Parse(wire)
	if !z.parser.IsStartSession() {
		reason := "pty_finished"
		if z.parser.AbnormalFinish {
			reason = "pty_abnormal_finish"
		}
		z.endPTYAudit(reason)
		z.logEnd(reason)
	}
	return wire, text, nil
}

func (z *Stream) endPTYAudit(reason string) {
	z.gate = nil
	if z.parser.ConsumeLastAbort() {
		z.finishFileAudit(FileAborted, "pty_cancel")
	} else {
		z.finishFileAudit(FileIncomplete, reason)
	}
	z.audit.client = ZSession{}
}

// Finish closes observation and returns a partial unclassified handshake prefix.
func (z *Stream) Finish() []byte {
	z.mu.Lock()
	defer z.unlockAndPublish()
	if z.closed {
		return nil
	}
	z.closed = true
	z.gate = nil
	z.finishFileAudit(FileIncomplete, "terminal_session_closed")
	z.audit.client = ZSession{}
	if z.parser.IsStartSession() {
		z.logEnd("terminal_session_closed")
	} else if len(z.pending) > 0 {
		slog.Debug("WebShell ZMODEM incomplete PTY handshake at close", slog.Int("bytes", len(z.pending)))
	}
	pending := z.pending
	z.pending = nil
	return pending
}

// Accept returns only the portion to forward to the PTY. Bytes after a
// recognized end marker must not ride the transfer path back to the shell.
// canceled reports a browser-initiated cancellation of the active transfer.
func (z *Stream) Accept(data []byte) ([]byte, bool, error) {
	z.mu.Lock()
	defer z.unlockAndPublish()
	return z.accept(data)
}

// AcceptTerminal routes unmarked input through the same gate while a transfer
// is active. Outside a transfer the caller must use ordinary command control.
func (z *Stream) AcceptTerminal(data []byte) (handled bool, forward []byte, canceled bool, err error) {
	z.mu.Lock()
	defer z.unlockAndPublish()
	if z.gate == nil && z.blocked == nil {
		return false, nil, false, nil
	}
	forward, canceled, err = z.accept(data)
	return true, forward, canceled, err
}

func (z *Stream) accept(data []byte) (forward []byte, canceled bool, err error) {
	if z.blocked != nil {
		return nil, false, z.blocked
	}
	if z.closed || len(data) == 0 {
		return nil, false, nil
	}
	reason := ""
	if !z.parser.IsStartSession() {
		reason = "no_active_pty_handshake"
	} else if z.parser.IsExpired(time.Now(), z.idleLimit()) {
		reason = "idle_deadline_exceeded"
		z.finishFileAudit(FileIncomplete, reason)
		z.audit.client = ZSession{}
	}
	if reason != "" {
		if z.dropReason != reason {
			z.dropReason = reason
			slog.Debug("WebShell ZMODEM browser frame dropped", slog.Uint64("transfer_id", z.id),
				slog.String("reason", reason), slog.Int("bytes", len(data)))
		}
		return nil, false, nil
	}
	defer func() {
		if z.gate != nil && z.direction == TypeUpload {
			forward, err = z.filterOffer(forward)
			if err != nil {
				return
			}
		}
		if z.fromClient == 0 {
			slog.Debug("WebShell ZMODEM first browser frame accepted", slog.Uint64("transfer_id", z.id),
				slog.String("direction", z.direction), slog.Int("bytes", len(forward)))
		}
		z.fromClient += int64(len(forward))
		if z.auditSink != nil {
			z.audit.client.consume(forward)
		}
		if canceled {
			z.gate = nil
			z.finishFileAudit(FileAborted, "browser_cancel")
			z.audit.client = ZSession{}
			z.logEnd("browser_cancel")
		} else if !z.parser.IsStartSession() {
			z.gate = nil
			z.finishFileAudit(FileIncomplete, "browser_final_oo")
			z.audit.client = ZSession{}
			z.logEnd("browser_final_oo")
		}
	}()
	z.parser.MarkActive()
	awaitingEnd := z.parser.AwaitingUploadEnd()
	for i, b := range data {
		if b == 0x18 {
			z.can++
		} else {
			z.can = 0
		}
		if z.can == 5 {
			z.parser.Abort()
			end := i + 1
			// Keep the standard CAN/backspace cancellation padding.
			for end < len(data) && (data[end] == 0x18 || data[end] == '\b') {
				end++
			}
			return data[:end], true, nil
		}
		if awaitingEnd && b == 'O' {
			z.oo++
			if z.oo == 2 {
				z.parser.Parse([]byte("OO"))
				return data[:i+1], false, nil
			}
		} else {
			z.oo = 0
		}
	}
	return data, false, nil
}
