package xterm

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/xmapst/taskflow/pkg/xterm/attach"
	"github.com/xmapst/taskflow/pkg/xterm/internal/command"
	"github.com/xmapst/taskflow/pkg/xterm/internal/session"
	"github.com/xmapst/taskflow/pkg/xterm/pty"
	"github.com/xmapst/taskflow/pkg/xterm/record"
	"github.com/xmapst/taskflow/pkg/xterm/zmodem"
)

type (
	Input              = session.Input
	SessionIO          = session.SessionIO
	CommandAuditor     = command.Auditor
	CommandInterceptor = command.Interceptor
	TerminalInput      = attach.TerminalInput
	Attachment         = attach.Attachment
	PTY                = pty.PTY
	Recorder           = record.Recorder
)

var ErrRecording = session.ErrRecording

// AttachOption configures Attach.
type AttachOption = attach.Option

// WithExitGrace sets how long Attach keeps draining output after the child
// exits (default 500ms).
func WithExitGrace(d time.Duration) AttachOption { return attach.WithExitGrace(d) }

// Attach connects a client to an open PTY, taking ownership of the PTY.
func Attach(ctx context.Context, term PTY, client Attachment, opts ...AttachOption) error {
	return attach.Attach(ctx, term, client, opts...)
}

// OpenPTY starts an interactive command in a platform terminal.
func OpenPTY(ctx context.Context, command string) (PTY, error) {
	return pty.Open(ctx, command)
}

// NewRecorder writes an uncompressed asciinema v2 header to a caller-owned writer.
func NewRecorder(w io.Writer, cols, rows int16) (*Recorder, error) {
	return record.New(w, cols, rows)
}

type sessionConfig struct {
	command          string
	args             []string
	dir              string
	env              []string
	term             string
	title            string
	cols, rows       int16
	recording        io.Writer
	fileAudit        zmodem.Auditor
	commandAudit     CommandAuditor
	commandIntercept CommandInterceptor
}

// Option configures a terminal session.
type Option func(*sessionConfig)

// WithCommand selects the shell; an empty command uses the platform default.
func WithCommand(command string) Option {
	return func(cfg *sessionConfig) { cfg.command = command }
}

// WithArgs passes arguments to the command selected by WithCommand (or to the
// default shell). Windows applies them with a nonempty command as well.
func WithArgs(args ...string) Option {
	return func(cfg *sessionConfig) { cfg.args = args }
}

// WithDir sets the shell's working directory; empty inherits the server's.
func WithDir(dir string) Option {
	return func(cfg *sessionConfig) { cfg.dir = dir }
}

// WithEnv adds KEY=VALUE entries to the inherited environment; they win on
// conflict.
func WithEnv(env ...string) Option {
	return func(cfg *sessionConfig) { cfg.env = env }
}

// WithTerm sets TERM for the shell on Unix (default xterm-256color).
func WithTerm(term string) Option {
	return func(cfg *sessionConfig) { cfg.term = term }
}

// WithRecordingTitle sets the title field of the recording header.
func WithRecordingTitle(title string) Option {
	return func(cfg *sessionConfig) { cfg.title = title }
}

// WithSize sets the initial terminal size in cells, which also seeds the
// recording header. Non-positive values keep the 80x24 default. Later resize
// requests from the client still apply.
func WithSize(cols, rows int16) Option {
	return func(cfg *sessionConfig) { cfg.cols, cfg.rows = cols, rows }
}

// WithRecording enables uncompressed asciinema v2 recording. A nil writer
// disables it. The caller handles compression and closes the writer.
func WithRecording(w io.Writer) Option {
	return func(cfg *sessionConfig) { cfg.recording = w }
}

// WithCommandAuditor checks echo-verified commands before submitting them. A nil
// auditor leaves ordinary input unfiltered.
func WithCommandAuditor(check CommandAuditor) Option {
	return func(cfg *sessionConfig) { cfg.commandAudit = check }
}

// WithFileAuditor authorizes each ZMODEM file offer before forwarding it and
// reports later outcomes. Denial or an authorization error closes the pane.
// The callback runs synchronously and can be called concurrently.
func WithFileAuditor(check zmodem.Auditor) Option {
	return func(cfg *sessionConfig) { cfg.fileAudit = check }
}

// WithCommandInterceptor handles selected commands after auditing. It requires
// WithCommandAuditor; it has no effect without an auditor.
func WithCommandInterceptor(intercept CommandInterceptor) Option {
	return func(cfg *sessionConfig) { cfg.commandIntercept = intercept }
}

// Run opens a terminal, handles input and output concurrently, and releases
// the terminal before returning. Later options of the same kind take precedence.
func Run(ctx context.Context, transport SessionIO, opts ...Option) error {
	if transport == nil {
		return errors.New("terminal session requires a transport")
	}
	var cfg sessionConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return session.Run(ctx, transport, session.Config{
		Command:          cfg.command,
		Args:             cfg.args,
		Dir:              cfg.dir,
		Env:              cfg.env,
		Term:             cfg.term,
		Title:            cfg.title,
		Cols:             cfg.cols,
		Rows:             cfg.rows,
		Recording:        cfg.recording,
		FileAudit:        cfg.fileAudit,
		CommandAudit:     cfg.commandAudit,
		CommandIntercept: cfg.commandIntercept,
	})
}
