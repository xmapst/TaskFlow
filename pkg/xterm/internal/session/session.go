package session

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/xmapst/taskflow/pkg/xterm/attach"
	"github.com/xmapst/taskflow/pkg/xterm/internal/command"
	"github.com/xmapst/taskflow/pkg/xterm/pty"
	"github.com/xmapst/taskflow/pkg/xterm/record"
	"github.com/xmapst/taskflow/pkg/xterm/zmodem"
)

// Input is a terminal data frame, a ZMODEM frame, or a resize request.
// For a resize, both dimensions must be positive.
type Input struct {
	Data       []byte
	Transfer   bool
	Cols, Rows int16
}

// SessionIO adapts a bidirectional transport to a terminal. Receive and Send
// must return when ctx is canceled; they may run concurrently. Finish runs
// before transport cancellation on normal exit or an in-session recording error.
// A transport may also implement OnExit(code uint32) to learn the shell's exit
// status; it runs just before OnFinish when the shell ended on its own.
type SessionIO interface {
	OnReceive(context.Context) (Input, error)
	OnSend(context.Context, []byte) error
	OnFinish(error)
}

var ErrRecording = errors.New("terminal recording failed")

type Config struct {
	Command          string
	Args             []string
	Dir              string
	Env              []string
	Term             string
	Cols, Rows       int16 // initial terminal size; zero means the default
	Title            string
	Recording        io.Writer
	FileAudit        zmodem.Auditor
	CommandAudit     command.Auditor
	CommandIntercept command.Interceptor
}

func Run(ctx context.Context, transport SessionIO, cfg Config) (err error) {
	if cfg.Cols <= 0 {
		cfg.Cols = pty.DefaultCols
	}
	if cfg.Rows <= 0 {
		cfg.Rows = pty.DefaultRows
	}
	term, err := pty.OpenConfig(ctx, pty.Config{Command: cfg.Command, Args: cfg.Args, Dir: cfg.Dir, Env: cfg.Env, Term: cfg.Term, Cols: cfg.Cols, Rows: cfg.Rows})
	if err != nil {
		return fmt.Errorf("open terminal: %w", err)
	}
	recorder, err := record.NewWithHeader(cfg.Recording, cfg.Cols, cfg.Rows, record.Header{Title: cfg.Title, Env: recordEnv(cfg)})
	if err != nil && !errors.Is(err, record.ErrMissingWriter) {
		return errors.Join(fmt.Errorf("%w: start: %w", ErrRecording, err), term.Close())
	}
	if recorder != nil {
		defer func() {
			if finishErr := recorder.Close(); finishErr != nil {
				err = errors.Join(err, fmt.Errorf("%w: finalize: %w", ErrRecording, finishErr))
			}
		}()
	}
	var childInForeground func() bool
	if fg, ok := term.(pty.Foreground); ok {
		childInForeground = fg.ChildInForeground
	}
	control := command.New(cfg.Cols, cfg.CommandAudit, cfg.CommandIntercept, childInForeground)
	transfer := zmodem.NewAuditedStream(cfg.FileAudit)
	// A disconnect or transport error can bypass FinishOutput. Finalize
	// after Attach has joined the pumps so no last file offer is missed.
	defer transfer.Finish()
	return attach.Attach(ctx, term, newSessionAttachment(transport, control, recorder, transfer, cfg.Cols, cfg.Rows))
}

// recordEnv mirrors what asciinema stores: the shell and terminal type.
func recordEnv(cfg Config) [][2]string {
	shell := cfg.Command
	if shell == "" {
		shell = "default"
	}
	term := cfg.Term
	if term == "" {
		term = pty.DefaultTerm
	}
	return [][2]string{{"SHELL", shell}, {"TERM", term}}
}
