package xsqlite

import (
	"database/sql"
	"fmt"
	"regexp"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/migrator"
	"gorm.io/gorm/schema"
)

func (m *Migrator) BuildIndexOptions(opts []schema.IndexOption, stmt *gorm.Statement) (results []any) {
	for _, opt := range opts {
		str := stmt.Quote(opt.DBName)
		if opt.Expression != "" {
			str = opt.Expression
		}

		if opt.Collate != "" {
			str += " COLLATE " + opt.Collate
		}

		if opt.Sort != "" {
			str += " " + opt.Sort
		}
		results = append(results, clause.Expr{
			SQL: str,
		})
	}
	return
}

func (m *Migrator) CreateIndex(value any, name string) error {
	return m.RunWithValue(value, func(stmt *gorm.Statement) error {
		if stmt.Schema != nil {
			if idx := stmt.Schema.LookIndex(name); idx != nil {
				opts := m.BuildIndexOptions(idx.Fields, stmt)
				values := []any{clause.Column{
					Name: idx.Name,
				}, clause.Table{
					Name: stmt.Table,
				}, opts}

				createIndexSQL := "CREATE "
				if idx.Class != "" {
					createIndexSQL += idx.Class + " "
				}
				createIndexSQL += "INDEX ?"

				if idx.Type != "" {
					createIndexSQL += " USING " + idx.Type
				}
				createIndexSQL += " ON ??"

				if idx.Where != "" {
					createIndexSQL += " WHERE " + idx.Where
				}

				return m.DB.Exec(createIndexSQL, values...).Error
			}
		}
		return fmt.Errorf("failed to create index with name %v", name)
	})
}

func (m *Migrator) HasIndex(value any, name string) bool {
	var count int
	_ = m.RunWithValue(value, func(stmt *gorm.Statement) error {
		if stmt.Schema != nil {
			if idx := stmt.Schema.LookIndex(name); idx != nil {
				name = idx.Name
			}
		}

		if name != "" {
			_ = m.DB.Raw(
				"SELECT count(*) FROM sqlite_master WHERE type = ? AND tbl_name = ? AND name = ?", "index", stmt.Table, name,
			).Row().Scan(&count)
		}
		return nil
	})
	return count > 0
}

// sqlite_master normalizes the CREATE INDEX prefix and strips any schema
// qualifier. Match the identifier itself so keywords, quotes and index
// expressions are not accidentally rewritten.
var createIndexName = regexp.MustCompile("(?i)^(CREATE\\s+(?:UNIQUE\\s+)?INDEX\\s+)(?:`(?:``|[^`])*`|\"(?:\"\"|[^\"])*\"|'(?:''|[^'])*'|\\[[^]]*\\]|[a-z0-9_$\\x{80}-\\x{10FFFF}]+)")

func (m *Migrator) RenameIndex(value any, oldName, newName string) error {
	return m.RunWithValue(value, func(stmt *gorm.Statement) error {
		// Keep the rename atomic even within a caller's transaction.
		db := m.DB.Session(&gorm.Session{})
		db.DisableNestedTransaction = false
		return db.Transaction(func(tx *gorm.DB) error {
			var ddl sql.NullString
			if err := tx.Raw("SELECT sql FROM main.sqlite_master WHERE type = 'index' AND tbl_name = ? AND name = ?", stmt.Table, oldName).Row().Scan(&ddl); err != nil {
				return fmt.Errorf("read DDL for index %q: %w", oldName, err)
			}
			prefix := createIndexName.FindStringSubmatchIndex(ddl.String)
			if prefix == nil {
				return fmt.Errorf("cannot rename index %q: missing or invalid CREATE INDEX DDL", oldName)
			}
			createSQL := ddl.String[:prefix[3]] + "main." + quoteIdentifier(newName) + ddl.String[prefix[1]:]
			if err := tx.Exec("DROP INDEX main." + quoteIdentifier(oldName)).Error; err != nil {
				return err
			}
			return tx.Exec(createSQL).Error
		})
	})
}

func (m *Migrator) DropIndex(value any, name string) error {
	return m.RunWithValue(value, func(stmt *gorm.Statement) error {
		if stmt.Schema != nil {
			if idx := stmt.Schema.LookIndex(name); idx != nil {
				name = idx.Name
			}
		}

		return m.DB.Exec("DROP INDEX ?", clause.Column{
			Name: name,
		}).Error
	})
}

type Index struct {
	Name    string
	Origin  string
	Seq     int
	Unique  bool
	Partial bool
}

// GetIndexes return Indexes []gorm.Index and execErr error,
// See the [doc]
//
// [doc]: https://www.sqlite.org/pragma.html#pragma_index_list
func (m *Migrator) GetIndexes(value any) ([]gorm.Index, error) {
	indexes := make([]gorm.Index, 0)
	err := m.RunWithValue(value, func(stmt *gorm.Statement) error {
		rst := make([]*Index, 0)
		if err := m.DB.Raw("SELECT * FROM PRAGMA_index_list(?)", stmt.Table).Scan(&rst).Error; err != nil { // alias `PRAGMA index_list(?)`
			return err
		}
		for _, index := range rst {
			if index.Origin == "u" { // skip the index was created by a UNIQUE constraint
				continue
			}
			var columns []string
			if err := m.DB.Raw("SELECT name FROM PRAGMA_index_info(?)", index.Name).Scan(&columns).Error; err != nil { // alias `PRAGMA index_info(?)`
				return err
			}
			indexes = append(indexes, &migrator.Index{
				TableName:  stmt.Table,
				NameValue:  index.Name,
				ColumnList: columns,
				PrimaryKeyValue: sql.NullBool{
					Bool:  index.Origin == "pk",
					Valid: true,
				}, // The exceptions are INTEGER PRIMARY KEY
				UniqueValue: sql.NullBool{
					Bool:  index.Unique,
					Valid: true,
				},
			})
		}
		return nil
	})
	return indexes, err
}
