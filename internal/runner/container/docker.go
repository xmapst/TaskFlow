package container

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	ctr "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
)

const (
	// pidsLimit caps the processes of a Linux container (fork bomb guard).
	pidsLimit = 150
	// cleanupTimeout bounds the removal of a container after its run ended or
	// was cancelled; it must outlive the cancelled run context.
	cleanupTimeout = 30 * time.Second
	// drainTimeout bounds how long output is read after the container exited.
	drainTimeout = 10 * time.Second
)

// Docker drives the local Docker Engine through the official SDK
// (github.com/moby/moby/client) instead of the `docker` command line.
//
// It talks to the daemon over its local endpoint: the unix socket
// /var/run/docker.sock on Unix and the named pipe //./pipe/docker_engine on
// Windows. DOCKER_HOST (and the other standard DOCKER_* variables) are honoured,
// so rootless Docker, Docker Desktop contexts and custom sockets work too.
type Docker struct {
	mu     sync.Mutex
	client *client.Client
}

// NewDocker returns a backend for the local Docker Engine. No connection is
// made until the first call.
func NewDocker() *Docker {
	return &Docker{}
}

func (d *Docker) Name() string {
	return "docker"
}

// api returns the shared SDK client, creating it on first use. Creating a
// client does not connect, so a daemon that starts later is picked up.
func (d *Docker) api() (*client.Client, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.client != nil {
		return d.client, nil
	}
	c, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("%w: docker client: %v", ErrUnavailable, err)
	}
	d.client = c
	return c, nil
}

func (d *Docker) Available(ctx context.Context) error {
	c, err := d.api()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	v, err := c.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return fmt.Errorf("%w: docker: %v", ErrUnavailable, err)
	}
	if strings.TrimSpace(v.Version) == "" {
		return fmt.Errorf("%w: docker reported no server version", ErrUnavailable)
	}
	return nil
}

// OS asks the daemon which container OS it is running. Docker Desktop on
// Windows can switch between Linux and Windows containers at any time.
func (d *Docker) OS(ctx context.Context) (string, error) {
	c, err := d.api()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := c.Info(ctx, client.InfoOptions{})
	if err != nil {
		return "", fmt.Errorf("%w: docker info: %v", ErrUnavailable, err)
	}
	// The daemon reports "linux" or "windows"; Linux containers are the Unix
	// kind as far as the runner is concerned.
	switch os := strings.ToLower(strings.TrimSpace(res.Info.OSType)); os {
	case "linux":
		return OSUnix, nil
	case OSWindows:
		return OSWindows, nil
	default:
		return "", fmt.Errorf("%w: docker reported unsupported container os %q", ErrUnavailable, os)
	}
}

func (d *Docker) Run(ctx context.Context, spec Spec) (res Result, err error) {
	if err = spec.Validate(); err != nil {
		return Result{}, err
	}
	for _, e := range spec.Env {
		if !strings.Contains(e, "=") {
			return Result{}, fmt.Errorf("invalid environment entry %q", e)
		}
	}
	c, err := d.api()
	if err != nil {
		return Result{}, err
	}
	if err = d.ensureImage(ctx, c, spec); err != nil {
		return Result{}, d.ctxErr(ctx, err)
	}

	// The container is removed explicitly (not with AutoRemove) so its state
	// can still be inspected for an OOM kill after it has exited. It is also
	// what stops a run whose context was cancelled.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		_ = d.Kill(cleanup, spec.Name)
	}()

	created, err := c.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name:       spec.Name,
		Config:     d.containerConfig(spec),
		HostConfig: d.hostConfig(spec),
	})
	if err != nil {
		return Result{}, d.ctxErr(ctx, fmt.Errorf("docker create: %w", err))
	}

	// Wait and attach are registered before the container starts so neither
	// the exit status nor the first bytes of output can be missed.
	waitCtx, cancelWait := context.WithCancel(ctx)
	defer cancelWait()
	wait := c.ContainerWait(waitCtx, created.ID, client.ContainerWaitOptions{Condition: ctr.WaitConditionNextExit})
	attached, err := c.ContainerAttach(ctx, created.ID, client.ContainerAttachOptions{Stream: true, Stdout: true, Stderr: true})
	if err != nil {
		return Result{}, d.ctxErr(ctx, fmt.Errorf("docker attach: %w", err))
	}
	defer attached.Close()

	copied := make(chan error, 1)
	go func() {
		// Steps run without a TTY, so the attach stream is multiplexed and has a
		// type byte per frame. A raw stream would mean the container was created
		// with one anyway; copying it verbatim beats logging nothing.
		if mt, ok := attached.MediaType(); ok && strings.HasSuffix(mt, "raw-stream") {
			_, copyErr := io.Copy(spec.Stdout, attached.Reader)
			copied <- copyErr
			return
		}
		_, copyErr := stdcopy.StdCopy(spec.Stdout, spec.Stderr, attached.Reader)
		copied <- copyErr
	}()

	if _, err = c.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return Result{}, d.ctxErr(ctx, fmt.Errorf("docker start: %w", err))
	}

	var exit ctr.WaitResponse
	select {
	case exit = <-wait.Result:
	case err = <-wait.Error:
		return Result{}, d.ctxErr(ctx, fmt.Errorf("docker wait: %w", err))
	case <-ctx.Done():
		return Result{}, context.Cause(ctx)
	}
	if exit.Error != nil && exit.Error.Message != "" {
		return Result{}, fmt.Errorf("docker run: %s", exit.Error.Message)
	}

	// The output stream ends by itself once the container has exited; bound the
	// wait so a stuck connection cannot hold the step forever.
	select {
	case copyErr := <-copied:
		// A dead stream leaves the log truncated but the exit status is still
		// valid, so say so rather than report the whole step as failed.
		if copyErr != nil {
			_, _ = fmt.Fprintf(spec.Stderr, "warning: reading container output: %v\n", copyErr)
		}
	case <-time.After(drainTimeout):
	case <-ctx.Done():
		return Result{}, context.Cause(ctx)
	}

	res.ExitCode = int(exit.StatusCode)
	inspect, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if info, ierr := c.ContainerInspect(inspect, created.ID, client.ContainerInspectOptions{}); ierr == nil && info.Container.State != nil {
		res.OOMKilled = info.Container.State.OOMKilled
	}
	return res, nil
}

// Kill force-removes a container by name; a container that is already gone is
// not an error.
func (d *Docker) Kill(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("container name is empty")
	}
	c, err := d.api()
	if err != nil {
		return err
	}
	_, err = c.ContainerRemove(ctx, name, client.ContainerRemoveOptions{Force: true})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove container %s: %w", name, err)
	}
	return nil
}

// ensureImage pulls the image when it is not present locally, writing the pull
// progress to the step's stdout, one line per layer state change.
//
// The credentials come from the host's docker config, the same one the CLI
// reads: the daemon itself keeps none, so a pull without this header would be
// anonymous and every private image would fail.
func (d *Docker) ensureImage(ctx context.Context, c *client.Client, spec Spec) error {
	if _, err := c.ImageInspect(ctx, spec.Image); err == nil {
		return nil
	} else if !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("docker inspect image %s: %w", spec.Image, err)
	}
	authHeader, source, err := registryAuth(ctx, spec.Image)
	if err != nil {
		return err
	}
	if source != "" {
		_, _ = fmt.Fprintf(spec.Stdout, "pulling image %s (credentials: %s)\n", spec.Image, source)
	} else {
		_, _ = fmt.Fprintf(spec.Stdout, "pulling image %s (no credentials configured)\n", spec.Image)
	}
	pull, err := c.ImagePull(ctx, spec.Image, client.ImagePullOptions{RegistryAuth: authHeader})
	if err != nil {
		return pullError(spec.Image, source, err)
	}
	defer pull.Close()
	last := map[string]string{}
	for msg, err := range pull.JSONMessages(ctx) {
		if err != nil {
			return pullError(spec.Image, source, err)
		}
		if msg.Error != nil {
			return pullError(spec.Image, source, errors.New(msg.Error.Message))
		}
		// Progress ticks repeat the same status; log it only when it changes.
		if msg.Status == "" || last[msg.ID] == msg.Status {
			continue
		}
		last[msg.ID] = msg.Status
		if msg.ID != "" {
			_, _ = fmt.Fprintf(spec.Stdout, "%s: %s\n", msg.ID, msg.Status)
		} else {
			_, _ = fmt.Fprintln(spec.Stdout, msg.Status)
		}
	}
	return nil
}

// pullError adds the one hint that matters when a pull is refused: nothing was
// configured, so the daemon asked anonymously and the registry said no.
func pullError(image, source string, err error) error {
	if source == "" && isAuthError(err) {
		return fmt.Errorf("docker pull %s: %w (no credentials for this registry; run `docker login` on the host or set DOCKER_AUTH_CONFIG)", image, err)
	}
	return fmt.Errorf("docker pull %s: %w", image, err)
}

func isAuthError(err error) bool {
	if cerrdefs.IsUnauthorized(err) || cerrdefs.IsPermissionDenied(err) {
		return true
	}
	// The daemon reports a registry refusal inside the pull stream as text, so
	// the status code is gone by the time it reaches us.
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"unauthorized", "authentication required", "no basic auth", "denied"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func (d *Docker) containerConfig(spec Spec) *ctr.Config {
	return &ctr.Config{
		Image:        spec.Image,
		User:         spec.User,
		WorkingDir:   spec.Workdir,
		Env:          spec.Env,
		Entrypoint:   []string{spec.Command[0]},
		Cmd:          spec.Command[1:],
		AttachStdout: true,
		AttachStderr: true,
	}
}

func (d *Docker) hostConfig(spec Spec) *ctr.HostConfig {
	hc := &ctr.HostConfig{}
	for _, m := range spec.Mounts {
		hc.Mounts = append(hc.Mounts, mount.Mount{
			Type:     mount.Type(m.TypeOrDefault()),
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}
	// no-new-privileges and the pids cgroup are Linux concepts; a Windows
	// daemon rejects or ignores them.
	//
	// Capabilities are deliberately left at Docker's default set instead of
	// being dropped: steps run as root and routinely install packages, and
	// apt/dpkg fail without SETUID, SETGID and CHOWN (it drops to the _apt
	// user, and ping needs NET_RAW). The default set still excludes the
	// dangerous ones such as SYS_ADMIN and NET_ADMIN.
	if spec.OS != OSWindows {
		hc.SecurityOpt = []string{"no-new-privileges"}
		hc.PidsLimit = new(int64(pidsLimit))
	}
	return hc
}

// ctxErr reports the cancellation cause instead of the SDK error it triggered.
func (d *Docker) ctxErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return err
}
