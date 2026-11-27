package api

import (
	"errors"
)

var ServiceName = "taskflow"

type ExecCode int64

const (
	ExecCodeSuccess   ExecCode = 0
	ExecCodeFailed    ExecCode = -1
	ExecCodeSkipped   ExecCode = -2
	ExecCodeKilled    ExecCode = -997
	ExecCodeTimeout   ExecCode = -998
	ExecCodeSystemErr ExecCode = -999
)

func (e ExecCode) Int64() int64 {
	return int64(e)
}

var (
	ErrExecTimeOut = errors.New("forced termination by timeout")
	ErrExecKilled  = errors.New("artificial force kill")
)

// These markers delimit console output in stored step logs.
const (
	ExecConsoleStart = "TASK_FLOW::CONSOLE::START"
	ExecConsoleDone  = "TASK_FLOW::CONSOLE::DONE"
)
