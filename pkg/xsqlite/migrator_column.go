package xsqlite

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/migrator"
)

func (m *Migrator) HasColumn(value any, name string) bool {
	var count int
	_ = m.Migrator.RunWithValue(value, func(stmt *gorm.Statement) error {
		if stmt.Schema != nil {
			if field := stmt.Schema.LookUpField(name); field != nil {
				name = field.DBName
			}
		}

		if name != "" {
			_ = m.DB.Raw(
				"SELECT count(*) FROM pragma_table_xinfo(?) WHERE name = ? COLLATE NOCASE",
				stmt.Table, name,
			).Row().Scan(&count)
		}
		return nil
	})
	return count > 0
}

func (m *Migrator) AlterColumn(value any, name string) error {
	return m.RunWithoutForeignKey(func() error {
		return m.recreateTable(value, nil, func(ddl *ddl, stmt *gorm.Statement) (*ddl, []any, error) {
			if stmt.Schema == nil {
				return nil, nil, fmt.Errorf("cannot alter column %q without model schema", name)
			}
			if field := stmt.Schema.LookUpField(name); field != nil {
				var sqlArgs []any
				for i, f := range ddl.fields {
					if matches := columnRegexp.FindStringSubmatch(f); len(matches) > 1 && unquoteIdentifier(matches[1]) == field.DBName {
						ddl.fields[i] = fmt.Sprintf("`%v` ?", field.DBName)
						sqlArgs = []any{m.FullDataTypeOf(field)}
						// FullDataTypeOf omits inline UNIQUE constraints; preserve them when rebuilding the table.
						if strings.Contains(strings.ToUpper(matches[3]), " UNIQUE") {
							uniName := m.DB.NamingStrategy.UniqueName(stmt.Table, field.DBName)
							uni, _ := m.GuessConstraintInterfaceAndTable(stmt, uniName)
							if uni != nil {
								uniSQL, uniArgs := uni.Build()
								ddl.addConstraint(uniName, uniSQL)
								sqlArgs = append(sqlArgs, uniArgs...)
							}
						}
						break
					}
				}
				return ddl, sqlArgs, nil
			}
			return nil, nil, fmt.Errorf("failed to alter field with name %v", name)
		})
	})
}

// ColumnTypes returns metadata for the table’s columns.
func (m *Migrator) ColumnTypes(value any) ([]gorm.ColumnType, error) {
	columnTypes := make([]gorm.ColumnType, 0)
	execErr := m.RunWithValue(value, func(stmt *gorm.Statement) (err error) {
		var (
			sqls   []string
			sqlDDL *ddl
		)

		if err := m.DB.Raw("SELECT sql FROM sqlite_master WHERE type IN ? AND tbl_name = ? AND sql IS NOT NULL order by type = ? desc", []string{"table", "index"}, stmt.Table, "table").Scan(&sqls).Error; err != nil {
			return err
		}

		if sqlDDL, err = m.parseDDL(sqls...); err != nil {
			return err
		}

		rows, err := m.DB.Raw("SELECT * FROM " + quoteIdentifier(stmt.Table) + " LIMIT 1").Rows()
		if err != nil {
			return err
		}
		defer func() {
			err = rows.Close()
		}()

		var rawColumnTypes []*sql.ColumnType
		rawColumnTypes, err = rows.ColumnTypes()
		if err != nil {
			return err
		}

		for _, c := range rawColumnTypes {
			columnType := migrator.ColumnType{
				SQLColumnType: c,
			}
			for _, column := range sqlDDL.columns {
				if column.NameValue.String == c.Name() {
					column.SQLColumnType = c
					columnType = column
					break
				}
			}
			if columnType.ScanType() == nil {
				// SQLite has no runtime value type for an empty/NULL-only result.
				// Supply a stable declared-type fallback for GORM/gen.
				typ := strings.ToUpper(c.DatabaseTypeName())
				switch {
				case typ == "DATE", typ == "DATETIME", typ == "TIME", typ == "TIMESTAMP":
					columnType.ScanTypeValue = reflect.TypeFor[time.Time]()
				case strings.Contains(typ, "INT"):
					columnType.ScanTypeValue = reflect.TypeFor[int64]()
				case strings.Contains(typ, "BOOL"):
					columnType.ScanTypeValue = reflect.TypeFor[bool]()
				case strings.Contains(typ, "REAL"), strings.Contains(typ, "FLOA"), strings.Contains(typ, "DOUB"), strings.Contains(typ, "NUM"), strings.Contains(typ, "DEC"):
					columnType.ScanTypeValue = reflect.TypeFor[float64]()
				case strings.Contains(typ, "BLOB"), typ == "":
					columnType.ScanTypeValue = reflect.TypeFor[[]byte]()
				default:
					columnType.ScanTypeValue = reflect.TypeFor[string]()
				}
			}
			columnTypes = append(columnTypes, columnType)
		}

		return err
	})

	return columnTypes, execErr
}

func (m *Migrator) DropColumn(value any, name string) error {
	return m.RunWithoutForeignKey(func() error {
		return m.recreateTable(value, nil, func(ddl *ddl, stmt *gorm.Statement) (*ddl, []any, error) {
			if stmt.Schema != nil {
				if field := stmt.Schema.LookUpField(name); field != nil {
					name = field.DBName
				}
			}

			ddl.removeColumn(name)
			return ddl, nil, nil
		})
	})
}
