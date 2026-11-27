//go:build darwin || dragonfly || freebsd || linux || nacl || netbsd || openbsd || solaris || aix

package lockfile

import (
	"os"
	"syscall"
)

func (l Lockfile) isRunning(pid int) (bool, error) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, err
	}

	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false, nil //nolint:nilerr // 信号发不出去就是进程已经不在了，这是答案而不是错误
	}

	return true, nil
}
