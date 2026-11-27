package models

import (
	"time"
	"uuid"

	"gorm.io/gorm"
)

type State int

const (
	StateStopped State = iota // 成功
	StateRunning              // 运行
	StateFailed               // 失败
	StateUnknown              // 未知
	StatePending              // 等待
	StatePaused               // 挂起
	StateSkipped              // 跳过
	StateAll     State = -1
)

// OrUnknown returns the pointed-to state, or StateUnknown if s is nil.
func (s *State) OrUnknown() State {
	if s == nil {
		return StateUnknown
	}
	return *s
}

var StateMap = map[State]string{
	StateStopped: "stopped",
	StateRunning: "running",
	StateFailed:  "failed",
	StateUnknown: "unknown",
	StatePending: "pending",
	StatePaused:  "paused",
	StateSkipped: "skipped",
}

type SBase struct {
	ID        uuid.UUID `json:"id" gorm:"primarykey;comment:ID"`
	CreatedAt time.Time `json:"created_at" gorm:"comment:创建时间"`
	UpdatedAt time.Time `json:"updated_at" gorm:"comment:更新时间"`
}

func (b *SBase) BeforeCreate(tx *gorm.DB) (err error) {
	b.ID = uuid.NewV7()
	return
}

type SEnvs []*SEnv

type SEnv struct {
	Name   string `json:"name,omitempty" gorm:"size:256;index:,unique,composite:owner_name,priority:2;not null;comment:名称"`
	Value  string `json:"value,omitempty" gorm:"size:256;comment:值"`
	Secret bool   `json:"secret,omitzero" gorm:"not null;default:false;comment:敏感信息"`
}
