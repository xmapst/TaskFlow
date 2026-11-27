package runner

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/xmapst/xexec"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/internal/storage"
	"github.com/xmapst/taskflow/internal/storage/models"
)

type sXExecRunner struct {
	mu         sync.Mutex
	cleared    bool
	storage    *storage.StepStore
	masker     *secretMasker
	ctx        context.Context
	cancel     context.CancelFunc
	shell      string
	workspace  string
	scriptPath string
	timeout    time.Duration
}

// newXExecRunner writes the step content to the script directory. The
// interpreter comes from the step's shell option, which only names the program:
// xexec runs Unix scripts through sh and picks Python from the suffix, so there
// is nowhere to pass extra interpreter arguments.
func newXExecRunner(stg *storage.StepStore, workspace, scriptDir string) (IRunner, error) {
	c := &sXExecRunner{
		storage:   stg,
		workspace: workspace,
	}
	opts, err := stepOptions(stg, "xexec", "shell")
	if err != nil {
		return nil, err
	}
	argv, err := optionCommand(opts, "shell")
	if err != nil {
		return nil, err
	}
	if len(argv) == 0 {
		argv = []string{c.defaultShell()}
	}
	if len(argv) > 1 {
		return nil, fmt.Errorf("xexec shell takes one program, got %q: xexec always runs Unix scripts through sh, "+
			"so interpreter arguments cannot be applied; pass them inside the script instead", strings.Join(argv, " "))
	}
	c.shell = filepath.Base(argv[0])
	c.scriptPath = filepath.Join(scriptDir, uuid.NewV7().String()+c.scriptSuffix())
	if err := os.MkdirAll(scriptDir, os.ModePerm); err != nil {
		return nil, err
	}
	content, err := stg.Content()
	if err != nil {
		return nil, err
	}
	if c.shell == "cmd" || c.shell == "powershell" {
		content = c.utf8ToGb2312(content)
	}
	if err = os.WriteFile(c.scriptPath, []byte(content), os.ModePerm); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *sXExecRunner) scriptSuffix() string {
	switch c.shell {
	case "python", "python2", "python3", "py", "py2", "py3":
		return ".py"
	}
	return c.selfScriptSuffix()
}

func (c *sXExecRunner) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cleared = true
	if c.cancel != nil {
		c.cancel()
	}
	_ = os.Remove(c.scriptPath)
	return nil
}

func (c *sXExecRunner) Run(ctx context.Context) (exit api.ExecCode, err error) {
	defer func() {
		if _r := recover(); _r != nil {
			err = fmt.Errorf("panic during execution %v", _r)
			exit = api.ExecCodeSystemErr
			stack := debug.Stack()
			c.storage.Log().Write(err.Error(), string(stack))
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
	c.ctx, c.cancel = runCtx, cancel
	c.masker = newStepMasker(c.storage)
	c.mu.Unlock()
	code, env, err := xexec.ExecScript(
		runCtx, c.scriptPath,
		xexec.WithScriptEnv(c.envs()...),
		xexec.WithScriptWorkdir(c.workspace),
		xexec.WithScriptLogger(c),
	)
	c.saveEnv(env, code == 0 && err == nil && runCtx.Err() == nil)
	exit = api.ExecCode(code)
	if runCtx.Err() != nil {
		switch {
		case errors.Is(context.Cause(runCtx), api.ErrExecTimeOut):
			err = api.ErrExecTimeOut
			exit = api.ExecCodeTimeout
		default:
			err = api.ErrExecKilled
			exit = api.ExecCodeKilled
		}
	}
	return
}

func (c *sXExecRunner) envs() []string {
	var envs []string
	taskEnv := c.storage.GlobalEnv().List()
	for _, env := range taskEnv {
		envs = append(envs, fmt.Sprintf("%s=%s", env.Name, env.Value))
	}
	stepEnv := c.storage.Env().List()
	for _, env := range stepEnv {
		envs = append(envs, fmt.Sprintf("%s=%s", env.Name, env.Value))
	}

	return append(envs,
		fmt.Sprintf("TASK_NAME=%s", c.storage.TaskName()),
		fmt.Sprintf("TASK_STEP_NAME=%s", c.storage.Name()),
		fmt.Sprintf("TASK_WORKSPACE=%s", c.workspace),
	)
}

func (c *sXExecRunner) utf8ToGb2312(s string) string {
	defer func() {
		recover()
	}()
	reader := transform.NewReader(strings.NewReader(s), simplifiedchinese.GBK.NewEncoder())
	d, err := io.ReadAll(reader)
	if err != nil {
		return s
	}

	return string(d)
}

// outputPrefixes are the prefixes xexec puts on the script's own output; only
// those lines may carry an ::add-mask:: command.
var outputPrefixes = []string{"[STDOUT] ", "[STDERR] "}

// emit is the single exit to the step log: every message xexec reports passes
// through the masker first.
func (c *sXExecRunner) emit(msg string) {
	c.mu.Lock()
	m := c.masker
	c.mu.Unlock()
	if m == nil {
		c.storage.Log().Write(msg)
		return
	}
	masked, keep := maskLogMessage(m, msg)
	if keep {
		c.storage.Log().Write(masked)
	}
}

// maskLogMessage masks one message xexec reports. A ::add-mask:: command is
// honoured only on the script's own output lines, so xexec's own system
// messages can never be swallowed by script output; keep reports whether the
// message should reach the log at all.
func maskLogMessage(m *secretMasker, msg string) (masked string, keep bool) {
	for _, prefix := range outputPrefixes {
		if line, ok := strings.CutPrefix(msg, prefix); ok {
			line, keep = m.Line(line)
			if !keep {
				return "", false
			}
			return prefix + line, true
		}
	}
	return m.Mask(msg), true
}

func (c *sXExecRunner) Infof(format string, args ...any) {
	c.emit(fmt.Sprintf(format, args...))
}

func (c *sXExecRunner) Errorf(format string, args ...any) {
	c.emit(fmt.Sprintf(format, args...))
}

func (c *sXExecRunner) Debugf(format string, args ...any) {
	c.emit(fmt.Sprintf(format, args...))
}

func (c *sXExecRunner) Warnf(format string, args ...any) {
	c.emit(fmt.Sprintf(format, args...))
}

func (c *sXExecRunner) saveEnv(envs map[string]any, publish bool) {
	taskEnv := c.storage.GlobalEnv()
	// A script publishing a value must not downgrade an env that is already
	// marked secret: the upsert would otherwise clear the flag.
	secrets := make(map[string]bool)
	if publish {
		for _, env := range taskEnv.List() {
			if env.Secret {
				secrets[env.Name] = true
			}
		}
	}
	for k, v := range envs {
		val, err := json.Marshal(v)
		if err != nil {
			c.Errorf("save env error: %v", err)
			continue
		}
		if publish {
			text, ok := v.(string)
			if !ok {
				text = string(val)
			}
			if err := taskEnv.Insert(&models.SEnv{Name: k, Value: text, Secret: secrets[k]}); err != nil {
				c.Errorf("save task env error: %v", err)
			}
		}
	}
}
