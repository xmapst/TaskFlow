package runner

import (
	"context"

	"github.com/xmapst/taskflow/api"
)

type IRunner interface {
	Run(ctx context.Context) (exit api.ExecCode, err error)
	Clear() error
}
