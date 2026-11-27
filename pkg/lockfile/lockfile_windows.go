package lockfile

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

func lockGuard(file *os.File) error {
	var offset windows.Overlapped
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &offset)
}

// For some reason these consts don't exist in syscall.
const (
	errorInvalidParameter = 87
	codeStillActive       = 259
)

func (l Lockfile) isRunning(pid int) (bool, error) {
	procHnd, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, syscall.Errno(errorInvalidParameter)) {
			return false, nil
		}
		return false, err
	}
	defer syscall.CloseHandle(procHnd)

	var code uint32
	err = syscall.GetExitCodeProcess(procHnd, &code)
	if err != nil {
		return false, err
	}

	return code == codeStillActive, nil
}
