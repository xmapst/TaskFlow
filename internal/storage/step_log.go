package storage

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/xmapst/taskflow/internal/storage/models"
)

// LogStore persists output for one step and supports incremental reads.
type LogStore struct {
	db      *gorm.DB
	stepIDs *gorm.DB

	lock sync.Mutex
}

func (l *LogStore) List(latestLine *int64) (res models.SStepLogs) {
	query := l.db.Model(&models.SStepLog{}).Where("step_id IN (?)", l.stepIDs).Order("line ASC")
	if latestLine != nil {
		// 如果 latestLine 不为空，只查询行号大于 latestLine 的日志
		query = query.Where("line > ?", latestLine).Limit(500)
	}
	query.Find(&res)
	return
}

func (l *LogStore) Insert(log *models.SStepLog) error {
	l.lock.Lock()
	defer l.lock.Unlock()
	return l.db.Transaction(func(tx *gorm.DB) error {
		var owner models.SStep
		if err := tx.Where("id IN (?)", l.stepIDs).Select("id").Take(&owner).Error; err != nil {
			return err
		}
		log.StepID = owner.ID
		var count int64
		if err := tx.Model(&models.SStepLog{}).Where("step_id = ?", log.StepID).Count(&count).Error; err != nil {
			return err
		}
		log.Line = new(count)
		return tx.Create(log).Error
	})
}

func (l *LogStore) Write(contents ...string) {
	if err := l.Insert(&models.SStepLog{
		Timestamp: time.Now().UnixNano(),
		Content:   strings.Join(contents, " "),
	}); err != nil {
		slog.Warn(
			"operation warning",
			slog.Any("error", err),
		)
	}
}

func (l *LogStore) Writef(format string, args ...any) {
	l.Write(fmt.Sprintf(format, args...))
}

func (l *LogStore) RemoveAll() error {
	return l.db.Where("step_id IN (?)", l.stepIDs).Delete(&models.SStepLog{}).Error
}
