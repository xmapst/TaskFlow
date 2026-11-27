package xsqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/migrator"
)

type Migrator struct {
	migrator.Migrator
}

func (m *Migrator) RunWithoutForeignKey(fc func() error) error {
	// PRAGMAs are connection-local: use the same connection for disabling,
	// rebuilding and restoring, even when the caller has a multi-connection pool.
	run := func(db *gorm.DB) (err error) {
		// Connection supplies a non-cloning handle. Isolate each statement so
		// PRAGMA/Raw calls cannot overwrite the migration's model/table state.
		db = db.Session(&gorm.Session{})
		original := m.DB
		m.DB = db
		defer func() { m.DB = original }()
		var enabled int
		if err = db.Raw("PRAGMA foreign_keys").Row().Scan(&enabled); err != nil {
			return err
		}
		if enabled == 1 {
			if err = db.Exec("PRAGMA foreign_keys = OFF").Error; err != nil {
				return err
			}
			defer func() {
				// Cleanup must still run if the migration's context was cancelled.
				cleanup := db.WithContext(context.WithoutCancel(db.Statement.Context))
				err = errors.Join(err, cleanup.Exec("PRAGMA foreign_keys = ON").Error)
			}()
			var current int
			if err = db.Raw("PRAGMA foreign_keys").Row().Scan(&current); err != nil {
				return err
			}
			if current != 0 {
				return fmt.Errorf("cannot disable foreign_keys inside an active transaction")
			}
		}
		return fc()
	}
	// An existing transaction/connection must not acquire another pool slot.
	switch m.DB.Statement.ConnPool.(type) {
	case *sql.DB, *gorm.PreparedStmtDB:
		return m.DB.Connection(run)
	default:
		return run(m.DB)
	}
}

func (m *Migrator) CurrentDatabase() (name string) {
	var null any
	_ = m.DB.Raw("PRAGMA database_list").Row().Scan(&null, &name, &null)
	return
}
