package api

import (
	"encoding/json/v2"
	"time"
)

// JSONDuration is the JSON option set of the HTTP API for time.Duration: it is
// written as integer nanoseconds and read as either nanoseconds or a Go
// duration string such as "30s". Pass it to json.Marshal and json.Unmarshal
// when encoding requests or decoding responses that carry durations.
var JSONDuration = json.JoinOptions(
	json.WithMarshalers(json.MarshalFunc(func(value time.Duration) ([]byte, error) {
		return json.Marshal(int64(value))
	})),
	json.WithUnmarshalers(json.UnmarshalFunc(func(data []byte, value *time.Duration) error {
		if len(data) > 0 && data[0] == '"' {
			var text string
			if err := json.Unmarshal(data, &text); err != nil {
				return err
			}
			duration, err := time.ParseDuration(text)
			if err != nil {
				return err
			}
			*value = duration
			return nil
		}
		return json.Unmarshal(data, (*int64)(value))
	})),
)
