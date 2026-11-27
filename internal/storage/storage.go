// Package storage provides task persistence backed by GORM. New constructs an
// independent store; Init installs the default store used by package helpers.
// The caller owns and closes the underlying database connection.
package storage

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/xmapst/taskflow/internal/storage/models"
)

// New initializes and migrates a store without changing the package default.
// SQLite pools must enable foreign keys on every connection before use, as
// xsqlite.Open does by default. New does not modify connection settings.
func New(db *gorm.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("storage: database is nil")
	}
	store := &Store{db: db}
	if err := store.checkSQLite(); err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(
		&models.STask{},
		&models.SStep{},
		&models.STaskEnv{},
		&models.SStepEnv{},
		&models.SStepLog{},
		&models.SReplay{},
	); err != nil {
		return nil, fmt.Errorf("storage: migrate schema: %w", err)
	}
	return store, nil
}
