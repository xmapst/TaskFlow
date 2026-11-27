package models

import "uuid"

type SStepLog struct {
	SBase
	StepID    uuid.UUID `json:"step_id" gorm:"not null;uniqueIndex:idx_step_log_line"`
	Step      *SStep    `json:"-" gorm:"foreignKey:StepID;references:ID;constraint:OnDelete:CASCADE"`
	Timestamp int64     `json:"timestamp,omitempty" gorm:"not null;comment:时间戳"`
	Line      *int64    `json:"line,omitempty" gorm:"not null;uniqueIndex:idx_step_log_line;comment:行号"`
	Content   string    `json:"content,omitempty" gorm:"comment:内容"`
}

func (l *SStepLog) TableName() string {
	return "t_step_log"
}

type SStepLogs []*SStepLog

func (l SStepLogs) Len() int {
	return len(l)
}

func (l SStepLogs) Less(i, j int) bool {
	if l[i].Line == nil || l[j].Line == nil {
		return l[i].Timestamp < l[j].Timestamp
	}
	return *l[i].Line < *l[j].Line
}

func (l SStepLogs) Swap(i, j int) {
	l[i], l[j] = l[j], l[i]
}
