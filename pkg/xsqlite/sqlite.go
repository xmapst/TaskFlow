package xsqlite

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/migrator"
	"gorm.io/gorm/schema"
	moderncsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// DriverName is the default driver name for SQLite.
const DriverName = "sqlite"

type Dialector struct {
	DriverName string
	DSN        string
	Conn       gorm.ConnPool

	// ForeignKeysPerConnection declares that a supplied Conn or custom driver
	// enables foreign keys on every physical connection before use. The
	// built-in driver applies this by default, subject to explicit DSN options.
	ForeignKeysPerConnection bool
}

type Config struct {
	DriverName string
	DSN        string
	Conn       gorm.ConnPool

	// ForeignKeysPerConnection is the caller's guarantee for an externally
	// configured pool or custom driver; xsqlite does not modify those pools.
	ForeignKeysPerConnection bool
}

func Open(dsn string) gorm.Dialector {
	return &Dialector{
		DSN: dsn,
	}
}

func New(config Config) gorm.Dialector {
	return &Dialector{
		DSN:                      config.DSN,
		DriverName:               config.DriverName,
		Conn:                     config.Conn,
		ForeignKeysPerConnection: config.ForeignKeysPerConnection,
	}
}

func (d Dialector) Name() string {
	return "sqlite"
}

func (d Dialector) Initialize(db *gorm.DB) (err error) {
	if d.DriverName == "" {
		d.DriverName = DriverName
	}

	pool := d.Conn
	var owned *sql.DB
	if pool == nil {
		dsn := d.DSN
		if d.DriverName == DriverName {
			dsn = DefaultDSN(dsn)
		}
		var err error
		owned, err = sql.Open(d.DriverName, dsn)
		if err != nil {
			return err
		}
		pool = owned
	}

	var version string
	if err := pool.QueryRowContext(context.Background(), "select sqlite_version()").Scan(&version); err != nil {
		if owned != nil {
			_ = owned.Close()
		}
		return err
	}
	// Publish the pool only after probing it: on Initialize failure GORM
	// closes db.ConnPool, which must not close a caller-owned Conn.
	db.ConnPool = pool
	// https://www.sqlite.org/releaselog/3_35_0.html
	if d.compareVersion(version, "3.35.0") >= 0 {
		callbacks.RegisterDefaultCallbacks(db, &callbacks.Config{
			CreateClauses:        []string{"INSERT", "VALUES", "ON CONFLICT", "RETURNING"},
			UpdateClauses:        []string{"UPDATE", "SET", "FROM", "WHERE", "RETURNING"},
			DeleteClauses:        []string{"DELETE", "FROM", "WHERE", "RETURNING"},
			LastInsertIDReversed: true,
		})
	} else {
		callbacks.RegisterDefaultCallbacks(db, &callbacks.Config{
			LastInsertIDReversed: true,
		})
	}

	for k, v := range d.ClauseBuilders() {
		if _, ok := db.ClauseBuilders[k]; !ok {
			db.ClauseBuilders[k] = v
		}
	}
	return
}

func (d Dialector) ClauseBuilders() map[string]clause.ClauseBuilder {
	return map[string]clause.ClauseBuilder{
		"INSERT": func(c clause.Clause, builder clause.Builder) {
			if insert, ok := c.Expression.(clause.Insert); ok {
				if stmt, ok := builder.(*gorm.Statement); ok {
					_, _ = stmt.WriteString("INSERT ")
					if insert.Modifier != "" {
						_, _ = stmt.WriteString(insert.Modifier)
						_ = stmt.WriteByte(' ')
					}

					_, _ = stmt.WriteString("INTO ")
					if insert.Table.Name == "" {
						stmt.WriteQuoted(stmt.Table)
					} else {
						stmt.WriteQuoted(insert.Table)
					}
					return
				}
			}

			c.Build(builder)
		},
		"LIMIT": func(c clause.Clause, builder clause.Builder) {
			if limit, ok := c.Expression.(clause.Limit); ok {
				var lmt = -1
				if limit.Limit != nil && *limit.Limit >= 0 {
					lmt = *limit.Limit
				}
				if lmt >= 0 || limit.Offset > 0 {
					_, _ = builder.WriteString("LIMIT ")
					_, _ = builder.WriteString(strconv.Itoa(lmt))
				}
				if limit.Offset > 0 {
					_, _ = builder.WriteString(" OFFSET ")
					_, _ = builder.WriteString(strconv.Itoa(limit.Offset))
				}
			}
		},
		"FOR": func(c clause.Clause, builder clause.Builder) {
			if _, ok := c.Expression.(clause.Locking); ok {
				// SQLite3 does not support row-level locking.
				return
			}
			c.Build(builder)
		},
	}
}

func (d Dialector) DefaultValueOf(field *schema.Field) clause.Expression {
	if field.AutoIncrement {
		return clause.Expr{
			SQL: "NULL",
		}
	}

	// doesn't work, will raise error
	return clause.Expr{
		SQL: "DEFAULT",
	}
}

func (d Dialector) Migrator(db *gorm.DB) gorm.Migrator {
	return &Migrator{migrator.Migrator{
		DB:                          db,
		Dialector:                   d,
		CreateIndexAfterCreateTable: true,
	}}
}

func (d Dialector) BindVarTo(writer clause.Writer, stmt *gorm.Statement, v any) {
	_ = writer.WriteByte('?')
}

func (d Dialector) QuoteTo(writer clause.Writer, str string) {
	var (
		underQuoted, selfQuoted bool
		continuousBacktick      int8
		shiftDelimiter          int8
	)

	for _, v := range []byte(str) {
		switch v {
		case '`':
			continuousBacktick++
			if continuousBacktick == 2 {
				_, _ = writer.WriteString("``")
				continuousBacktick = 0
			}
		case '.':
			if continuousBacktick > 0 || !selfQuoted {
				shiftDelimiter = 0
				underQuoted = false
				continuousBacktick = 0
				_, _ = writer.WriteString("`")
			}
			_ = writer.WriteByte(v)
			continue
		default:
			if shiftDelimiter-continuousBacktick <= 0 && !underQuoted {
				_, _ = writer.WriteString("`")
				underQuoted = true
				if selfQuoted = continuousBacktick > 0; selfQuoted {
					continuousBacktick -= 1
				}
			}

			for ; continuousBacktick > 0; continuousBacktick -= 1 {
				_, _ = writer.WriteString("``")
			}

			_ = writer.WriteByte(v)
		}
		shiftDelimiter++
	}

	if continuousBacktick > 0 && !selfQuoted {
		_, _ = writer.WriteString("``")
	}
	_, _ = writer.WriteString("`")
}

func (d Dialector) Explain(sql string, vars ...any) string {
	return logger.ExplainSQL(sql, nil, `"`, vars...)
}

func (d Dialector) DataTypeOf(field *schema.Field) string {
	if expr, ok := d.generatedColumnExpr(field); ok {
		return d.dataTypeOf(field) + " GENERATED ALWAYS AS (" + expr + ") STORED"
	}

	return d.dataTypeOf(field)
}

func (d Dialector) dataTypeOf(field *schema.Field) string {
	switch field.DataType {
	case schema.Bool:
		return "numeric"
	case schema.Int, schema.Uint:
		if field.PrimaryKey && field.AutoIncrement {
			// SQLite only allows AUTOINCREMENT on an INTEGER PRIMARY KEY.
			return "integer PRIMARY KEY AUTOINCREMENT"
		}

		return "integer"
	case schema.Float:
		return "real"
	case schema.String:
		return "text"
	case schema.Time:
		// Distinguish between schema.Time and tag time
		if val, ok := field.TagSettings["TYPE"]; ok {
			return val
		}

		return "datetime"
	case schema.Bytes:
		return "blob"
	}

	return string(field.DataType)
}

func (d Dialector) SavePoint(tx *gorm.DB, name string) error {
	return tx.Exec(`SAVEPOINT "` + strings.ReplaceAll(name, `"`, `""`) + `"`).Error
}

func (d Dialector) RollbackTo(tx *gorm.DB, name string) error {
	return tx.Exec(`ROLLBACK TO SAVEPOINT "` + strings.ReplaceAll(name, `"`, `""`) + `"`).Error
}

func (d Dialector) Translate(err error) error {
	if terr, ok := errors.AsType[*moderncsqlite.Error](err); ok {
		switch terr.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return gorm.ErrDuplicatedKey
		case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
			return gorm.ErrForeignKeyViolated
		case sqlite3.SQLITE_CONSTRAINT_CHECK:
			return gorm.ErrCheckConstraintViolated
		}
	}
	return err
}

func (d Dialector) compareVersion(version1, version2 string) int {
	n, m := len(version1), len(version2)
	i, j := 0, 0
	for i < n || j < m {
		x := 0
		for ; i < n && version1[i] != '.'; i++ {
			x = x*10 + int(version1[i]-'0')
		}
		i++
		y := 0
		for ; j < m && version2[j] != '.'; j++ {
			y = y*10 + int(version2[j]-'0')
		}
		j++
		if x > y {
			return 1
		}
		if x < y {
			return -1
		}
	}
	return 0
}

// generatedColumnExpr returns the expression of a computed (generated) column
// declared via the `generated` tag, if any. The `identity` keyword is reserved
// for identity columns (rendered through the dialect's native auto-increment)
// and is not a computed-column expression.
func (d Dialector) generatedColumnExpr(field *schema.Field) (string, bool) {
	value, ok := field.TagSettings["GENERATED"]
	if !ok {
		return "", false
	}
	// Ignore an empty value or a bare `generated` tag, which the tag parser
	// stores as the upper-cased key, rather than treating it as an expression.
	if value = strings.TrimSpace(value); value == "" || value == "GENERATED" {
		return "", false
	}
	if d.isIdentityKeyword(value) {
		return "", false
	}
	return value, true
}

// isIdentityKeyword reports whether value is the `identity` keyword, optionally
// combined with the generation mode `always` / `by default`. Any other token
// means value is a computed-column expression.
func (d Dialector) isIdentityKeyword(value string) bool {
	identity := false
	for token := range strings.FieldsSeq(strings.ToLower(value)) {
		switch token {
		case "identity":
			identity = true
		case "always", "by", "default":
		default:
			return false
		}
	}
	return identity
}
