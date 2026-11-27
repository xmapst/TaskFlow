// Package container defines the backend interface used to run a step inside an
// isolated container instead of directly on the host. The only implementation
// is Docker, driven through the Docker Engine SDK.
//
// The package only describes WHAT must be run (Spec) and HOW a backend is
// driven (Backend). It does not know about tasks, steps or storage, so a
// microVM, a Kubernetes pod or a remote executor can be added later by
// implementing Backend without touching the runner.
package container

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
)

// Container operating systems reported by Backend.OS.
const (
	OSUnix    = "unix"
	OSWindows = "windows"
)

// Mount types. An empty Mount.Type is MountBind.
const (
	// MountBind exposes a host file or directory.
	MountBind = "bind"
	// MountVolume exposes a Docker volume by name.
	MountVolume = "volume"
	// MountTmpfs is an in-memory filesystem; it has no source.
	MountTmpfs = "tmpfs"
	// MountNpipe exposes a Windows named pipe, such as the Docker engine pipe.
	MountNpipe = "npipe"
)

// Mount describes a host path exposed inside the container.
type Mount struct {
	// Source is a host path, a volume name or a pipe name. It is empty for
	// MountTmpfs.
	Source string
	// Target is an absolute path (or pipe name) inside the container.
	Target string
	// Type is one of the Mount* constants; empty means MountBind.
	Type string
	// ReadOnly mounts the source read-only.
	ReadOnly bool
}

// TypeOrDefault returns the mount type, resolving the empty value to a bind.
func (m Mount) TypeOrDefault() string {
	if m.Type == "" {
		return MountBind
	}
	return m.Type
}

// Validate checks the invariants every backend relies on.
func (m Mount) Validate() error {
	if m.Target == "" {
		return errors.New("mount needs a target")
	}
	switch m.TypeOrDefault() {
	case MountBind:
		switch {
		case m.Source == "":
			return errors.New("bind mount needs a source")
		case !filepath.IsAbs(m.Source):
			return fmt.Errorf("bind mount source %q must be an absolute host path", m.Source)
		}
	case MountVolume, MountNpipe:
		if m.Source == "" {
			return fmt.Errorf("%s mount needs a source", m.TypeOrDefault())
		}
	case MountTmpfs:
		if m.Source != "" {
			return errors.New("tmpfs mount does not take a source")
		}
	default:
		return fmt.Errorf("unsupported mount type %q", m.Type)
	}
	return nil
}

// Spec is a complete, backend-independent description of one container run.
type Spec struct {
	// Name is a unique, runtime-safe container name. Backends use it to find
	// and kill the container, so callers must make it unique per attempt.
	Name string
	// Image is the OCI image reference, e.g. "alpine:3.20".
	Image string
	// Command is the program and its arguments (the image ENTRYPOINT is
	// replaced). It must not be empty.
	Command []string
	// Workdir is the working directory inside the container.
	Workdir string
	// Env holds KEY=VALUE entries.
	Env []string
	// Mounts lists the bind mounts; the task workdir is always one of them.
	Mounts []Mount
	// User is the "uid[:gid]" (Linux) or account name (Windows) the process runs
	// as; empty uses the image default.
	User string
	// OS is the container OS (OSLinux or OSWindows; empty means OSLinux). Linux
	// only options (capabilities, no-new-privileges, pids limit) are skipped
	// for Windows containers, which do not have them.
	OS     string
	Stdout io.Writer
	Stderr io.Writer
}

// Result is the outcome of a finished container.
type Result struct {
	// ExitCode is the process exit code. It is only meaningful when
	// Started is true and the run was not killed.
	ExitCode int
	// OOMKilled reports that the runtime killed the process for memory.
	OOMKilled bool
}

// Backend runs containers. Implementations must be safe for concurrent use.
type Backend interface {
	// Name identifies the backend ("docker", ...), for logs.
	Name() string
	// Available reports whether the runtime can be used right now, with a
	// human-readable reason when it cannot. It must be cheap enough to call
	// before every run.
	Available(ctx context.Context) error
	// OS reports which kind of containers the runtime runs right now: OSLinux
	// or OSWindows. Docker Desktop on Windows can switch between the two.
	OS(ctx context.Context) (string, error)
	// Run starts the container and blocks until it exits or ctx is done.
	// When ctx is done the backend must stop and remove the container before
	// returning, and return ctx's error.
	// The container is always removed before Run returns.
	Run(ctx context.Context, spec Spec) (Result, error)
	// Kill force-stops a running container by Spec.Name. It is idempotent: a
	// container that is already gone is not an error.
	Kill(ctx context.Context, name string) error
}

// ErrUnavailable wraps the reason a backend cannot be used.
var ErrUnavailable = errors.New("container backend unavailable")

// Validate checks the backend-independent invariants of a Spec.
func (s Spec) Validate() error {
	switch {
	case s.Name == "":
		return errors.New("container name is empty")
	case s.Image == "":
		return errors.New("container image is empty")
	case len(s.Command) == 0:
		return errors.New("container command is empty")
	case s.OS != "" && s.OS != OSUnix && s.OS != OSWindows:
		return fmt.Errorf("unsupported container os %q", s.OS)
	}
	for _, m := range s.Mounts {
		if err := m.Validate(); err != nil {
			return fmt.Errorf("container mount %q: %w", m.Target, err)
		}
	}
	return nil
}
