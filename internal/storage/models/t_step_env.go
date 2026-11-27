package models

import "uuid"

type SStepEnv struct {
	SBase
	StepID uuid.UUID `gorm:"not null;index:,unique,composite:owner_name,priority:1"`
	Step   *SStep    `gorm:"foreignKey:StepID;references:ID;constraint:OnDelete:CASCADE"`
	SEnv
}

func (*SStepEnv) TableName() string { return "t_step_env" }
