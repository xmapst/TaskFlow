package iproto

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
)

// MkdirConfig defines the content of a mkdir step.
type MkdirConfig struct {
	Path string `json:"path"`
}

// TouchConfig defines the content of a touch step.
type TouchConfig struct {
	Path      string `json:"path"`
	Overwrite bool   `json:"overwrite"`
	Content   string `json:"content"`
}

// ApprovalConfig defines the content of an approval step.
type ApprovalConfig struct {
	Request struct {
		URL     string                    `json:"url"`
		Method  string                    `json:"method"`
		Headers map[string]ApprovalScalar `json:"headers"`
		Query   map[string]ApprovalScalar `json:"query"`
	} `json:"request"`
	Callback ApprovalCallbackConfig `json:"callback"`
}

// ApprovalCallbackConfig defines where and how approval decisions are reported.
type ApprovalCallbackConfig struct {
	URL      string `json:"url"`
	Approved string `json:"approved"`
	Rejected string `json:"rejected"`
}

// ApprovalScalar is a header or query value encoded as text on the wire. Keep numeric literals
// as text to avoid rounding identifiers larger than an IEEE-754 integer.
type ApprovalScalar string

func (s *ApprovalScalar) UnmarshalJSON(raw []byte) error {
	switch jsontext.Value(raw).Kind() {
	case jsontext.KindString:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		*s = ApprovalScalar(value)
	case jsontext.KindNumber, jsontext.KindTrue, jsontext.KindFalse:
		*s = ApprovalScalar(raw)
	default:
		return errors.New("expected a string, number or boolean")
	}
	return nil
}
