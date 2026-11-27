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

type sTouchRunner struct {
	storage   *storage.StepStore
	workspace string
}

func newTouchRunner(stg *storage.StepStore, workspace, _ string) (IRunner, error) {
	return &sTouchRunner{
		storage:   stg,
		workspace: workspace,
	}, nil
}

func (t *sTouchRunner) Run(ctx context.Context) (exit api.ExecCode, err error) {
	if ctx.Err() != nil {
		return api.ExecCodeKilled, api.ErrExecKilled
	}
	content, err := t.storage.Content()
	if err != nil {
		return api.ExecCodeSystemErr, err
	}
	var config iproto.TouchConfig
	if err = yaml.Unmarshal([]byte(content), &config); err != nil {
		return api.ExecCodeSystemErr, err
	}
	if config.Path == "" {
		return api.ExecCodeSystemErr, fmt.Errorf("path is empty")
	}
	if config.Path, err = localPath(config.Path); err != nil {
		return api.ExecCodeSystemErr, err
	}
	root, err := openWorkdir(t.workspace)
	if err != nil {
		return api.ExecCodeSystemErr, err
	}
	defer func() { _ = root.Close() }()
	flag := os.O_CREATE | os.O_APPEND | os.O_WRONLY
	if config.Overwrite {
		t.storage.Log().Writef("overwrite %s", config.Path)
		flag = os.O_CREATE | os.O_TRUNC | os.O_WRONLY
	} else {
		t.storage.Log().Writef("create or append %s", config.Path)
	}
	file, err := root.OpenFile(config.Path, flag, os.ModePerm)
	if err != nil {
		return api.ExecCodeSystemErr, err
	}
	_, err = file.WriteString(config.Content)
	if cerr := file.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return api.ExecCodeSystemErr, err
	}
	return api.ExecCodeSuccess, nil
}

func (t *sTouchRunner) Clear() error {
	return nil
}
