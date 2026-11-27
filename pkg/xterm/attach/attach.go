package attach

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// TerminalInput is the writable side of an attached PTY.
type TerminalInput interface {
	io.Writer
	Resize(cols, rows int16) error
}

// Terminal is the lifecycle and byte I/O needed by Attach. Wait must stop
// when its context is canceled; Close must release blocked reads and writes.
type Terminal interface {
	TerminalInput
	io.Reader
	Wait(context.Context) (uint32, error)
	Close() error
}

// Attachment connects a client to a PTY. ForwardInput runs once and owns client
// input. SendOutput runs concurrently on the PTY output loop. Both must return
// when ctx is canceled (or a pending PTY operation is released by Close).
// SendOutput must not retain data past the call without copying it.
//
// An attachment may also implement FinishOutput(context.Context) error to flush
// buffered client-side output when PTY reads end. OnDetach(error), if present,
// is called once with the session outcome before cancellation and PTY closure;
// it must not wait for ForwardInput or SendOutput to stop.
//
// It may also implement OnExit(code uint32), called at most once, just before
// OnDetach, when the child exited on its own and its status is known. A child
// ended by a signal reports a platform-specific code. It is not called when
// the session ended for another reason first (client gone, ctx canceled).
type Attachment interface {
	ForwardInput(ctx context.Context, dst TerminalInput) error
	SendOutput(ctx context.Context, data []byte) error
}

// DefaultExitGrace is how long Attach keeps reading trailing output after the
// child has exited.
const DefaultExitGrace = 500 * time.Millisecond

// Option configures Attach.
type Option func(*options)

type options struct{ exitGrace time.Duration }

// WithExitGrace sets how long to keep draining output after the child exits.
// Zero or negative selects DefaultExitGrace.
func WithExitGrace(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.exitGrace = d
		}
	}
}

// Attach forwards input and output concurrently until either side stops or ctx
// is canceled. A process exit allows a bounded grace period for reading trailing
// output. Once both arguments are accepted, Attach owns and closes the PTY.
func Attach(ctx context.Context, term Terminal, client Attachment, opts ...Option) error {
	if term == nil || client == nil {
		return errors.New("terminal attachment requires a PTY and client")
	}
	cfg := options{exitGrace: DefaultExitGrace}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	input := make(chan error, 1)
	output := make(chan error, 1)
	exited := make(chan struct{})
	var exitCode uint32 // written before close(exited), read only after it
	var exitErr error

	var pumps sync.WaitGroup
	pumps.Go(func() {
		// Expose neither the read side nor process lifecycle to the input loop.
		input <- client.ForwardInput(ctx, struct{ TerminalInput }{term})
	})
	pumps.Go(func() { forwardOutput(ctx, term, client, output) })
	go func() {
		defer close(exited)
		exitCode, exitErr = term.Wait(ctx)
	}()

	err := awaitSession(ctx, input, output, exited, cfg.exitGrace)
	if observer, ok := client.(interface{ OnExit(uint32) }); ok {
		select {
		case <-exited:
			if exitErr == nil {
				observer.OnExit(exitCode)
			}
		default:
		}
	}
	if observer, ok := client.(interface{ OnDetach(error) }); ok {
		observer.OnDetach(err)
	}
	cancel()
	<-exited // Keep the process handle valid until Wait has returned on Windows.
	closeErr := term.Close()
	pumps.Wait()
	return errors.Join(err, closeErr)
}

func forwardOutput(ctx context.Context, term io.Reader, client Attachment, done chan<- error) {
	buf := make([]byte, 16<<10)
	for {
		n, readErr := term.Read(buf)
		if n > 0 {
			if err := client.SendOutput(ctx, buf[:n]); err != nil {
				done <- err
				if readErr == nil {
					// Closing ConPTY can still write output to the pipe.
					_, _ = io.Copy(io.Discard, term)
				}
				return
			}
		}
		if readErr != nil {
			if finisher, ok := client.(interface{ FinishOutput(context.Context) error }); ok {
				if err := finisher.FinishOutput(ctx); err != nil {
					done <- err
					return
				}
			}
			// A terminal hangup may be EOF or a platform-specific error (e.g. EIO).
			done <- ctx.Err()
			return
		}
	}
}

func awaitSession(ctx context.Context, input, output <-chan error, exited <-chan struct{}, grace time.Duration) error {
	select {
	case err := <-input:
		return err
	case err := <-output:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-exited:
		// Some output can still be buffered after the process has exited.
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case err := <-input:
			return err
		case err := <-output:
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
}
