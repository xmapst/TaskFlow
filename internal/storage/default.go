package storage

import (
	"sync"

	"gorm.io/gorm"

	"github.com/xmapst/taskflow/internal/storage/models"
)

var (
	defaultMu    sync.RWMutex
	defaultStore *Store
)

// Init installs a default store only after initialization succeeds. Existing
// handles retain their original database; this function does not close it.
func Init(db *gorm.DB) error {
	store, err := New(db)
	if err != nil {
		return err
	}
	defaultMu.Lock()
	defaultStore = store
	defaultMu.Unlock()
	return nil
}

// Default returns the initialized package store. Applications using the package
// helpers must call Init first; independently constructed stores do not need it.
func Default() *Store {
	defaultMu.RLock()
	store := defaultStore
	defaultMu.RUnlock()
	if store == nil {
		panic("storage: call Init before using the default store")
	}
	return store
}

// Name returns the default store's database dialect.
func Name() string { return Default().Name() }

// FixDatabase recovers interrupted work under exclusive database ownership.
func FixDatabase() error { return Default().FixDatabase() }

// Task returns a task-scoped handle from the default store.
func Task(name string) *TaskStore { return Default().Task(name) }

// TaskCreate inserts a task into the default store.
func TaskCreate(task *models.STask) error { return Default().TaskCreate(task) }

// TaskCount counts tasks in the specified state.
func TaskCount(state models.State) int64 { return Default().TaskCount(state) }

// TaskList lists tasks using the default store.
func TaskList(page, pageSize int64, prefix string) (models.STasks, int64) {
	return Default().TaskList(page, pageSize, prefix)
}

func ReplayCreate(replay *models.SReplay) error { return Default().ReplayCreate(replay) }

func ReplayList(offset, limit int64) ([]models.SReplay, int64, error) {
	return Default().ReplayList(offset, limit)
}
