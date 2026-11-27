package storage

import (
	"sync"
	"time"
	"uuid"

	"gorm.io/gorm"

	"github.com/xmapst/taskflow/api/iproto"
	"github.com/xmapst/taskflow/internal/storage/models"
)

// StepStore scopes persistence operations to one step within a task.
type StepStore struct {
	db       *gorm.DB
	taskName string
	stepName string
	taskID   uuid.UUID
	stepID   uuid.UUID
	log      *LogStore
	logOnce  sync.Once
}

func (s *StepStore) Name() string {
	return s.stepName
}

func (s *StepStore) scoped() *gorm.DB {
	query := s.db.Model(&models.SStep{})
	if s.stepID != (uuid.UUID{}) {
		return query.Where("id = ? AND task_id = ?", s.stepID, s.taskID)
	}
	task := &TaskStore{db: s.db, taskName: s.taskName}
	return query.Where("task_id IN (?) AND name = ?", task.ids(), s.stepName)
}

func (s *StepStore) ids() *gorm.DB { return s.scoped().Select("id") }

func (s *StepStore) ClearAll() error { return s.Remove() }

func (s *StepStore) Remove() error {
	return s.scoped().Delete(&models.SStep{}).Error
}

func (s *StepStore) State() (state models.State, err error) {
	err = s.scoped().
		Select("state").
		Scan(&state).
		Error
	return
}

func (s *StepStore) Env() *EnvStore {
	return &EnvStore{db: s.db, ownerColumn: "step_id", ownerIDs: s.ids()}
}

func (s *StepStore) TaskName() string {
	return s.taskName
}

func (s *StepStore) Timeout() (res time.Duration, err error) {
	err = s.scoped().
		Select("timeout").
		Scan(&res).
		Error
	return
}

func (s *StepStore) Kind() (res string, err error) {
	err = s.scoped().
		Select("kind").
		Scan(&res).
		Error
	return
}

func (s *StepStore) Options() (res iproto.SStepOptions, err error) {
	var tmp models.SStep
	err = s.scoped().
		Select("options").
		Scan(&tmp).
		Error
	if err != nil {
		return
	}

	res = iproto.SStepOptions(tmp.Options)
	return
}

func (s *StepStore) Content() (res string, err error) {
	err = s.scoped().
		Select("content").
		Scan(&res).
		Error
	return
}

func (s *StepStore) RetryPolicy() (res *iproto.SRetryPolicy, err error) {
	var tmp models.SStep
	err = s.scoped().
		Select("retry_policy").
		Scan(&tmp).
		Error
	if err != nil {
		return
	}

	res = tmp.RetryPolicy.Data()
	return
}

func (s *StepStore) Get() (res *models.SStep, err error) {
	res = new(models.SStep)
	err = s.scoped().
		First(res).
		Error
	return
}

func (s *StepStore) Update(value *models.SStepUpdate) (err error) {
	if value == nil {
		return
	}
	return s.scoped().
		Updates(value).
		Error
}

func (s *StepStore) GlobalEnv() *EnvStore {
	if s.taskID != (uuid.UUID{}) {
		owner := s.db.Model(&models.STask{}).Select("id").Where("id = ?", s.taskID)
		return &EnvStore{db: s.db, ownerColumn: "task_id", ownerIDs: owner}
	}
	task := &TaskStore{db: s.db, taskName: s.taskName}
	return task.Env()
}

func (s *StepStore) Log() *LogStore {
	s.logOnce.Do(func() { s.log = &LogStore{db: s.db, stepIDs: s.ids()} })
	return s.log
}
