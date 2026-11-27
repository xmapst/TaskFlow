package models

// SReplay records the database identity of a finalized terminal
// recording and the file containing its cast data. Keeping this mapping in the
// database allows replay listings to be paginated without scanning the replay
// directory on every request.
type SReplay struct {
	SBase
	Date     string `json:"date" gorm:"size:10;not null;index:idx_replay_order,priority:1;comment:录像日期"`
	ReplayID string `json:"replay_id" gorm:"size:36;not null;uniqueIndex;index:idx_replay_order,priority:2;comment:录像标识"`
	FilePath string `json:"file_path" gorm:"size:1024;not null;comment:录像文件相对路径"`
	Size     int64  `json:"size" gorm:"not null;comment:压缩文件大小"`
}

func (r *SReplay) TableName() string {
	return "t_replay"
}
