package xsqlite

import (
	"net/url"
	"strings"
)

// DefaultDSN preserves GORM's time.Time scan behavior with modernc and enables
// foreign keys on each new physical connection. Explicit DSN options take
// precedence. This can also be used when opening an external modernc pool.
func DefaultDSN(dsn string) string {
	name, query, _ := strings.Cut(dsn, "?")
	values, _ := url.ParseQuery(query) // the driver reports malformed queries
	defaults := make(url.Values)
	if !values.Has("_texttotime") {
		defaults.Set("_texttotime", "1")
	}
	if !values.Has("_time_format") {
		defaults.Set("_time_format", "sqlite")
	}
	if !values.Has("_foreign_keys") && !values.Has("_fk") && !hasForeignKeyPragma(values["_pragma"]) {
		defaults.Set("_foreign_keys", "on")
	}
	if len(defaults) == 0 {
		return dsn
	}
	if query != "" {
		return name + "?" + defaults.Encode() + "&" + query
	}
	return name + "?" + defaults.Encode()
}

func hasForeignKeyPragma(pragmas []string) bool {
	for _, pragma := range pragmas {
		// Be conservative: _pragma accepts SQL, including comments, quoting,
		// schema qualifiers and multiple statements. Never override a possible
		// explicit setting just because it does not use one spelling.
		if strings.Contains(strings.ToLower(pragma), "foreign_keys") {
			return true
		}
	}
	return false
}

// ConfiguresForeignKeys reports whether opening this dialector's pool applies
// the modernc defaults or the caller has explicitly opted into the external
// pool contract. The consumer must still check that foreign keys are enabled:
// modernc callers are allowed to override the default in the DSN.
func (d Dialector) ConfiguresForeignKeys() bool {
	return (d.Conn == nil && (d.DriverName == "" || d.DriverName == DriverName)) || d.ForeignKeysPerConnection
}
