package iproto

const (
	XTaskName  = "X-Task-Name"
	XTaskState = "X-Task-STATE"
)

type SPageRes struct {
	Current int64 `json:"current" yaml:"current"`
	Size    int64 `json:"size" yaml:"size"`
	Total   int64 `json:"total" yaml:"total"`
}

type SPageReq struct {
	Page   int64  `json:"page" form:"page" query:"page" yaml:"page"`
	Size   int64  `json:"size" form:"size" query:"size" yaml:"size"`
	Prefix string `json:"prefix" form:"prefix" query:"prefix" yaml:"prefix"`
}

type STimeRes struct {
	Start string `json:"start,omitempty" yaml:"start,omitempty"`
	End   string `json:"end,omitempty" yaml:"end,omitempty"`
}

type SEnvs []*SEnv

type SEnv struct {
	Name   string `json:"name" yaml:"name" validate:"required"`
	Value  string `json:"value" yaml:"value"`
	Secret bool   `json:"secret,omitzero" yaml:"secret,omitempty"`
}
