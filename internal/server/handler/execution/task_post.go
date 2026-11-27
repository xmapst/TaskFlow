package execution

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/xmapst/xdag"
	"go.uber.org/multierr"
	"gorm.io/datatypes"
	"sigs.k8s.io/yaml"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/api/httpx"
	"github.com/xmapst/taskflow/api/iproto"
	"github.com/xmapst/taskflow/internal/chanrpc"
	"github.com/xmapst/taskflow/internal/config"
	"github.com/xmapst/taskflow/internal/storage"
	"github.com/xmapst/taskflow/internal/storage/models"
	"github.com/xmapst/taskflow/pkg/utility"
)

// CreateTask 校验并提交任务定义。
// @Summary		创建
// @Description	JSON/YAML 请求体为任务对象；时长字段支持 Go 时长字符串（如 2m）或整数纳秒。Swagger 2.0 的时长 schema 展示整数形式。
// @Tags		任务
// @Accept		application/json,application/yaml
// @Produce		application/json,application/yaml
// @Param		task body iproto.STaskReq true "任务内容"
// @Success		200 {object} api.Response[iproto.STaskCreateRes]
// @Router		/api/v1/task [post]
func (h *Handler) CreateTask(w http.ResponseWriter, r *http.Request) {
	var req = new(iproto.STaskReq)
	if err := h.bindTask(r, req); err != nil {
		slog.Error(
			"operation failed",
			slog.Any("error", err),
		)
		httpx.Send(w, r, api.WithCode[any](api.CodeFailed).WithError(err))
		return
	}

	name, err := h.createTask(r.Context(), req, config.App.ExecTimeout)
	if err != nil {
		httpx.Send(w, r, api.WithCode[any](api.CodeFailed).WithError(err))
		return
	}

	w.Header().Set(iproto.XTaskName, name)

	httpx.Send(w, r, api.WithData(&iproto.STaskCreateRes{
		Name:  name,
		Count: int64(len(req.Step)),
	}))
}

func (h *Handler) bindTask(r *http.Request, req *iproto.STaskReq) error {
	if r.Header.Get("Content-Type") == "" {
		return fmt.Errorf("task Content-Type is required; use JSON or YAML")
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return err
	}
	return decodeTask(mediaType, r.Body, req)
}

// decodeTask parses a task definition from its wire format. It is shared by the
// HTTP endpoint and the WebSocket call, which only differ in where the bytes
// come from.
func decodeTask(mediaType string, body io.Reader, req *iproto.STaskReq) error {
	var err error
	switch mediaType {
	case "application/json":
		err = json.UnmarshalRead(body, req, json.RejectUnknownMembers(true), api.JSONDuration)
	case "application/yaml":
		var data []byte
		if data, err = io.ReadAll(body); err == nil {
			data, err = yaml.YAMLToJSON(data)
		}
		if err == nil {
			err = json.UnmarshalRead(bytes.NewReader(data), req, json.RejectUnknownMembers(true), api.JSONDuration)
		}
	default:
		err = fmt.Errorf("unsupported task content type %q", mediaType)
	}
	if err == nil && req.Step == nil {
		return fmt.Errorf("step is required")
	}
	return err
}

func (h *Handler) createTask(ctx context.Context, task *iproto.STaskReq, maxTimeout time.Duration) (name string, err error) {
	if task == nil {
		return "", errors.New("task cannot be null")
	}
	name, err = h.normalizeTaskFields(task, maxTimeout)
	if err != nil {
		slog.Error(
			"task review",
			slog.Any("error", err),
		)
		return "", err
	}

	// Validate and normalize every step before replacing any persisted history.
	if err = h.normalizeTaskSteps(task); err != nil {
		return "", err
	}

	var db = storage.Task(name)
	state, err := db.State()
	if err != nil {
		slog.Error(
			"task state",
			slog.String("task", name),
			slog.Any("error", err),
		)
		return "", err
	}
	if state != models.StateStopped && state != models.StateSkipped && state != models.StateUnknown && state != models.StateFailed {
		return "", errors.New("task is running")
	}

	// Do not replace a task unless its previous data has been cleared.
	if err = db.ClearAll(); err != nil {
		return "", fmt.Errorf("clear previous task data: %w", err)
	}

	defer func() {
		if err != nil {
			// Preserve both the creation failure and any rollback failure.
			err = errors.Join(err, db.ClearAll())
		}
	}()

	if err = h.saveTask(name, task); err != nil {
		slog.Error(
			"task save",
			slog.String("task", name),
			slog.Any("error", err),
		)
		return "", err
	}

	if err = h.saveSteps(db, task.Step); err != nil {
		return "", err
	}
	row, err := db.Get()
	if err != nil {
		return "", err
	}
	if err = chanrpc.Submit(ctx, iproto.TaskSubmission{
		Name: row.Name,
		ID:   row.ID,
	}); err != nil {
		return "", err
	}
	return
}

func (h *Handler) saveTask(name string, task *iproto.STaskReq) error {
	// save task
	err := storage.TaskCreate(&models.STask{
		Name:     name,
		Workdir:  task.Workdir,
		Timeout:  task.Timeout,
		Cleanup:  task.Cleanup,
		Message:  "the task is waiting to be scheduled for execution",
		State:    new(models.StatePending),
		OldState: new(models.StatePending),
	})
	if err != nil {
		slog.Error(
			"task save",
			slog.String("task", name),
			slog.Any("error", err),
		)
		return fmt.Errorf("save task error: %s", err)
	}

	var envs models.SEnvs
	for _, env := range task.Env {
		envs = append(envs, &models.SEnv{
			Name:   env.Name,
			Value:  env.Value,
			Secret: env.Secret,
		})
	}
	if err = storage.Task(name).Env().Insert(envs...); err != nil {
		slog.Error(
			"task save envs",
			slog.String("task", name),
			slog.Any("error", err),
		)
		return fmt.Errorf("save task env error: %s", err)
	}
	return nil
}

func (h *Handler) saveSteps(db *storage.TaskStore, steps iproto.SStepsReq) (err error) {
	for _, step := range steps {
		if _err := h.saveStep(db.Name(), step); _err != nil {
			slog.Error(
				"task create step",
				slog.String("task", db.Name()),
				slog.String("step", step.Name),
				slog.Any("error", _err),
			)
			err = multierr.Append(err, fmt.Errorf("save step error: %s", _err))
		}
	}
	if err != nil {
		slog.Error(
			"task create",
			slog.String("task", db.Name()),
			slog.Any("error", err),
		)
		return err
	}
	return nil
}

func (h *Handler) saveStep(taskName string, step *iproto.SStepReq) (err error) {
	taskStorage := storage.Task(taskName)
	data := &models.SStep{
		Name:     step.Name,
		Desc:     step.Desc,
		Kind:     step.Kind,
		Options:  datatypes.JSON(step.Options),
		Content:  step.Content,
		Timeout:  step.Timeout,
		Message:  "the step is waiting to be scheduled for execution",
		Code:     new(api.ExecCode(0)),
		State:    new(models.StatePending),
		OldState: new(models.StatePending),
	}
	data.Routing = datatypes.NewJSONType(step.Routing)
	data.RetryPolicy = datatypes.NewJSONType(step.RetryPolicy)
	err = taskStorage.StepCreate(data)
	if err != nil {
		slog.Error(
			"step save",
			slog.String("task", taskName),
			slog.String("step", step.Name),
			slog.Any("error", err),
		)
		return fmt.Errorf("save step error: %s", err)
	}
	stepStorage := taskStorage.StepFor(data)
	defer func() {
		if err != nil {
			err = errors.Join(err, stepStorage.ClearAll())
		}
	}()
	// save step env
	var envs models.SEnvs
	for _, env := range step.Env {
		envs = append(envs, &models.SEnv{
			Name:   env.Name,
			Value:  env.Value,
			Secret: env.Secret,
		})
	}
	if err = stepStorage.Env().Insert(envs...); err != nil {
		slog.Error(
			"step save envs",
			slog.String("task", taskName),
			slog.String("step", step.Name),
			slog.Any("error", err),
		)
		return fmt.Errorf("save step env error: %s", err)
	}
	return
}

func (h *Handler) normalizeTaskFields(task *iproto.STaskReq, maxTimeout time.Duration) (name string, err error) {
	if task == nil {
		return "", errors.New("task cannot be null")
	}
	name = fmt.Sprintf("Task-%s", uuid.NewV7().String())
	if task.Workdir == "" {
		task.Workdir = name
	}
	if !filepath.IsLocal(task.Workdir) || strings.ContainsAny(task.Workdir, "\\:\x00") || filepath.Clean(task.Workdir) == "." {
		return "", errors.New("workdir must be a relative directory inside the workspace")
	}
	for part := range strings.SplitSeq(task.Workdir, "/") {
		if part == ".." {
			return "", errors.New("workdir must not contain '..' path components")
		}
	}
	task.Workdir = filepath.ToSlash(filepath.Clean(task.Workdir))
	for _, step := range task.Step {
		if step == nil {
			return "", errors.New("step cannot be null")
		}
	}
	for _, env := range task.Env {
		if env == nil {
			return "", errors.New("environment entry cannot be null")
		}
	}
	if task.Step == nil || len(task.Step) == 0 {
		return "", errors.New("steps can not be empty")
	}

	// Reject duplicate environment variable names.
	var envKeys []string
	for _, v := range task.Env {
		envKeys = append(envKeys, v.Name)
	}
	dup := utility.CheckDuplicate(envKeys)
	if dup != nil {
		return "", fmt.Errorf("duplicate keys %v", dup)
	}

	// Use the configured maximum for missing or out-of-range timeouts.
	if task.Timeout <= 0 || task.Timeout >= maxTimeout {
		task.Timeout = maxTimeout
	}
	return name, nil
}

func (h *Handler) normalizeTaskSteps(task *iproto.STaskReq) error {
	// Names must be final before uniqueness and dependency checks.
	for _, step := range task.Step {
		if err := h.normalizeStep(step); err != nil {
			return err
		}
	}
	if err := h.checkUniqueStepNames(task.Step); err != nil {
		return err
	}
	if err := h.validateStepDependencies(task.Step); err != nil {
		return err
	}
	if task.Timeout <= 0 {
		return errors.New("global timeout must be greater than 0")
	}
	for _, step := range task.Step {
		if step.Timeout <= 0 || step.Timeout > task.Timeout {
			step.Timeout = task.Timeout
		}
	}
	return nil
}

func (h *Handler) normalizeStep(step *iproto.SStepReq) error {
	if step == nil || strings.TrimSpace(step.Name) == "" {
		return errors.New("step name cannot be null")
	}
	for _, env := range step.Env {
		if env == nil {
			return errors.New("step environment entry cannot be null")
		}
	}
	if step.Content == "" {
		return errors.New("step content is empty")
	}

	step.Kind = strings.TrimSpace(step.Kind)
	if step.Kind == "" {
		return fmt.Errorf("step %q: kind is required", step.Name)
	}
	if _, err := step.Options.Object(); err != nil {
		return fmt.Errorf("step %q: %w", step.Name, err)
	}

	// Reject duplicate environment variable names.
	var envKeys []string
	for _, v := range step.Env {
		envKeys = append(envKeys, v.Name)
	}
	dup := utility.CheckDuplicate(envKeys)
	if dup != nil {
		return fmt.Errorf("duplicate key %v", dup)
	}

	if policy := step.RetryPolicy; policy != nil && (policy.Jitter < 0 || policy.Jitter > 1) {
		return fmt.Errorf("step %q: retryPolicy.jitter must be between 0 and 1", step.Name)
	}
	if step.Routing != nil {
		if err := h.normalizeStepRouting(step.Routing); err != nil {
			return fmt.Errorf("step %q: %w", step.Name, err)
		}
	}
	return nil
}

func (h *Handler) checkUniqueStepNames(steps iproto.SStepsReq) error {
	counts := make(map[string]int)
	for _, v := range steps {
		if v.Name == "" {
			continue
		}
		counts[v.Name]++
	}
	var errs []error
	for name, count := range counts {
		if count > 1 {
			errs = append(errs, fmt.Errorf("%s repeat count %d", name, count))
		}
	}
	if errs == nil {
		return nil
	}
	return fmt.Errorf("%v", errs)
}

// validateStepDependencies rejects invalid graphs before task history is replaced.
func (h *Handler) validateStepDependencies(steps iproto.SStepsReq) error {
	indegree := make(map[string]int, len(steps))
	dependents := make(map[string][]string, len(steps))
	for _, step := range steps {
		indegree[step.Name] = 0
	}
	for _, step := range steps {
		for _, edge := range iproto.DAGDependencies(step.Routing).Edges {
			if _, exists := indegree[edge.Task]; !exists {
				return fmt.Errorf("step %q depends on unknown step %q", step.Name, edge.Task)
			}
			indegree[step.Name]++
			dependents[edge.Task] = append(dependents[edge.Task], step.Name)
		}
	}
	ready := make([]string, 0, len(steps))
	for name, count := range indegree {
		if count == 0 {
			ready = append(ready, name)
		}
	}
	for i := 0; i < len(ready); i++ {
		for _, dependent := range dependents[ready[i]] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
	}
	if len(ready) != len(steps) {
		return errors.New("step dependencies contain a cycle")
	}
	return nil
}

func (h *Handler) normalizeStepRouting(routing *iproto.SStepRouting) error {
	if routing.Join != "" && routing.Join != "all" && routing.Join != "any" {
		return fmt.Errorf("routing.join must be all or any, got %q", routing.Join)
	}
	seen := make(map[iproto.SStepEdge]struct{}, len(routing.Edges))
	edges := routing.Edges[:0]
	for i, edge := range routing.Edges {
		if edge.Step == "" {
			return fmt.Errorf("routing.edges[%d].step is required", i)
		}
		key := edge
		if key.Trigger == "" {
			key.Trigger = string(xdag.OnSuccess)
		}
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			edges = append(edges, edge)
		}
	}
	routing.Edges = edges
	if slices.Contains(routing.Emit, string(xdag.OnFailure)) {
		return errors.New("routing.emit cannot contain the reserved failure trigger")
	}
	return nil
}
