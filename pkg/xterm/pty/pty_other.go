//go:build !windows

package pty

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

type terminal struct {
	*os.File
	cmd       *exec.Cmd
	done      chan struct{}
	waitErr   error
	closeOnce sync.Once
	closeErr  error
}

func (t *terminal) Resize(width, height int16) error {
	if width <= 0 || height <= 0 {
		return os.ErrInvalid
	}
	conn, err := t.SyscallConn()
	if err != nil {
		return err
	}
	var resizeErr error
	if err := conn.Control(func(fd uintptr) {
		resizeErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Row: uint16(height), Col: uint16(width)})
	}); err != nil {
		return err
	}
	return resizeErr
}

func (t *terminal) Wait(ctx context.Context) (uint32, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-t.done:
		code := t.cmd.ProcessState.ExitCode()
		if code >= 0 {
			if _, ok := errors.AsType[*exec.ExitError](t.waitErr); ok {
				return uint32(code), nil
			}
		}
		return uint32(code), t.waitErr
	}
}

func (t *terminal) Close() error {
	t.closeOnce.Do(func() {
		// Closing the master hangs up the foreground process group; kill the
		// shell itself as well so an ignored hangup cannot keep it alive.
		t.closeErr = t.File.Close()
		// Background jobs of an interactive shell sit in their own process groups
		// and nohup'ed ones ignore the hangup, so sweep the whole session.
		if err := t.killSession(t.cmd.Process.Pid); err != nil {
			t.closeErr = errors.Join(t.closeErr, err)
		}
		if err := t.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.closeErr = errors.Join(t.closeErr, err)
		}
		<-t.done
	})
	return t.closeErr
}

func openTerminal(ctx context.Context, cfg Config) (*terminal, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	command, args := cfg.Command, cfg.Args
	defaultShell := command == ""
	if defaultShell {
		command = "sh"
		// POSIX sh may be dash, which does not provide Tab completion.
		if bash, err := exec.LookPath("bash"); err == nil {
			command = bash
			args = append([]string{"--noprofile", "--norc", "-i"}, cfg.Args...)
		}
	}
	master, slave, err := pty.Open()
	if err != nil {
		return nil, err
	}
	defer slave.Close()
	// Register the PTY with Go's poller so Close interrupts blocked reads and writes.
	fd := int(master.Fd())
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = master.Close()
		return nil, err
	}
	pollFD, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
	_ = master.Close()
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(pollFD), "pty")
	c := exec.Command(command, args...)
	c.Dir = cfg.Dir
	c.Env = append(os.Environ(), "TERM="+cfg.Term)
	if defaultShell {
		// Keep the prompt on one line for echo-based command auditing.
		c.Env = append(c.Env, "PS1=$ ")
	}
	c.Env = append(c.Env, cfg.Env...) // exec.Cmd keeps the last duplicate
	c.Stdin, c.Stdout, c.Stderr = slave, slave, slave
	c.SysProcAttr = &unix.SysProcAttr{Setsid: true, Setctty: true}
	t := &terminal{File: file, cmd: c, done: make(chan struct{})}
	if err := t.Resize(cfg.Cols, cfg.Rows); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := c.Start(); err != nil {
		_ = file.Close()
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = t.Close() })
	go func() {
		t.waitErr = c.Wait()
		stop()
		close(t.done)
	}()
	return t, nil
}
