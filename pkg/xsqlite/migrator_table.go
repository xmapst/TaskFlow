package xsqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Migrator) HasTable(value any) bool {
	var count int
	_ = m.Migrator.RunWithValue(value, func(stmt *gorm.Statement) error {
		return m.DB.Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", stmt.Table).Row().Scan(&count)
	})
	return count > 0
}

func (m *Migrator) DropTable(values ...any) error {
	return m.RunWithoutForeignKey(func() error {
		values = m.ReorderModels(values, false)
		tx := m.DB.Session(&gorm.Session{})

		for _, value := range slices.Backward(values) {
			if err := m.RunWithValue(value, func(stmt *gorm.Statement) error {
				return tx.Exec("DROP TABLE IF EXISTS ?", clause.Table{
					Name: stmt.Table,
				}).Error
			}); err != nil {
				return err
			}
		}

		return nil
	})
}

func (m *Migrator) GetTables() (tableList []string, err error) {
	return tableList, m.DB.Raw("SELECT name FROM sqlite_master where type=?", "table").Scan(&tableList).Error
}

func (m *Migrator) getRawDDL(table string) (string, error) {
	var createSQL string
	err := m.DB.Raw("SELECT sql FROM sqlite_master WHERE type = ? AND tbl_name = ? AND name = ?", "table", table, table).Row().Scan(&createSQL)
	if err != nil {
		return createSQL, fmt.Errorf("read DDL for table %q: %w", table, err)
	}
	return createSQL, nil
}

func (m *Migrator) recreateTable(
	value any, tablePtr *string,
	getCreateSQL func(ddl *ddl, stmt *gorm.Statement) (sql *ddl, sqlArgs []any, err error),
) error {
	return m.RunWithValue(value, func(stmt *gorm.Statement) error {
		table := stmt.Table
		if tablePtr != nil {
			table = *tablePtr
		}

		rawDDL, err := m.getRawDDL(table)
		if err != nil {
			return err
		}

		originDDL, err := m.parseDDL(rawDDL)
		if err != nil {
			return err
		}

		createDDL, sqlArgs, err := getCreateSQL(originDDL.clone(), stmt)
		if err != nil {
			return err
		}
		if createDDL == nil {
			return nil
		}

		newTableName := table + "__temp"
		if err := createDDL.renameTable(newTableName, table); err != nil {
			return err
		}

		createSQL := createDDL.compile()

		// indexes and triggers are dropped together with the old table; save
		// their DDL so they can be recreated on the rebuilt table.
		var auxDDLs []string
		if err := m.DB.Raw(
			"SELECT sql FROM sqlite_master WHERE tbl_name = ? AND type IN (?, ?) AND sql IS NOT NULL",
			table, "index", "trigger",
		).Scan(&auxDDLs).Error; err != nil {
			return err
		}

		return m.DB.Transaction(func(tx *gorm.DB) error {
			// Save the original sequence before dropping the table, even if it
			// has no rows left to copy. sqlite_sequence may not exist at all.
			var hasSequence bool
			if err := tx.Raw("SELECT EXISTS (SELECT 1 FROM main.sqlite_master WHERE name = 'sqlite_sequence' AND type = 'table')").Row().Scan(&hasSequence); err != nil {
				return err
			}
			var sequence sql.NullInt64
			if hasSequence {
				if err := tx.Raw("SELECT MAX(seq) FROM main.sqlite_sequence WHERE name = ?", table).Row().Scan(&sequence); err != nil {
					return err
				}
			}

			if err := tx.Exec(createSQL, sqlArgs...).Error; err != nil {
				return err
			}

			// Read column metadata from the actual schemas. Generated columns
			// can be read from the source, but only ordinary columns are writable.
			var columns []string
			if err := tx.Raw(`SELECT dst.name
				FROM pragma_table_xinfo(?) AS dst
				JOIN pragma_table_xinfo(?) AS src ON src.name = dst.name COLLATE NOCASE
				WHERE dst.hidden = 0 AND src.hidden <> 1
				ORDER BY dst.cid`, newTableName, table).Scan(&columns).Error; err != nil {
				return err
			}
			if len(columns) == 0 {
				return fmt.Errorf("no common writable columns to rebuild table %q", table)
			}
			for i, column := range columns {
				columns[i] = quoteIdentifier(column)
			}
			columnList := strings.Join(columns, ",")
			if err := tx.Exec(fmt.Sprintf("INSERT INTO %s(%s) SELECT %s FROM %s", quoteIdentifier(newTableName), columnList, columnList, quoteIdentifier(table))).Error; err != nil {
				return err
			}
			if err := tx.Exec("DROP TABLE " + quoteIdentifier(table)).Error; err != nil {
				return err
			}

			// legacy_alter_table keeps RENAME from re-resolving views that
			// reference the table; they point at the original name and become
			// valid again right after the rename.
			var legacy int
			if err = tx.Raw("PRAGMA legacy_alter_table").Row().Scan(&legacy); err != nil {
				return err
			}
			if err = tx.Exec("PRAGMA legacy_alter_table = ON").Error; err != nil {
				return err
			}
			renameErr := tx.Exec(fmt.Sprintf("ALTER TABLE %s RENAME TO %s", quoteIdentifier(newTableName), quoteIdentifier(table))).Error
			renameErr = errors.Join(renameErr, tx.WithContext(context.WithoutCancel(tx.Statement.Context)).Exec(fmt.Sprintf("PRAGMA legacy_alter_table = %d", legacy)).Error)
			if renameErr != nil {
				return renameErr
			}

			// INSERT ... SELECT initializes the replacement's sequence entry
			// even when the source is empty. Rename moves that entry to table.
			// Update only an existing entry, so a migration removing AUTOINCREMENT
			// does not leave stale sequence state behind.
			if sequence.Valid {
				if err := tx.Exec("UPDATE main.sqlite_sequence SET seq = ? WHERE name = ? AND (seq IS NULL OR seq < ?)", sequence.Int64, table, sequence.Int64).Error; err != nil {
					return err
				}
			}

			// recreate the saved indexes and triggers; ones referencing a
			// column that no longer exists cannot apply anymore and are
			// skipped, any other failure aborts the migration
			for _, aux := range auxDDLs {
				if err = tx.Exec(aux).Error; err != nil {
					if strings.Contains(err.Error(), "no such column") {
						continue
					}
					return err
				}
			}
			return nil
		})
	})
}
