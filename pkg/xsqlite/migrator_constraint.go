package xsqlite

import (
	"gorm.io/gorm"
)

func (m *Migrator) CreateConstraint(value any, name string) error {
	return m.RunWithoutForeignKey(func() error { return m.createConstraint(value, name) })
}

func (m *Migrator) createConstraint(value any, name string) error {
	return m.RunWithValue(value, func(stmt *gorm.Statement) error {
		constraint, table := m.GuessConstraintInterfaceAndTable(stmt, name)

		return m.recreateTable(value, &table,
			func(ddl *ddl, stmt *gorm.Statement) (*ddl, []any, error) {
				var (
					constraintName   string
					constraintSql    string
					constraintValues []any
				)

				if constraint != nil {
					constraintName = constraint.GetName()
					constraintSql, constraintValues = constraint.Build()
				} else {
					return nil, nil, nil
				}

				ddl.addConstraint(constraintName, constraintSql)
				return ddl, constraintValues, nil
			})
	})
}

func (m *Migrator) DropConstraint(value any, name string) error {
	return m.RunWithoutForeignKey(func() error { return m.dropConstraint(value, name) })
}

func (m *Migrator) dropConstraint(value any, name string) error {
	return m.RunWithValue(value, func(stmt *gorm.Statement) error {
		constraint, table := m.GuessConstraintInterfaceAndTable(stmt, name)
		if constraint != nil {
			name = constraint.GetName()
		}

		return m.recreateTable(value, &table,
			func(ddl *ddl, stmt *gorm.Statement) (*ddl, []any, error) {
				ddl.removeConstraint(name)
				return ddl, nil, nil
			})
	})
}

func (m *Migrator) HasConstraint(value any, name string) bool {
	var count int64
	_ = m.RunWithValue(value, func(stmt *gorm.Statement) error {
		constraint, table := m.GuessConstraintInterfaceAndTable(stmt, name)
		if constraint != nil {
			name = constraint.GetName()
		}

		_ = m.DB.Raw(
			"SELECT count(*) FROM sqlite_master WHERE type = ? AND tbl_name = ? AND (sql LIKE ? OR sql LIKE ? OR sql LIKE ? OR sql LIKE ? OR sql LIKE ?)",
			"table", table, `%CONSTRAINT "`+name+`" %`, `%CONSTRAINT `+name+` %`, "%CONSTRAINT `"+name+"`%", "%CONSTRAINT ["+name+"]%", "%CONSTRAINT \t"+name+"\t%",
		).Row().Scan(&count)

		return nil
	})

	return count > 0
}
