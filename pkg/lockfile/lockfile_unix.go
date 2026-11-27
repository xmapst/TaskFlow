//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package lockfile

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func lockGuard(file *os.File) error {
	lock := unix.Flock_t{Type: unix.F_WRLCK, Whence: 0, Len: 1}
	for {
		err := unix.FcntlFlock(file.Fd(), unix.F_SETLKW, &lock)
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}

func (l Lockfile) isRunning(pid int) (bool, error) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, err
	}
	defer proc.Release()

	err = proc.Signal(syscall.Signal(0))
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return true, nil
	case errors.Is(err, os.ErrProcessDone), errors.Is(err, syscall.ESRCH):
		return false, nil
	default:
		return false, err
	}
}
