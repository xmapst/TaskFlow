package runner

import (
	"context"
	"fmt"
	"os"

	"sigs.k8s.io/yaml"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/api/iproto"
	"github.com/xmapst/taskflow/internal/storage"
)

type sMkdirRunner struct {
	storage   *storage.StepStore
	workspace string
}

func newMkdirRunner(stg *storage.StepStore, workspace, _ string) (IRunner, error) {
	return &sMkdirRunner{
		storage:   stg,
		workspace: workspace,
	}, nil
}

func (m *sMkdirRunner) Run(ctx context.Context) (exit api.ExecCode, err error) {
	if ctx.Err() != nil {
		return api.ExecCodeKilled, api.ErrExecKilled
	}
	content, err := m.storage.Content()
	if err != nil {
		return api.ExecCodeSystemErr, err
	}
	var config iproto.MkdirConfig
	if err = yaml.Unmarshal([]byte(content), &config); err != nil {
		return api.ExecCodeSystemErr, err
	}
	if config.Path == "" {
		return api.ExecCodeSystemErr, fmt.Errorf("path is empty")
	}
	if config.Path, err = localPath(config.Path); err != nil {
		return api.ExecCodeSystemErr, err
	}
	root, err := openWorkdir(m.workspace)
	if err != nil {
		return api.ExecCodeSystemErr, err
	}
	defer func() { _ = root.Close() }()
	m.storage.Log().Writef("mkdir -p %s", config.Path)
	if err = root.MkdirAll(config.Path, os.ModePerm); err != nil {
		return api.ExecCodeSystemErr, err
	}
	return api.ExecCodeSuccess, nil
}

func (m *sMkdirRunner) Clear() error {
	return nil
}
