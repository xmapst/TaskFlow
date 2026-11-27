//go:build !windows

package pty

import "golang.org/x/sys/unix"

// ChildInForeground corroborates the VT mode with the kernel's job-control
// state. A printed alternate-screen escape alone must not disable shell audit.
func (t *terminal) ChildInForeground() bool {
	conn, err := t.SyscallConn()
	if err != nil {
		return false
	}
	var group int
	var ioctlErr error
	err = conn.Control(func(fd uintptr) {
		group, ioctlErr = unix.IoctlGetInt(int(fd), unix.TIOCGPGRP)
	})
	return err == nil && ioctlErr == nil && group > 0 && group != t.cmd.Process.Pid
}
