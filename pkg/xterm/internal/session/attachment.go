package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/xmapst/taskflow/pkg/xterm/attach"
	"github.com/xmapst/taskflow/pkg/xterm/internal/command"
	"github.com/xmapst/taskflow/pkg/xterm/record"
	"github.com/xmapst/taskflow/pkg/xterm/zmodem"
)

// sessionAttachment adapts the framed SessionIO API to Attach. Its input
// ports are invoked on the input goroutine; terminal text is observed on the
// output goroutine. Notice output may originate from the input goroutine.
type sessionAttachment struct {
	transport SessionIO
	keys      func(context.Context, []byte, func([]byte) error, func([]byte) error) error
	transfer  *zmodem.Stream
	resync    func()
	resize    func(int16, int16) error
	classify  func([]byte) ([]byte, []byte, error)
	flush     func() []byte
	observe   func([]byte)
	record    func([]byte) error
	outputMu  sync.Mutex

	cols, rows int16 // size the terminal was opened with
}

func newSessionAttachment(transport SessionIO, commands *command.Control, recorder *record.Recorder, transfer *zmodem.Stream, cols, rows int16) *sessionAttachment {
	a := &sessionAttachment{
		transport: transport,
		cols:      cols,
		rows:      rows,
		keys:      commands.Write,
		transfer:  transfer,
		resync:    commands.InvalidateEcho,
		classify:  transfer.Output,
		flush:     transfer.Finish,
		observe:   commands.ObserveOutput,
		resize: func(cols, rows int16) error {
			if recorder != nil {
				if err := recorder.Resize(cols, rows); err != nil {
					return fmt.Errorf("%w: %w", ErrRecording, err)
				}
			}
			commands.Resize(cols)
			return nil
		},
	}
	if recorder != nil {
		a.record = recorder.Output
	}
	return a
}

func (a *sessionAttachment) writeTransfer(data []byte, canceled bool, err error, write func([]byte) error) error {
	if err != nil {
		return err
	}
	if canceled {
		// Invalidate before the write can produce a new shell prompt.
		a.resync()
	}
	if len(data) != 0 {
		if err := write(data); err != nil {
			return fmt.Errorf("write ZMODEM frame to PTY (%d bytes): %w", len(data), err)
		}
	}
	return nil
}

func (a *sessionAttachment) ForwardInput(ctx context.Context, dst attach.TerminalInput) error {
	cols, rows := a.cols, a.rows
	write := func(data []byte) error {
		n, err := dst.Write(data)
		if err == nil && n != len(data) {
			return io.ErrShortWrite
		}
		return err
	}
	notice := func(data []byte) error { return a.publish(ctx, data, data) }
	for ctx.Err() == nil {
		frame, err := a.transport.OnReceive(ctx)
		if err != nil {
			return err
		}
		switch {
		case frame.Transfer:
			data, canceled, err := a.transfer.Accept(frame.Data)
			if err := a.writeTransfer(data, canceled, err, write); err != nil {
				return err
			}
		case frame.Cols > 0 && frame.Rows > 0:
			// A repeated size must not invalidate an in-progress line's echo.
			if frame.Cols == cols && frame.Rows == rows {
				continue
			}
			if err := a.resize(frame.Cols, frame.Rows); err != nil {
				return err
			}
			if err := dst.Resize(frame.Cols, frame.Rows); err != nil {
				return err
			}
			cols, rows = frame.Cols, frame.Rows
		case len(frame.Data) != 0:
			if handled, data, canceled, err := a.transfer.AcceptTerminal(frame.Data); handled {
				if err := a.writeTransfer(data, canceled, err, write); err != nil {
					return err
				}
			} else if err := a.keys(ctx, frame.Data, write, notice); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func (a *sessionAttachment) SendOutput(ctx context.Context, raw []byte) error {
	wire, text, err := a.classify(raw)
	if err != nil {
		return err
	}
	a.observe(text)
	return a.publish(ctx, wire, text)
}

func (a *sessionAttachment) FinishOutput(ctx context.Context) error {
	// A partial handshake prefix already went to the client as raw bytes.
	text := a.flush()
	a.observe(text)
	return a.publish(ctx, nil, text)
}

// OnExit forwards the child's exit status to transports that want it.
func (a *sessionAttachment) OnExit(code uint32) {
	if h, ok := a.transport.(interface{ OnExit(uint32) }); ok {
		h.OnExit(code)
	}
}

func (a *sessionAttachment) OnDetach(err error) {
	if err == nil || errors.Is(err, ErrRecording) || errors.Is(err, zmodem.ErrFileRejected) {
		a.transport.OnFinish(err)
	}
}

func (a *sessionAttachment) publish(ctx context.Context, raw, text []byte) error {
	// Keep notices and terminal output in the same order for both sinks.
	a.outputMu.Lock()
	defer a.outputMu.Unlock()
	if a.record != nil && len(text) != 0 {
		if err := a.record(text); err != nil {
			return fmt.Errorf("%w: %w", ErrRecording, err)
		}
	}
	if len(raw) != 0 {
		return a.transport.OnSend(ctx, raw)
	}
	return nil
}
