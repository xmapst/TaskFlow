package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"uuid"

	"github.com/tidwall/gjson"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/internal/runner/container"
	"github.com/xmapst/taskflow/internal/storage"
)

// dockerBackend is shared by all container steps so they reuse one SDK client.
var dockerBackend container.Backend = container.NewDocker()

// containerLayout is where the task workdir, the script directory and the home
// directory appear inside the container; it depends on the container OS, not on
// the host OS.
type containerLayout struct {
	workdir   string
	scriptDir string
	home      string
	sep       string
	// user is the account used for the process; empty keeps the image default.
	user string
}

func (c *sContainerRunner) layoutFor(containerOS string) containerLayout {
	if containerOS == container.OSWindows {
		// ContainerAdministrator exists in both nanoserver and servercore and
		// is allowed to write to bind mounts, unlike the default ContainerUser.
		return containerLayout{
			workdir:   `C:\workspace`,
			scriptDir: `C:\taskflow`,
			home:      `C:\Users\ContainerAdministrator`,
			sep:       `\`,
			user:      "ContainerAdministrator",
		}
	}
	return containerLayout{workdir: "/workspace", scriptDir: "/taskflow", home: "/root", sep: "/", user: "0:0"}
}

// sContainerRunner runs one step in a container created by a container.Backend.
//
// The step kind is "container" and options carry the settings: image says which
// image to run and shell sets the interpreter, which defaults to sh (cmd for
// Windows containers) and may carry arguments. The container is run by the local
// Docker Engine through its SDK (unix socket, or named pipe on Windows). The step
// content is the script: it is written to its own directory under the script
// directory, and that directory (not a single file, which Windows containers
// cannot mount) is mounted read-only into the container and run by the
// interpreter.
type sContainerRunner struct {
	mu        sync.Mutex
	cleared   bool
	cancel    context.CancelFunc
	storage   *storage.StepStore
	backend   container.Backend
	workspace string
	image     string
	shell     []string
	mounts    []container.Mount
	content   string
	// runDir is the host directory holding this step's script.
	runDir string
}

// parseContainerKind checks that the step is run by this executor. The image is
// not part of the kind: the kind names the executor, options configure it.
func (c *sContainerRunner) parseContainerKind(kind string) error {
	kind = strings.TrimSpace(kind)
	if strings.Contains(kind, "@") {
		return fmt.Errorf("container image belongs in options.image, not in the step kind: use kind \"container\", got %q", kind)
	}
	if !strings.EqualFold(kind, "container") {
		return fmt.Errorf("container runner requires kind \"container\", got %q", kind)
	}
	return nil
}

// imageFrom reads the image from the step's options. It is required: without one
// there is nothing to run.
func (c *sContainerRunner) imageFrom(opts gjson.Result) error {
	image, err := optionString(opts, "image")
	if err != nil {
		return err
	}
	if image == "" {
		return errors.New("container step requires options.image")
	}
	if strings.ContainsAny(image, " \t\r\n") {
		return fmt.Errorf("invalid container image %q", image)
	}
	c.image = image
	return nil
}

// shellFrom reads the interpreter from the step's shell option. Nothing is
// resolved here: prepare picks sh or cmd once the container OS is known.
func (c *sContainerRunner) shellFrom(opts gjson.Result) error {
	argv, err := optionCommand(opts, "shell")
	if err != nil {
		return err
	}
	if len(argv) == 0 {
		return nil
	}
	if strings.HasPrefix(argv[0], "-") {
		return fmt.Errorf("invalid container shell %q: the first word must be the program", argv[0])
	}
	c.shell = argv
	return nil
}

// shellProgram is the interpreter's program name, or empty before prepare has
// picked the default.
func (c *sContainerRunner) shellProgram() string {
	if len(c.shell) == 0 {
		return ""
	}
	return c.shell[0]
}

// containerShellKind classifies the interpreter from the step's shell option.
// It never depends on the host OS.
func (c *sContainerRunner) containerShellKind() string {
	program := strings.TrimSuffix(filepath.Base(c.shellProgram()), ".exe")
	switch strings.ToLower(program) {
	case "python", "python2", "python3", "py", "py2", "py3":
		return "python"
	case "cmd", "bat":
		return "cmd"
	case "powershell", "pwsh", "ps", "ps1":
		return "powershell"
	}
	return "sh"
}

func (c *sContainerRunner) containerScriptSuffix() string {
	switch c.containerShellKind() {
	case "python":
		return ".py"
	case "cmd":
		return ".bat"
	case "powershell":
		return ".ps1"
	}
	return ".sh"
}

// containerCommand returns the argv that makes the interpreter run the mounted
// script. Arguments the step configured are kept ahead of the script.
func (c *sContainerRunner) containerCommand(script string) []string {
	argv := slices.Clone(c.shell)
	switch c.containerShellKind() {
	case "cmd":
		return append(argv, "/c", script)
	case "powershell":
		if len(argv) == 1 {
			argv = append(argv, "-NoProfile", "-NonInteractive")
		}
		return append(argv, "-File", script)
	}
	// sh, bash, python and friends take the script as their last argument.
	return append(argv, script)
}

func newContainerRunner(stg *storage.StepStore, workspace, scriptDir string) (IRunner, error) {
	var runner = &sContainerRunner{
		storage: stg,
		backend: dockerBackend,
	}
	kind, err := stg.Kind()
	if err != nil {
		return nil, err
	}
	if err = runner.parseContainerKind(kind); err != nil {
		return nil, err
	}
	opts, err := stepOptions(stg, "container", "image", "shell", "mounts")
	if err != nil {
		return nil, err
	}
	if err = runner.imageFrom(opts); err != nil {
		return nil, err
	}
	if err = runner.shellFrom(opts); err != nil {
		return nil, err
	}
	if err = runner.mountsFrom(opts); err != nil {
		return nil, err
	}
	content, err := stg.Content()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(content) == "" {
		return nil, errors.New("container step script is empty")
	}
	runner.content = content
	if runner.workspace, err = filepath.Abs(workspace); err != nil {
		return nil, err
	}
	absScriptDir, err := filepath.Abs(scriptDir)
	if err != nil {
		return nil, err
	}
	runner.runDir = filepath.Join(absScriptDir, uuid.NewV7().String())
	return runner, nil
}

// prepare resolves everything that depends on the container OS and writes the
// script. It runs after the OS is known, i.e. when the step actually starts.
func (c *sContainerRunner) prepare(containerOS string, env func(workdir string) []string) (container.Spec, error) {
	lay := c.layoutFor(containerOS)
	if len(c.shell) == 0 {
		c.shell = []string{"sh"}
		if containerOS == container.OSWindows {
			c.shell = []string{"cmd"}
		}
	}
	content := c.content
	// Unix interpreters choke on CRLF (a Windows-edited script or a Windows
	// client); cmd and PowerShell are happy with either, so they are left as is.
	if k := c.containerShellKind(); k == "sh" || k == "python" {
		content = strings.ReplaceAll(content, "\r\n", "\n")
	}
	if err := os.MkdirAll(c.runDir, os.ModePerm); err != nil {
		return container.Spec{}, err
	}
	file := "script" + c.containerScriptSuffix()
	if err := os.WriteFile(filepath.Join(c.runDir, file), []byte(content), 0o755); err != nil {
		return container.Spec{}, err
	}
	mounts := []container.Mount{
		{Source: c.workspace, Target: lay.workdir},
		{Source: c.runDir, Target: lay.scriptDir},
	}
	mounts = append(mounts, c.mounts...)
	envs := env(lay.workdir)
	return container.Spec{
		Image:   c.image,
		Command: c.containerCommand(lay.scriptDir + lay.sep + file),
		Workdir: lay.workdir,
		Env:     envs,
		Mounts:  mounts,
		User:    lay.user,
		OS:      containerOS,
	}, nil
}

// mountsFrom reads options.mounts: extra paths the step needs, such as the
// Docker socket, a cache directory or a config file. The workdir and the script
// directory are mounted by the runner itself, so their targets are reserved.
func (c *sContainerRunner) mountsFrom(opts gjson.Result) error {
	value := opts.Get("mounts")
	if !value.Exists() || value.Type == gjson.Null {
		return nil
	}
	if !value.IsArray() {
		return fmt.Errorf("option \"mounts\" must be a list, got %s", value.Type)
	}
	reserved := c.reservedTargets()
	mounts := make([]container.Mount, 0, len(value.Array()))
	seen := make(map[string]bool, cap(mounts))
	for i, item := range value.Array() {
		m, err := c.parseMount(i, item)
		if err != nil {
			return err
		}
		if reserved[m.Target] {
			return fmt.Errorf("option \"mounts\"[%d]: target %q is reserved for the task workdir or the step script", i, m.Target)
		}
		if seen[m.Target] {
			return fmt.Errorf("option \"mounts\"[%d]: target %q is mounted twice", i, m.Target)
		}
		seen[m.Target] = true
		mounts = append(mounts, m)
	}
	c.mounts = mounts
	return nil
}

// reservedTargets lists the mount targets the runner owns, for both container
// systems, so a step never has to know which one it will run on.
func (c *sContainerRunner) reservedTargets() map[string]bool {
	unix := c.layoutFor(container.OSUnix)
	windows := c.layoutFor(container.OSWindows)
	return map[string]bool{
		unix.workdir:      true,
		unix.scriptDir:    true,
		windows.workdir:   true,
		windows.scriptDir: true,
	}
}

// parseMount reads one entry of options.mounts. Unknown fields are rejected for
// the same reason unknown options are: a typo must not silently mount the wrong
// thing.
func (c *sContainerRunner) parseMount(index int, item gjson.Result) (container.Mount, error) {
	if !item.IsObject() {
		return container.Mount{}, fmt.Errorf("option \"mounts\"[%d] must be an object, got %s", index, item.Type)
	}
	var unknown []string
	item.ForEach(func(key, _ gjson.Result) bool {
		switch key.String() {
		case "source", "target", "type", "readOnly":
		default:
			unknown = append(unknown, key.String())
		}
		return true
	})
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return container.Mount{}, fmt.Errorf("option \"mounts\"[%d] does not support %s; supported: readOnly, source, target, type",
			index, quoteAll(unknown))
	}
	var m container.Mount
	for _, field := range []struct {
		key string
		dst *string
	}{
		{"source", &m.Source},
		{"target", &m.Target},
		{"type", &m.Type},
	} {
		value := item.Get(field.key)
		switch {
		case !value.Exists() || value.Type == gjson.Null:
		case value.Type != gjson.String:
			return container.Mount{}, fmt.Errorf("option \"mounts\"[%d].%s must be a string, got %s",
				index, field.key, value.Type)
		default:
			*field.dst = strings.TrimSpace(value.String())
		}
	}
	m.Type = strings.ToLower(m.Type)
	if ro := item.Get("readOnly"); ro.Exists() && ro.Type != gjson.Null {
		if ro.Type != gjson.True && ro.Type != gjson.False {
			return container.Mount{}, fmt.Errorf("option \"mounts\"[%d].readOnly must be a boolean, got %s", index, ro.Type)
		}
		m.ReadOnly = ro.Bool()
	}
	if err := m.Validate(); err != nil {
		return container.Mount{}, fmt.Errorf("option \"mounts\"[%d]: %w", index, err)
	}
	return m, nil
}

func (c *sContainerRunner) envs(workdir string) []string {
	var envs []string
	for _, env := range c.storage.GlobalEnv().List() {
		envs = append(envs, env.Name+"="+env.Value)
	}
	for _, env := range c.storage.Env().List() {
		envs = append(envs, env.Name+"="+env.Value)
	}
	// The host path is meaningless inside the container.
	return append(envs,
		"TASK_NAME="+c.storage.TaskName(),
		"TASK_STEP_NAME="+c.storage.Name(),
		"TASK_WORKSPACE="+workdir,
	)
}

func (c *sContainerRunner) Run(ctx context.Context) (exit api.ExecCode, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic during execution %v", r)
			exit = api.ExecCodeSystemErr
			c.storage.Log().Write(err.Error(), string(debug.Stack()))
		}
	}()
	timeout, err := c.storage.Timeout()
	if err != nil {
		return api.ExecCodeFailed, err
	}
	var runCtx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		runCtx, cancel = context.WithTimeoutCause(ctx, timeout, api.ErrExecTimeOut)
	} else {
		runCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()
	c.mu.Lock()
	if c.cleared {
		c.mu.Unlock()
		return api.ExecCodeKilled, api.ErrExecKilled
	}
	c.cancel = cancel
	c.mu.Unlock()

	if err = c.backend.Available(runCtx); err != nil {
		return api.ExecCodeSystemErr, err
	}
	// The workdir must exist before it is bind-mounted.
	root, err := openWorkdir(c.workspace)
	if err != nil {
		return api.ExecCodeSystemErr, err
	}
	_ = root.Close()

	containerOS, err := c.backend.OS(runCtx)
	if err != nil {
		return api.ExecCodeSystemErr, err
	}
	spec, err := c.prepare(containerOS, c.envs)
	if err != nil {
		return api.ExecCodeSystemErr, err
	}

	masker := newStepMasker(c.storage)
	out := &lineWriter{log: func(contents ...string) {
		// Join first: a ::add-mask:: command is only recognised as a whole line.
		if line, ok := masker.Line(strings.Join(contents, " ")); ok {
			c.storage.Log().Write(line)
		}
	}}
	defer out.Flush()
	// What the daemon calls "linux" is the Unix layout; logs use that name.
	label := "linux"
	if containerOS == container.OSWindows {
		label = "windows"
	}
	spec.Name = "taskflow-" + uuid.NewV7().String()
	spec.Stdout, spec.Stderr = out, out
	c.storage.Log().Writef("[%s] run %s (%s) as %s", c.backend.Name(), c.image, label, spec.Name)
	res, err := c.backend.Run(runCtx, spec)
	out.Flush()

	if runCtx.Err() != nil {
		if errors.Is(context.Cause(runCtx), api.ErrExecTimeOut) {
			return api.ExecCodeTimeout, api.ErrExecTimeOut
		}
		return api.ExecCodeKilled, api.ErrExecKilled
	}
	if err != nil {
		return api.ExecCodeSystemErr, err
	}
	if res.OOMKilled {
		c.storage.Log().Write("container was killed because it exceeded its memory limit")
	}
	return api.ExecCode(res.ExitCode), nil
}

// Clear stops a running container; it is safe to call repeatedly.
func (c *sContainerRunner) Clear() error {
	defer func() { _ = os.RemoveAll(c.runDir) }()
	c.mu.Lock()
	c.cleared = true
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// lineWriter turns a byte stream into log lines. It is safe for concurrent use
// so stdout and stderr can share it and keep their relative order.
type lineWriter struct {
	mu  sync.Mutex
	buf []byte
	log func(contents ...string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.log(strings.TrimRight(string(w.buf[:i]), "\r"))
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

// Flush emits a trailing partial line.
func (w *lineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.log(strings.TrimRight(string(w.buf), "\r\n"))
		w.buf = nil
	}
}
