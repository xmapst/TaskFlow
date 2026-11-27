package xsqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gorm.io/gorm/migrator"
)

var (
	// SQLite identifiers may be Unicode, quoted, or contain escaped quotes.
	identifierPattern  = "(?:`(?:``|[^`])+`|\"(?:\"\"|[^\"])+\"|'(?:''|[^'])+'|\\[[^]]+\\]|[^\\s(),;]+)"
	uniqueRegexp       = regexp.MustCompile(`(?i)^(?:CONSTRAINT\s+` + identifierPattern + `\s+)?UNIQUE\s*(\(.*)$`)
	indexRegexp        = regexp.MustCompile(`(?is)CREATE(?: UNIQUE)? INDEX ` + identifierPattern + `(?s:.*?)ON (.*)$`)
	tableRegexp        = regexp.MustCompile(`(?is)(CREATE TABLE (?:IF NOT EXISTS\s+)?` + identifierPattern + `)(?:\s*\((.*)\))?(.*)$`)
	checkRegexp        = regexp.MustCompile(`^(?i)CHECK\s*\(`)
	constraintRegexp   = regexp.MustCompile("(?i)^CONSTRAINT\\s+(?:`[^`]+`|\"[^\"]+\"|'[^']+'|\\[[^]]+]|\\S+)\\s+")
	columnRegexp       = regexp.MustCompile(`^(` + identifierPattern + `)\s+([\w\(\)\d]+)(.*)$`)
	defaultValueRegexp = regexp.MustCompile(`(?i) DEFAULT \(?(.+)?\)?( |COLLATE|GENERATED|$)`)
	regRealDataType    = regexp.MustCompile(`\D(\d+)\D?`)
)

type ddl struct {
	head    string
	fields  []string
	suffix  string // table options after the column list, e.g. WITHOUT ROWID, STRICT
	columns []migrator.ColumnType
}

func (m *Migrator) parseDDL(strs ...string) (*ddl, error) {
	var result ddl
	for _, str := range strs {
		if sections := tableRegexp.FindStringSubmatch(str); len(sections) > 0 {
			var (
				ddlBody      = sections[2]
				ddlBodyRunes = []rune(ddlBody)
				bracketLevel int
				quote        rune
				buf          string
			)
			ddlBodyRunesLen := len(ddlBodyRunes)

			result.head = sections[1]
			result.suffix = sections[3]

			for idx := 0; idx < ddlBodyRunesLen; idx++ {
				var (
					next rune = 0
					c         = ddlBodyRunes[idx]
				)
				if idx+1 < ddlBodyRunesLen {
					next = ddlBodyRunes[idx+1]
				}

				if quote != 0 {
					if c == quote {
						if c == next && quote != ']' {
							buf += string(c) + string(next)
							idx++
							continue
						}
						quote = 0
					}
				} else if c == '`' || c == '"' || c == '\'' {
					quote = c
				} else if c == '[' {
					quote = ']'
				} else {
					if c == '(' {
						bracketLevel++
					} else if c == ')' {
						bracketLevel--
					} else if bracketLevel == 0 {
						if c == ',' {
							result.fields = append(result.fields, strings.TrimSpace(buf))
							buf = ""
							continue
						}
					}
				}

				if bracketLevel < 0 {
					return nil, errors.New("invalid DDL, unbalanced brackets")
				}

				buf += string(c)
			}

			if bracketLevel != 0 {
				return nil, errors.New("invalid DDL, unbalanced brackets")
			}

			if buf != "" {
				result.fields = append(result.fields, strings.TrimSpace(buf))
			}

			for _, f := range result.fields {
				fUpper := strings.ToUpper(f)
				if checkRegexp.MatchString(f) || strings.HasPrefix(fUpper, "FOREIGN KEY") {
					continue
				}
				if matches := uniqueRegexp.FindStringSubmatch(f); matches != nil {
					if len(matches) > 0 {
						cols, err := m.parseAllColumns(matches[1])
						if err == nil && len(cols) == 1 {
							for idx, column := range result.columns {
								if column.NameValue.String == cols[0] {
									column.UniqueValue = sql.NullBool{
										Bool:  true,
										Valid: true,
									}
									result.columns[idx] = column
									break
								}
							}
						}
					}
					continue
				}
				if constraintRegexp.MatchString(f) {
					continue
				}
				if strings.HasPrefix(fUpper, "PRIMARY KEY") {
					cols, err := m.parseAllColumns(f)
					if err == nil {
						for _, name := range cols {
							for idx, column := range result.columns {
								if column.NameValue.String == name {
									column.PrimaryKeyValue = sql.NullBool{
										Bool:  true,
										Valid: true,
									}
									result.columns[idx] = column
									break
								}
							}
						}
					}
				} else if matches := columnRegexp.FindStringSubmatch(f); len(matches) > 0 {
					columnType := migrator.ColumnType{
						NameValue: sql.NullString{
							String: unquoteIdentifier(matches[1]),
							Valid:  true,
						},
						DataTypeValue: sql.NullString{
							String: matches[2],
							Valid:  true,
						},
						ColumnTypeValue: sql.NullString{
							String: matches[2],
							Valid:  true,
						},
						PrimaryKeyValue: sql.NullBool{
							Valid: true,
						},
						UniqueValue: sql.NullBool{
							Valid: true,
						},
						NullableValue: sql.NullBool{
							Bool:  true,
							Valid: true,
						},
						DefaultValueValue: sql.NullString{
							Valid: false,
						},
					}

					matchUpper := strings.ToUpper(matches[3])
					if strings.Contains(matchUpper, " NOT NULL") {
						columnType.NullableValue = sql.NullBool{
							Bool:  false,
							Valid: true,
						}
					} else if strings.Contains(matchUpper, " NULL") {
						columnType.NullableValue = sql.NullBool{
							Bool:  true,
							Valid: true,
						}
					}
					if strings.Contains(matchUpper, " UNIQUE") {
						columnType.UniqueValue = sql.NullBool{
							Bool:  true,
							Valid: true,
						}
					}
					if strings.Contains(matchUpper, " PRIMARY") {
						columnType.PrimaryKeyValue = sql.NullBool{
							Bool:  true,
							Valid: true,
						}
					}
					if defaultMatches := defaultValueRegexp.FindStringSubmatch(matches[3]); len(defaultMatches) > 1 {
						if strings.ToLower(defaultMatches[1]) != "null" {
							columnType.DefaultValueValue = sql.NullString{
								String: strings.Trim(defaultMatches[1], `"`),
								Valid:  true,
							}
						}
					}

					// data type length
					matches := regRealDataType.FindAllStringSubmatch(columnType.DataTypeValue.String, -1)
					if len(matches) == 1 && len(matches[0]) == 2 {
						size, _ := strconv.Atoi(matches[0][1])
						columnType.LengthValue = sql.NullInt64{
							Valid: true,
							Int64: int64(size),
						}
						columnType.DataTypeValue.String = strings.TrimSuffix(columnType.DataTypeValue.String, matches[0][0])
					}

					result.columns = append(result.columns, columnType)
				}
			}
		} else if matches := indexRegexp.FindStringSubmatch(str); len(matches) > 0 {
			// don't report Unique by UniqueIndex
		} else {
			return nil, errors.New("invalid DDL")
		}
	}

	return &result, nil
}

func (d *ddl) clone() *ddl {
	copied := new(ddl)
	*copied = *d

	copied.fields = make([]string, len(d.fields))
	copy(copied.fields, d.fields)
	copied.columns = make([]migrator.ColumnType, len(d.columns))
	copy(copied.columns, d.columns)

	return copied
}

func (d *ddl) compile() string {
	if len(d.fields) == 0 {
		return d.head + d.suffix
	}

	return fmt.Sprintf("%s (%s)%s", d.head, strings.Join(d.fields, ","), d.suffix)
}

func (d *ddl) renameTable(dst, src string) error {
	prefix := regexp.MustCompile(`(?i)^CREATE TABLE\s+(?:IF NOT EXISTS\s+)?`)
	loc := prefix.FindStringIndex(d.head)
	if loc == nil || unquoteIdentifier(strings.TrimSpace(d.head[loc[1]:])) != src {
		return fmt.Errorf("failed to look up tablename %q from DDL head %q", src, d.head)
	}
	d.head = d.head[:loc[1]] + quoteIdentifier(dst)
	return nil
}

func (d *ddl) compileConstraintRegexp(name string) *regexp.Regexp {
	return regexp.MustCompile("^(?i:CONSTRAINT)\\s+[\"`]?" + regexp.QuoteMeta(name) + "[\"`\\s]")
}

func (d *ddl) addConstraint(name string, sql string) {
	reg := d.compileConstraintRegexp(name)

	for i := 0; i < len(d.fields); i++ {
		if reg.MatchString(d.fields[i]) {
			d.fields[i] = sql
			return
		}
	}

	d.fields = append(d.fields, sql)
}

func (d *ddl) removeConstraint(name string) bool {
	reg := d.compileConstraintRegexp(name)

	for i := 0; i < len(d.fields); i++ {
		if reg.MatchString(d.fields[i]) {
			d.fields = append(d.fields[:i], d.fields[i+1:]...)
			return true
		}
	}
	return false
}

func (d *ddl) hasConstraint(name string) bool {
	reg := d.compileConstraintRegexp(name)

	return slices.ContainsFunc(d.fields, reg.MatchString)
}

func (d *ddl) removeColumn(name string) bool {
	reg := regexp.MustCompile("^(" + identifierPattern + ")(?:\\s|$)")
	for i, field := range d.fields {
		if match := reg.FindStringSubmatch(field); match != nil && unquoteIdentifier(match[1]) == name {
			d.fields = append(d.fields[:i], d.fields[i+1:]...)
			return true
		}
	}
	return false
}

func quoteIdentifier(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func unquoteIdentifier(name string) string {
	if len(name) < 2 {
		return name
	}
	q := name[0]
	if q == '[' && name[len(name)-1] == ']' {
		return name[1 : len(name)-1]
	}
	if (q == '`' || q == '"' || q == '\'') && name[len(name)-1] == q {
		return strings.ReplaceAll(name[1:len(name)-1], string([]byte{q, q}), string(q))
	}
	return name
}
