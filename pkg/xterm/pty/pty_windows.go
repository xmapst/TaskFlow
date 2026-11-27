//go:build windows

package pty

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"

	"github.com/xmapst/taskflow/pkg/xterm/conpty"
)

type terminal struct {
	*conpty.Terminal
	process   *conpty.Process
	closeOnce sync.Once
	closeErr  error
}

func openTerminal(ctx context.Context, cfg Config) (*terminal, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pty, err := conpty.Open(int(cfg.Cols), int(cfg.Rows))
	if err != nil {
		return nil, err
	}
	var process *conpty.Process
	switch {
	case cfg.Command == "":
		process, err = pty.Start(ctx, conpty.Command{
			Path: "cmd.exe",
			Args: cfg.Args,
			Dir:  cfg.Dir,
			Env:  windowsEnv(cfg.Env),
		})
	case len(cfg.Args) == 0 && cfg.Dir == "" && len(cfg.Env) == 0:
		process, err = pty.StartCommandLine(ctx, cfg.Command)
	default:
		process, err = pty.Start(ctx, conpty.Command{
			Path: cfg.Command,
			Args: cfg.Args,
			Dir:  cfg.Dir,
			Env:  windowsEnv(cfg.Env),
		})
	}
	if err != nil {
		return nil, errors.Join(err, pty.Close())
	}
	return &terminal{Terminal: pty, process: process}, nil
}

// windowsEnv returns nil (inherit) without extras; otherwise the parent
// environment plus extras, since an explicit Env replaces it entirely.
func windowsEnv(extra []string) []string {
	if len(extra) == 0 {
		return nil
	}
	return append(os.Environ(), extra...)
}

func (t *terminal) Wait(ctx context.Context) (uint32, error) {
	state, err := t.process.WaitContext(ctx)
	if state == nil {
		return 0, err
	}
	if _, ok := errors.AsType[*exec.ExitError](err); ok {
		err = nil // This interface returns the exit code separately.
	}
	return uint32(state.ExitCode()), err
}

func (t *terminal) Close() error {
	t.closeOnce.Do(func() {
		err := t.process.Kill()
		if errors.Is(err, os.ErrProcessDone) {
			err = nil
		}
		t.closeErr = errors.Join(err, t.Terminal.Close())
	})
	return t.closeErr
}
