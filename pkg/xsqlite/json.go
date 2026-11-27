package xsqlite

import (
	"database/sql/driver"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// JSON uses a JSON declaration and standard JSON values, never binary JSONB.
// Objects/arrays are stored as text. SQLite's NUMERIC affinity can coerce numeric
// scalars; use gorm:"type:JSON TEXT" when exact numeric text must be preserved.
// A zero value represents SQL NULL; JSON("null") represents JSON null.
// Use json_valid in a CHECK constraint if writes outside this type must also
// be validated: SQLite's JSON declaration alone does not enforce validity.
type JSON jsontext.Value

func (j JSON) valid(data []byte) bool {
	return jsontext.Value(data).IsValid(jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))
}

func (j JSON) Value() (driver.Value, error) {
	if len(j) == 0 {
		return nil, nil
	}
	if !j.valid(j) {
		return nil, fmt.Errorf("sqlite.JSON: invalid JSON")
	}
	// Return string rather than []byte so database/sql binds TEXT, not BLOB.
	return string(j), nil
}

func (j *JSON) Scan(value any) error {
	if j == nil {
		return fmt.Errorf("sqlite.JSON: Scan on nil receiver")
	}
	var data []byte
	switch value := value.(type) {
	case nil:
		*j = nil
		return nil
	case string:
		data = []byte(value)
	case []byte:
		data = value
	case int64, float64:
		// A declared JSON column has NUMERIC affinity in SQLite. Numeric JSON
		// scalars may therefore be returned as numbers rather than strings.
		var err error
		data, err = json.Marshal(value)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("sqlite.JSON: cannot scan %T", value)
	}
	if !j.valid(data) {
		return fmt.Errorf("sqlite.JSON: invalid JSON")
	}
	*j = append((*j)[:0], data...)
	return nil
}

func (j JSON) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	if !j.valid(j) {
		return nil, fmt.Errorf("sqlite.JSON: invalid JSON")
	}
	return []byte(j), nil
}

func (j *JSON) UnmarshalJSON(data []byte) error {
	if j == nil {
		return fmt.Errorf("sqlite.JSON: UnmarshalJSON on nil receiver")
	}
	if !j.valid(data) {
		return fmt.Errorf("sqlite.JSON: invalid JSON")
	}
	*j = append((*j)[:0], data...)
	return nil
}

func (JSON) GormDataType() string { return "json" }

func (JSON) GormDBDataType(_ *gorm.DB, field *schema.Field) string {
	if typ := field.TagSettings["TYPE"]; typ != "" {
		return typ
	}
	return "JSON"
}
