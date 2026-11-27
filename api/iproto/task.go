package iproto

import "time"

type STaskCreateRes struct {
	Name  string `json:"name" yaml:"name"`
	Count int64  `json:"count,omitempty" yaml:"count,omitempty"`
}

type STaskListDetailRes struct {
	Page  *SPageRes `json:"page" yaml:"page"`
	Tasks STasksRes `json:"tasks" yaml:"tasks"`
}

type STasksRes []*STaskRes

type STaskRes struct {
	Name    string        `json:"name,omitempty" yaml:"name,omitempty"`
	Workdir string        `json:"workdir,omitempty" yaml:"workdir,omitempty"`
	State   string        `json:"state" yaml:"state"`
	Count   int64         `json:"count,omitempty" yaml:"count,omitempty"`
	Timeout time.Duration `json:"timeout,omitzero" yaml:"timeout,omitempty" swaggertype:"integer" format:"int64"`
	Cleanup bool          `json:"cleanup" yaml:"cleanup"`
	Message string        `json:"message" yaml:"message"`
	Env     SEnvs         `json:"env,omitempty" yaml:"env,omitempty"`
	Time    *STimeRes     `json:"time,omitempty" yaml:"time,omitempty"`
}

type STaskReq struct {
	Workdir string        `json:"workdir,omitempty" yaml:"workdir,omitempty"`
	Timeout time.Duration `json:"timeout,omitzero" yaml:"timeout,omitempty" swaggertype:"integer" format:"int64"`
	Cleanup bool          `json:"cleanup,omitempty" yaml:"cleanup,omitempty"`
	Env     SEnvs         `json:"env,omitempty" yaml:"env,omitempty"`
	Step    SStepsReq     `json:"step,omitempty" yaml:"step,omitempty" validate:"required"`
}
