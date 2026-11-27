package config

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/go-gorm/caches/v4"
	"github.com/xmapst/xslog"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/internal/storage"
	"github.com/xmapst/taskflow/internal/storage/models"
	"github.com/xmapst/taskflow/pkg/lockfile"
	"github.com/xmapst/taskflow/pkg/utility"
	"github.com/xmapst/taskflow/pkg/xsqlite"
)

var (
	dbPool *sql.DB
	dbLock lockfile.Lockfile
)

func Init() error {
	var level slog.Level
	if err := level.UnmarshalText([]byte(App.LogLevel)); err != nil {
		return fmt.Errorf("invalid log level: %w", err)
	}
	options := []xslog.Option{xslog.WithLevel(level)}
	switch App.LogOutput {
	case "file":
		options = append(options, xslog.WithFile(filepath.Join(App.LogDir(), api.ServiceName+".log")))
	case "stdout":
		options = append(options, xslog.WithWriter(os.Stdout))
	default:
		return errors.New("invalid log output: expected file or stdout")
	}
	if err := xslog.Setup(options...); err != nil {
		return fmt.Errorf("setup logger: %w", err)
	}
	var err error

	var dirs = map[string]string{
		"root":      App.RootDir,
		"log":       App.LogDir(),
		"temp":      App.TempDir(),
		"replay":    App.ReplayDir(),
		"script":    App.ScriptDir(),
		"workspace": App.WorkspaceDir(),
	}

	for name, dir := range dirs {
		if name == "log" && App.LogOutput != "file" {
			continue
		}
		if err = utility.EnsureDirExist(dir); err != nil {
			return fmt.Errorf("failed to ensure directory %s: %v", dir, err)
		}
		slog.Info(
			"directory configured",
			slog.String("name", name),
			slog.String("path", dir),
		)
	}

	if err = App.initStorage(); err != nil {
		slog.Error(
			"initialize storage failed",
			slog.Any("error", err),
		)
		return err
	}

	if App.SelfURL != "" {
		utility.StartSelfUpdate(App.SelfURL, func() bool {
			if (storage.TaskCount(models.StateRunning) + storage.TaskCount(models.StatePending)) != 0 {
				// 还有任务执行中或者等待执行不升级
				slog.Warn("the task has not been completed")
				return true
			}
			return false
		})
	}
	return nil
}

func (c AppConfig) initStorage() (err error) {
	dir, err := filepath.Abs(c.DataDir())
	if err != nil {
		return fmt.Errorf("resolve database directory: %w", err)
	}
	if err = utility.EnsureDirExist(dir); err != nil {
		return fmt.Errorf("failed to ensure directory %s: %v", dir, err)
	}
	file := filepath.Join(dir, fmt.Sprintf("%s.db3", api.ServiceName))
	lockPath := file + ".lock"
	lock, err := lockfile.New(lockPath)
	if err != nil {
		return fmt.Errorf("initialize database lock at %q: %w", lockPath, err)
	}
	err = lock.TryLock()
	if errors.Is(err, lockfile.ErrBusy) {
		return fmt.Errorf("database %q is already in use by another TaskFlow instance: %w", file, err)
	}
	if err != nil {
		return fmt.Errorf("lock database at %q: %w", lockPath, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, lock.Unlock())
		}
	}()

	slog.Info(
		"database configured",
		slog.String("path", file),
	)
	dialector := xsqlite.Open(storage.SQLiteDSN(file))
	gromConf := &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			SingularTable:       true,
			NoLowerCase:         false,
			IdentifierMaxLength: 256,
		},
		Logger: logger.NewSlogLogger(slog.Default(), logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			Colorful:                  false,
			IgnoreRecordNotFoundError: true,
			LogLevel:                  logger.Error,
		}),
		SkipDefaultTransaction: true,
		FullSaveAssociations:   true,
		TranslateError:         true,
	}
	db, err := gorm.Open(dialector, gromConf)
	if err != nil {
		slog.Error(
			"operation failed",
			slog.Any("error", err),
		)
		return err
	}

	sqlDB, err := db.DB()
	if err != nil {
		slog.Error(
			"operation failed",
			slog.Any("error", err),
		)
		return err
	}
	// Serialize SQLite writes to avoid SQLITE_BUSY under concurrent workers.
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetMaxOpenConns(1)
	defer func() {
		if err != nil {
			err = errors.Join(err, sqlDB.Close())
		}
	}()

	if err = db.Use(&caches.Caches{Conf: &caches.Config{
		Easer: true,
	}}); err != nil {
		return fmt.Errorf("initialize database cache: %w", err)
	}

	if err = storage.Init(db); err != nil {
		slog.Error(
			"operation failed",
			slog.Any("error", err),
		)
		return err
	}
	if err = storage.FixDatabase(); err != nil {
		return err
	}
	dbPool, dbLock = sqlDB, lock
	return nil
}

func CloseDB() error {
	if dbPool == nil {
		return nil
	}
	// Close SQLite before allowing another process to open it.
	closeErr := dbPool.Close()
	unlockErr := dbLock.Unlock()
	dbPool, dbLock = nil, ""
	return errors.Join(closeErr, unlockErr)
}
