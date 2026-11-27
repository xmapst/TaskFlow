// Package lockfile handles pid file based locking.
// While a sync.Mutex helps against concurrency issues within a single process,
// this package is designed to help against concurrency issues between cooperating processes
// or serializing multiple invocations of the same process.
package lockfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Lockfile is a pid file which can be locked
type Lockfile string

// TemporaryError is a type of error where a retry after a random amount of sleep should help to mitigate it.
type TemporaryError string

func (t TemporaryError) Error() string { return string(t) }

// Temporary returns always true.
// It exists, so you can detect it via
//
//	if te, ok := err.(interface{ Temporary() bool }); ok {
//		fmt.Println("I am a temporary error situation, so wait and retry")
//	}
func (t TemporaryError) Temporary() bool { return true }

// Various errors returned by this package
var (
	ErrBusy          = TemporaryError("Locked by other process")             // If you get this, retry after a short sleep might help
	ErrNotExist      = TemporaryError("Lockfile created, but doesn't exist") // If you get this, retry after a short sleep might help
	ErrNeedAbsPath   = errors.New("lockfiles must be given as absolute path names")
	ErrInvalidPid    = errors.New("lockfile contains invalid pid for system")
	ErrDeadOwner     = errors.New("lockfile contains pid of process not existent on this system anymore")
	ErrRogueDeletion = errors.New("lockfile owned by me has been removed unexpectedly")
)

// New describes a new filename located at the given absolute path.
// The package reserves a sibling file with the suffix ".guard" for coordinating
// changes to the PID file. Do not remove or replace that guard file.
func New(path string) (Lockfile, error) {
	if !filepath.IsAbs(path) {
		return "", ErrNeedAbsPath
	}
	_ = os.MkdirAll(filepath.Dir(path), os.ModeDir)

	return Lockfile(path), nil
}

// GetOwner returns who owns the lockfile.
func (l Lockfile) GetOwner() (*os.Process, error) {
	name := string(l)

	// Ok, see, if we have a stale lockfile here
	content, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}

	// try hard for pids. If no pid, the lockfile is junk anyway and we delete it.
	pid, err := l.scanPidLine(content)
	if err != nil {
		return nil, err
	}

	running, err := l.isRunning(pid)
	if err != nil {
		return nil, err
	}

	if running {
		proc, err := os.FindProcess(pid)
		if err != nil {
			return nil, err
		}

		return proc, nil
	}

	return nil, ErrDeadOwner
}

// TryLock tries to own the lock.
// It Returns nil, if successful and and error describing the reason, it didn't work out.
// Please note, that existing lockfiles containing pids of dead processes
// and lockfiles containing no pid at all are simply deleted.
func (l Lockfile) TryLock() error {
	if !filepath.IsAbs(string(l)) {
		panic(ErrNeedAbsPath)
	}
	return l.withGuard(l.tryLock)
}

// POSIX record locks are per-process. Keep goroutines from independently
// opening or closing the same guard while one goroutine holds its lock.
var guardMu sync.Mutex

func (l Lockfile) withGuard(fn func() error) error {
	guardMu.Lock()
	defer guardMu.Unlock()

	// Never unlink the guard: waiters may already have it open. The kernel
	// releases the lock when the descriptor closes or the process dies.
	path := string(l) + ".guard"
	guard, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer guard.Close()
	if err := lockGuard(guard); err != nil {
		return fmt.Errorf("lock PID file guard %q: %w", path, err)
	}
	return fn()
}

func (l Lockfile) tryLock() error {
	name := string(l)

	tmplock, cleanup, err := l.makePidFile(name, os.Getpid())
	if err != nil {
		return err
	}

	defer cleanup()

	// EEXIST and similar error codes, caught by os.IsExist, are intentionally ignored,
	// as it means that someone was faster creating this link
	// and ignoring this kind of error is part of the algorithm.
	// Then we will probably fail the pid owner check later, if this process is still alive.
	// We cannot ignore ALL errors, since failure to support hard links, disk full
	// as well as many other errors can happen to a filesystem operation
	// and we really want to abort on those.
	if err = os.Link(tmplock, name); err != nil {
		if !os.IsExist(err) {
			return err
		}
	}

	fiTmp, err := os.Lstat(tmplock)
	if err != nil {
		return err
	}

	fiLock, err := os.Lstat(name)
	if err != nil {
		// tell user that a retry would be a good idea
		if os.IsNotExist(err) {
			return ErrNotExist
		}

		return err
	}

	// Success
	if os.SameFile(fiTmp, fiLock) {
		return nil
	}

	owner, err := l.GetOwner()
	if owner != nil {
		_ = owner.Release()
	}
	switch {
	default:
		if os.IsNotExist(err) {
			// tell user that a retry would be a good idea
			return ErrNotExist
		}
		// Other errors -> defensively fail and let caller handle this
		return err
	case err == nil:
		// This fork differs from the upstream repository in this line. We do
		// not want a process to obtain a lock if this lock is already help by
		// the same process. Therefore, we always return ErrBusy if the lockfile
		// contains a non-dead PID.
		return ErrBusy
	case errors.Is(err, ErrDeadOwner), errors.Is(err, ErrInvalidPid):
		// cases we can fix below
	}

	// clean stale/invalid lockfile
	err = os.Remove(name)
	if err != nil {
		// If it doesn't exist, then it doesn't matter who removed it.
		if !os.IsNotExist(err) {
			return err
		}
	}

	// Retry without reacquiring the guard; no other participant can replace
	// the stale PID file between the owner check, removal and retry.
	return l.tryLock()
}

// Unlock a lock again, if we owned it. Returns any error that happened during release of lock.
func (l Lockfile) Unlock() error {
	return l.withGuard(l.unlock)
}

func (l Lockfile) unlock() error {
	proc, err := l.GetOwner()
	if proc != nil {
		defer proc.Release()
	}
	switch {
	case errors.Is(err, ErrInvalidPid), errors.Is(err, ErrDeadOwner):
		return ErrRogueDeletion
	case err == nil:
		if proc.Pid == os.Getpid() {
			// we really own it, so let's remove it.
			return os.Remove(string(l))
		}
		// Not owned by me, so don't delete it.
		return ErrRogueDeletion
	default:
		// This is an application error or system error.
		// So give a better error for logging here.
		if os.IsNotExist(err) {
			return ErrRogueDeletion
		}
		// Other errors -> defensively fail and let caller handle this
		return err
	}
}

func (l Lockfile) scanPidLine(content []byte) (int, error) {
	if len(content) == 0 {
		return 0, ErrInvalidPid
	}

	var pid int
	if _, err := fmt.Sscanln(string(content), &pid); err != nil {
		return 0, ErrInvalidPid
	}

	if pid <= 0 {
		return 0, ErrInvalidPid
	}

	return pid, nil
}

func (l Lockfile) makePidFile(name string, pid int) (tmpname string, cleanup func(), err error) {
	tmplock, err := os.CreateTemp(filepath.Dir(name), filepath.Base(name)+".")
	if err != nil {
		return "", nil, err
	}

	cleanup = func() {
		_ = tmplock.Close()
		_ = os.Remove(tmplock.Name())
	}

	if _, err = io.WriteString(tmplock, fmt.Sprintf("%d\n", pid)); err != nil {
		cleanup() // Do cleanup here, so call doesn't have to.
		return "", nil, err
	}

	return tmplock.Name(), cleanup, nil
}
