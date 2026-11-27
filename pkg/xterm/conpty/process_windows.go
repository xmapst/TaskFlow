package conpty

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Command describes a process to attach to a Terminal. Args excludes the
// executable name. Nil Env inherits the parent environment; an explicit Env
// replaces it, preserving SystemRoot when not provided. Dir sets the child CWD.
type Command struct {
	Path string
	Args []string
	Env  []string
	Dir  string
}

// Process owns the child process, independently of the terminal I/O handles.
// The result is collected even if the caller never calls Wait.
type Process struct {
	process *os.Process
	job     *job // nil if the platform refused a Job Object
	done    chan struct{}
	state   *os.ProcessState
	err     error
}

func (p *Process) PID() int { return p.process.Pid }

// Kill terminates the child and, when a Job Object could be created, all of its
// descendants. It does not replace closing the terminal to release the host
// pipe handles.
func (p *Process) Kill() error {
	if p.job != nil {
		if ok, err := p.job.terminate(); ok {
			return err
		}
	}
	return p.process.Kill()
}

// Wait returns the same result to all callers. A nonzero exit is reported as an
// *exec.ExitError. Wait does not wait for the terminal output to be drained.
func (p *Process) Wait() (*os.ProcessState, error) {
	<-p.done
	return p.state, p.err
}

// WaitContext bounds the wait without affecting the child. To cancel the
// child and close its terminal, cancel the context passed to Start instead.
func (p *Process) WaitContext(ctx context.Context) (*os.ProcessState, error) {
	select {
	case <-p.done:
		return p.state, p.err
	default:
	}
	select {
	case <-p.done:
		return p.state, p.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Start resolves the executable and attaches the command to the terminal.
// Canceling ctx while the child is running kills it and closes the terminal.
func (t *Terminal) Start(ctx context.Context, command Command) (*Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if command.Path == "" || (filepath.VolumeName(command.Path) != "" && !filepath.IsAbs(command.Path)) {
		return nil, fmt.Errorf("invalid executable path %q: %w", command.Path, os.ErrInvalid)
	}
	path, err := exec.LookPath(command.Path)
	if err != nil {
		return nil, err
	}
	// Resolve against the parent before CreateProcess applies the child's Dir.
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	argv := append([]string{path}, command.Args...)
	return t.start(ctx, path, windows.ComposeCommandLine(argv), command.Dir, command.Env)
}

// StartCommandLine preserves the native Windows command-line syntax for callers
// that already have a complete command line. Prefer Start for separate arguments.
func (t *Terminal) StartCommandLine(ctx context.Context, line string) (*Process, error) {
	return t.start(ctx, "", line, "", nil)
}

func (t *Terminal) start(ctx context.Context, path, line, dir string, env []string) (*Process, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if t.closed {
		return nil, os.ErrClosed
	}
	if t.started || line == "" {
		return nil, os.ErrInvalid
	}
	process, j, err := t.createProcess(path, line, dir, env)
	if err != nil {
		return nil, fmt.Errorf("start ConPTY process: %w", err)
	}
	t.started = true
	p := &Process{process: process, job: j, done: make(chan struct{})}
	stopCancel := context.AfterFunc(ctx, func() {
		if p.Kill() == nil {
			// Reap before terminal teardown can make the child exit via EOF.
			<-p.done
		}
		_ = t.Close()
	})
	go func() {
		p.state, p.err = process.Wait()
		if p.err == nil && !p.state.Success() {
			p.err = &exec.ExitError{ProcessState: p.state}
		}
		stopCancel()
		if p.job != nil {
			p.job.close() // kills descendants that outlived the shell
		}
		close(p.done)
		// Flush the console after publishing the exit result. Close will drain
		// the pipe if the caller has stopped consuming output.
		t.finish()
	}()
	return p, nil
}

func (t *Terminal) createProcess(path, line, dir string, env []string) (*os.Process, *job, error) {
	var application, directory, environment *uint16
	var err error
	if path != "" {
		application, err = windows.UTF16PtrFromString(path)
		if err != nil {
			return nil, nil, err
		}
	}
	commandLine, err := windows.UTF16PtrFromString(line)
	if err != nil {
		return nil, nil, err
	}
	if dir != "" {
		directory, err = windows.UTF16PtrFromString(dir)
		if err != nil {
			return nil, nil, err
		}
	}
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_SUSPENDED)
	if env != nil {
		block, err := environmentBlock(withSystemRoot(env))
		if err != nil {
			return nil, nil, err
		}
		environment = &block[0]
		flags |= windows.CREATE_UNICODE_ENVIRONMENT
	}
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, nil, err
	}
	defer attrs.Delete()
	// lpValue is the HPCON value itself, not the address of a HANDLE.
	result, _, err := updateProcThreadAttribute.Call(uintptr(unsafe.Pointer(attrs.List())), 0,
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, uintptr(t.console), unsafe.Sizeof(t.console), 0, 0)
	if result == 0 {
		return nil, nil, os.NewSyscallError("UpdateProcThreadAttribute", err)
	}
	startup := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	// Explicit null standard handles let ConPTY supply its own. Without the
	// flag, Windows can copy redirected parent handles even with inherit=false.
	startup.Flags = windows.STARTF_USESTDHANDLES
	var info windows.ProcessInformation
	if err := windows.CreateProcess(application, commandLine, nil, nil, false, flags, environment, directory, &startup.StartupInfo, &info); err != nil {
		return nil, nil, err
	}
	defer windows.CloseHandle(info.Process)
	defer windows.CloseHandle(info.Thread)

	// Join the job while the child is still suspended so every descendant it
	// spawns later is inside it. Failure (e.g. a non-nestable parent job) only
	// loses descendant cleanup.
	jb := newJob()
	if jb != nil && jb.assign(info.Process) != nil {
		jb.close()
		jb = nil
	}

	// Adopt the child before it runs so even an immediate exit has a valid handle.
	process, err := os.FindProcess(int(info.ProcessId))
	if err != nil {
		_ = windows.TerminateProcess(info.Process, 1)
		if jb != nil {
			jb.close()
		}
		return nil, nil, err
	}
	if _, err := windows.ResumeThread(info.Thread); err != nil {
		if windows.TerminateProcess(info.Process, 1) == nil {
			_, _ = process.Wait()
		} else {
			_ = process.Release()
		}
		if jb != nil {
			jb.close()
		}
		return nil, nil, err
	}
	return process, jb, nil
}

func withSystemRoot(env []string) []string {
	for _, entry := range env {
		if key, _, ok := strings.Cut(entry, "="); ok && strings.EqualFold(key, "SystemRoot") {
			return env
		}
	}
	return append(append([]string(nil), env...), "SystemRoot="+os.Getenv("SystemRoot"))
}
