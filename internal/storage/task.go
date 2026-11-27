package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"gorm.io/gorm"

	"github.com/xmapst/taskflow/internal/storage/models"
)

// TaskStore scopes persistence operations to one task name.
type TaskStore struct {
	db       *gorm.DB
	taskName string
}

func (t *TaskStore) Name() string {
	return t.taskName
}

func (t *TaskStore) ClearAll() error {
	return t.Remove() // Foreign keys cascade to steps, environments and logs.
}

func (t *TaskStore) ids() *gorm.DB {
	return t.db.Model(&models.STask{}).Select("id").Where("name = ?", t.taskName)
}

func (t *TaskStore) Remove() error {
	return t.db.Where("name = ?", t.taskName).Delete(&models.STask{}).Error
}

func (t *TaskStore) State() (state models.State, err error) {
	err = t.db.Model(&models.STask{}).
		Select("state").
		Where(map[string]any{
			"name": t.taskName,
		}).
		Scan(&state).
		Error
	return
}

func (t *TaskStore) Env() *EnvStore {
	return &EnvStore{db: t.db, ownerColumn: "task_id", ownerIDs: t.ids()}
}

func (t *TaskStore) Timeout() (res time.Duration, err error) {
	err = t.db.Model(&models.STask{}).
		Select("timeout").
		Where(map[string]any{
			"name": t.taskName,
		}).
		Scan(&res).
		Error
	return
}

// ErrTaskNotFound indicates that no persisted task has the requested name.
var ErrTaskNotFound = errors.New("task does not exist")

// Workspace returns the task's persisted relative workdir, not its API name.
func (t *TaskStore) Workspace(ctx context.Context) (string, error) {
	var task models.STask
	err := t.db.WithContext(ctx).
		Select("workdir").
		Where("name = ?", t.taskName).
		Take(&task).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrTaskNotFound
	}
	if err != nil {
		return "", err
	}
	if !filepath.IsLocal(task.Workdir) || filepath.Clean(task.Workdir) == "." {
		return "", errors.New("invalid task workdir")
	}
	return filepath.Clean(task.Workdir), nil
}

func (t *TaskStore) Get() (res *models.STask, err error) {
	res = new(models.STask)
	err = t.db.Model(&models.STask{}).
		Where(map[string]any{
			"name": t.taskName,
		}).
		First(res).
		Error
	return
}

func (t *TaskStore) Update(value *models.STaskUpdate) (err error) {
	if value == nil {
		return
	}
	return t.db.Model(&models.STask{}).
		Where(map[string]any{
			"name": t.taskName,
		}).
		Updates(value).
		Error
}

func (t *TaskStore) Step(name string) *StepStore {
	return &StepStore{db: t.db, taskName: t.taskName, stepName: name}
}

func (t *TaskStore) StepFor(row *models.SStep) *StepStore {
	return &StepStore{
		db:       t.db,
		taskName: t.taskName,
		stepName: row.Name,
		taskID:   row.TaskID,
		stepID:   row.ID,
	}
}

func (t *TaskStore) StepCreate(step *models.SStep) error {
	var owner models.STask
	if err := t.ids().Take(&owner).Error; err != nil {
		return fmt.Errorf("find task for step %q: %w", step.Name, err)
	}
	step.TaskID = owner.ID
	if err := t.db.Create(step).Error; err != nil {
		return fmt.Errorf("save step %q: %w", step.Name, err)
	}
	return nil
}

func (t *TaskStore) StepCount() (res int64) {
	t.db.Model(&models.SStep{}).Where("task_id IN (?)", t.ids()).Count(&res)
	return
}

func (t *TaskStore) StepNameList(str string) (res []string) {
	query := t.db.Model(&models.SStep{}).
		Select("name").
		Where("task_id IN (?)", t.ids())
	if str != "" {
		query = query.Where("name LIKE ?", str)
	}
	query.Find(&res)
	return
}

func (t *TaskStore) StepStateList(str string) (res map[string]models.State) {
	var steps models.SSteps
	query := t.db.Model(&models.SStep{}).
		Select("name, state").
		Where("task_id IN (?)", t.ids())
	if str != "" {
		query = query.Where("name LIKE ?", str)
	}
	query.Find(&steps)
	res = make(map[string]models.State, len(steps))
	for _, v := range steps {
		res[v.Name] = *v.State
	}
	return
}

func (t *TaskStore) StepList(str string) (res models.SSteps) {
	query := t.db.Model(&models.SStep{}).
		Where("task_id IN (?)", t.ids())
	if str != "" {
		query = query.Where("name LIKE ?", str)
	}
	query.Order("id ASC").Find(&res)
	return
}
