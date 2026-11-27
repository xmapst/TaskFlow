package storage

import (
	"errors"
	"uuid"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/xmapst/taskflow/internal/storage/models"
)

// EnvStore persists environment variables within a task or step scope.
type EnvStore struct {
	db          *gorm.DB
	ownerColumn string
	ownerIDs    *gorm.DB
}

func (e *EnvStore) model() any {
	if e.ownerColumn == "task_id" {
		return &models.STaskEnv{}
	}
	return &models.SStepEnv{}
}

func (e *EnvStore) scoped() *gorm.DB {
	return e.db.Model(e.model()).Where(e.ownerColumn+" IN (?)", e.ownerIDs)
}

func (e *EnvStore) List() (res models.SEnvs) {
	e.scoped().Select("name", "value", "secret").Order("id ASC").Find(&res)
	return
}

func (e *EnvStore) Insert(envs ...*models.SEnv) error {
	if len(envs) == 0 {
		return nil
	}
	for _, env := range envs {
		if env == nil || env.Name == "" {
			return errors.New("name is empty")
		}
	}
	return e.db.Transaction(func(tx *gorm.DB) error {
		var owner struct{ ID uuid.UUID }
		if err := tx.Table("(?) AS owner", e.ownerIDs).Take(&owner).Error; err != nil {
			return err
		}
		upsert := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: e.ownerColumn}, {Name: "name"}},
			DoUpdates: clause.AssignmentColumns([]string{"value", "secret", "updated_at"}),
		})
		if e.ownerColumn == "task_id" {
			rows := make([]models.STaskEnv, 0, len(envs))
			for _, env := range envs {
				rows = append(rows, models.STaskEnv{TaskID: owner.ID, SEnv: *env})
			}
			return upsert.Create(&rows).Error
		}
		rows := make([]models.SStepEnv, 0, len(envs))
		for _, env := range envs {
			rows = append(rows, models.SStepEnv{StepID: owner.ID, SEnv: *env})
		}
		return upsert.Create(&rows).Error
	})
}

func (e *EnvStore) Update(env *models.SEnv) error {
	if env == nil || env.Name == "" {
		return errors.New("name is empty")
	}
	return e.scoped().Where("name = ?", env.Name).
		Updates(map[string]any{"value": env.Value, "secret": env.Secret}).Error
}

func (e *EnvStore) Get(name string) (res string, err error) {
	if name == "" {
		return "", errors.New("name is empty")
	}
	err = e.scoped().Select("value").Where("name = ?", name).Scan(&res).Error
	return
}

func (e *EnvStore) Remove(name string) error {
	if name == "" {
		return errors.New("name is empty")
	}
	return e.scoped().Where("name = ?", name).Delete(e.model()).Error
}

func (e *EnvStore) RemoveAll() error { return e.scoped().Delete(e.model()).Error }
