//go:build !linux && !windows

package pty

import (
	"errors"

	"golang.org/x/sys/unix"
)

// killSession kills the shell's process group. Without /proc there is no
// portable way to enumerate a session, so job-control background jobs, which
// have their own groups, are left to the shell's SIGHUP handling.
func (t *terminal) killSession(sid int) error {
	if err := unix.Kill(-sid, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
		return err
	}
	return nil
}
