package models

import (
	"math"

	"gorm.io/gorm"
)

func Paginate(db *gorm.DB, page, pageSize int64) *gorm.DB {
	if page == -1 {
		return db
	}
	if page < 1 {
		page = 1
	}
	switch {
	case pageSize > 500:
		pageSize = 500
	case pageSize <= 0:
		pageSize = 10
	}
	// Saturate rather than allowing a huge page to wrap into the first page.
	maxOffset := int64(math.MaxInt)
	offset := maxOffset
	if page-1 <= maxOffset/pageSize {
		offset = (page - 1) * pageSize
	}
	return db.Offset(int(offset)).Limit(int(pageSize))
}
