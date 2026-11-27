package iproto

import (
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"uuid"
)

// TaskSubmission identifies the persisted run to enqueue for execution.
type TaskSubmission struct {
	Name string
	ID   uuid.UUID
}

// TaskControl targets a specific run, not a replacement task with the same name.
type TaskControl struct {
	Name     string
	ID       uuid.UUID
	Step     string
	Action   string
	Duration string
}

// ApprovalRequest identifies one invocation, not just a task or step name.
type ApprovalRequest struct {
	ID       string `json:"id"`
	TaskName string `json:"task_name"`
	StepName string `json:"step_name"`
}

// ApprovalCreate is the body sent to the external approval platform. Besides
// the invocation identity it tells the platform where and how to report the
// decision: POST/PUT Callback.URL with an ApprovalDecision carrying the same
// identity and one of the two statuses.
type ApprovalCreate struct {
	ApprovalRequest
	Callback ApprovalCallbackConfig `json:"callback"`
}

type ApprovalDecision struct {
	ApprovalRequest
	Status string `json:"status"`
}

func DecodeApproval(data []byte) (ApprovalDecision, error) {
	var message ApprovalDecision
	if err := json.Unmarshal(data, &message, json.RejectUnknownMembers(true)); err != nil {
		return message, err
	}
	if len(message.ID) != 32 || message.TaskName == "" || message.StepName == "" || message.Status == "" {
		return message, errors.New("approval decision requires id, task_name, step_name and status")
	}
	if _, err := hex.DecodeString(message.ID); err != nil {
		return message, err
	}
	return message, nil
}

type WorkerPoolMessage struct {
	Size int `json:"size"`
}

func (m WorkerPoolMessage) Validate() error {
	if m.Size < 1 || m.Size > 150 {
		return errors.New("size must be an integer between 1 and 150")
	}
	return nil
}

// WorkerPoolSizeQuery requests the currently applied pool size.
type WorkerPoolSizeQuery struct{}
