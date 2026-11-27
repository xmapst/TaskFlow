package models

import "uuid"

type STaskEnv struct {
	SBase
	TaskID uuid.UUID `gorm:"not null;index:,unique,composite:owner_name,priority:1"`
	Task   *STask    `gorm:"foreignKey:TaskID;references:ID;constraint:OnDelete:CASCADE"`
	SEnv
}

func (*STaskEnv) TableName() string { return "t_task_env" }
