package pty

import "context"

const (
	DefaultCols = 80
	DefaultRows = 24
)

// PTY is a running command attached to a platform pseudoterminal. It provides
// raw terminal I/O, independent of any client protocol.
type PTY interface {
	// Read may return a platform-specific hangup error rather than io.EOF.
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	// Resize sets the dimensions in cells. Both must be positive.
	Resize(width, height int16) error
	// Wait returns the exit status without closing or draining the PTY. A normal
	// nonzero exit is not an error. Canceling ctx only stops this wait.
	Wait(ctx context.Context) (uint32, error)
	// Close releases the terminal and terminates the child if it is still running.
	// On Linux this kills every process in the child's session, so background jobs
	// do not outlive it; other Unix systems kill only its process group. It is idempotent and may discard unread output.
	Close() error
}

// Foreground is an optional capability for checking whether the shell has
// yielded the foreground terminal to another process group.
type Foreground interface {
	ChildInForeground() bool
}

// Config describes the command and terminal to open. The zero value starts the
// platform default shell in an 80x24 terminal.
type Config struct {
	// Command is an executable name or path. Empty selects Bash (falling back
	// to sh) on Unix or cmd.exe on Windows. Without Args, Dir and Env, a
	// Windows Command may be a native command line.
	Command string
	// Args are the arguments after the executable name.
	Args []string
	// Dir is the child's working directory; empty inherits the parent's.
	Dir string
	// Env entries are appended to the parent environment and win on conflict.
	Env []string
	// Term is the TERM value on Unix; empty means xterm-256color.
	Term string
	// Cols and Rows are the initial size; non-positive values use the defaults.
	Cols, Rows int16
}

// DefaultTerm is the TERM value given to the child on Unix.
const DefaultTerm = "xterm-256color"

// Open starts command in an 80-column by 24-row terminal; see OpenConfig. An
// empty command starts Bash (falling back to sh) on Unix or cmd.exe on Windows.
// On Unix a nonempty command is an executable name or path with no arguments;
// on Windows it is a native command line.
func Open(ctx context.Context, command string) (PTY, error) {
	return OpenConfig(ctx, Config{Command: command})
}

// OpenConfig starts the configured command in a platform terminal.
//
// Canceling ctx while the process is running initiates teardown. The caller
// should still call Close, including after normal exit. Read concurrently with
// Wait and drain remaining output before closing if it is needed.
func OpenConfig(ctx context.Context, cfg Config) (PTY, error) {
	if cfg.Cols <= 0 {
		cfg.Cols = DefaultCols
	}
	if cfg.Rows <= 0 {
		cfg.Rows = DefaultRows
	}
	if cfg.Term == "" {
		cfg.Term = DefaultTerm
	}
	term, err := openTerminal(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return term, nil
}
