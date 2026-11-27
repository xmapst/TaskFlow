package iproto

import (
	"errors"
	"time"

	"github.com/tidwall/gjson"
)

// SStepOptions holds executor-specific settings as raw JSON. The selected
// executor validates the keys and values it accepts. For example, xexec
// accepts shell, and container accepts image, shell, and mounts.
type SStepOptions []byte

// Object returns the options as a JSON object. No options at all is the same as
// an empty object, so a step that configures nothing needs no special case.
func (o SStepOptions) Object() (gjson.Result, error) {
	if len(o) == 0 {
		return gjson.Result{}, nil
	}
	if !gjson.ValidBytes(o) {
		return gjson.Result{}, errors.New("options is not valid JSON")
	}
	raw := gjson.ParseBytes(o)
	// An explicit null means "nothing configured", the same as no options.
	if raw.Type == gjson.Null {
		return gjson.Result{}, nil
	}
	if !raw.IsObject() {
		return gjson.Result{}, errors.New("options must be a JSON object")
	}
	return raw, nil
}

// MarshalJSON returns the stored JSON, or null if no options are set.
func (o SStepOptions) MarshalJSON() ([]byte, error) {
	if len(o) == 0 {
		return []byte("null"), nil
	}
	if !gjson.ValidBytes(o) {
		return nil, errors.New("options is not valid JSON")
	}
	return o, nil
}

// UnmarshalJSON keeps the raw JSON, so the step stores what the client sent
// rather than a re-encoded copy.
func (o *SStepOptions) UnmarshalJSON(data []byte) error {
	if !gjson.ValidBytes(data) {
		return errors.New("options is not valid JSON")
	}
	*o = append((*o)[:0], data...)
	return nil
}

type SStepRes struct {
	Name        string        `json:"name" yaml:"name"`
	State       string        `json:"state" yaml:"state"`
	Code        int64         `json:"code" yaml:"code"`
	Desc        string        `json:"desc,omitempty" yaml:"desc,omitempty"`
	Timeout     time.Duration `json:"timeout,omitzero" yaml:"timeout,omitempty" swaggertype:"integer" format:"int64"`
	Routing     *SStepRouting `json:"routing,omitempty" yaml:"routing,omitempty"`
	Message     string        `json:"message" yaml:"message"`
	Env         SEnvs         `json:"env,omitempty" yaml:"env,omitempty"`
	Kind        string        `json:"kind,omitempty" yaml:"kind,omitempty"`
	Options     SStepOptions  `json:"options,omitempty" yaml:"options,omitempty" description:"执行器私有参数，JSON 对象：xexec 支持 shell；container 支持 image、shell、mounts" swaggertype:"object"`
	Content     string        `json:"content,omitempty" yaml:"content,omitempty"`
	RetryPolicy *SRetryPolicy `json:"retryPolicy,omitempty" yaml:"retryPolicy,omitempty"`
	Time        *STimeRes     `json:"time,omitempty" yaml:"time,omitempty"`
}

type SStepsRes []*SStepRes

type SStepReq struct {
	Name        string        `json:"name,omitempty" yaml:"name,omitempty"`
	Desc        string        `json:"desc,omitempty" yaml:"desc,omitempty"`
	Timeout     time.Duration `json:"timeout,omitzero" yaml:"timeout,omitempty" swaggertype:"integer" format:"int64"`
	Routing     *SStepRouting `json:"routing,omitempty" yaml:"routing,omitempty"`
	Env         SEnvs         `json:"env,omitempty" yaml:"env,omitempty"`
	Kind        string        `json:"kind,omitempty" yaml:"kind,omitempty" validate:"required"`
	Options     SStepOptions  `json:"options,omitempty" yaml:"options,omitempty" description:"执行器私有参数，JSON 对象：xexec 支持 shell；container 支持 image、shell、mounts" swaggertype:"object"`
	Content     string        `json:"content,omitempty" yaml:"content,omitempty" validate:"required"`
	RetryPolicy *SRetryPolicy `json:"retryPolicy,omitempty" yaml:"retryPolicy,omitempty"`
}

// SStepRouting configures labeled edges, their join policy, and labels emitted on success.
type SStepRouting struct {
	Edges []SStepEdge `json:"edges,omitempty" yaml:"edges,omitempty"`
	Join  string      `json:"join,omitempty" yaml:"join,omitempty" enums:"all,any"`
	Emit  []string    `json:"emit,omitempty" yaml:"emit,omitempty"`
}

type SStepEdge struct {
	Step    string `json:"step" yaml:"step"`
	Trigger string `json:"trigger,omitempty" yaml:"trigger,omitempty"`
}

type SRetryPolicy struct {
	Interval    time.Duration `json:"interval" yaml:"interval" description:"间隔时间" swaggertype:"integer" format:"int64"`
	MaxInterval time.Duration `json:"maxInterval" yaml:"maxInterval" description:"最大间隔时间" swaggertype:"integer" format:"int64"`
	MaxAttempts int64         `json:"maxAttempts" yaml:"maxAttempts" description:"最大尝试次数"`
	Multiplier  float64       `json:"multiplier" yaml:"multiplier" description:"乘数"`
	Jitter      float64       `json:"jitter,omitempty" yaml:"jitter,omitempty" minimum:"0" maximum:"1" description:"重试退避抖动"`
}

type SStepsReq []*SStepReq

type SStepLogRes struct {
	Timestamp int64  `json:"timestamp" yaml:"timestamp"`
	Line      int64  `json:"line" yaml:"line"`
	Content   string `json:"content" yaml:"content"`
}

type SStepLogsRes []*SStepLogRes
