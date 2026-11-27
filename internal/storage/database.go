package storage

import (
	"fmt"
	"net/url"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/internal/storage/models"
)

// Store provides database-level operations and scoped persistence handles.
type Store struct {
	db *gorm.DB
}

// SQLiteDSN adds the application's SQLite tuning defaults for the modernc
// driver. It leaves URI options such as mode/cache intact. Explicit tuning
// in the supplied DSN takes precedence; use xsqlite.Open to also apply its
// foreign-key and time handling defaults.
func SQLiteDSN(dsn string) string {
	name, options, _ := strings.Cut(dsn, "?")
	params, _ := url.ParseQuery(options) // the driver reports malformed queries
	defaults := make(url.Values)
	for _, option := range []struct{ name, value, shorthand, alias string }{
		{"busy_timeout", "5000", "_busy_timeout", "_timeout"},
		{"journal_mode", "WAL", "_journal_mode", "_journal"},
		{"synchronous", "NORMAL", "_synchronous", "_sync"},
		{"journal_size_limit", "104857600", "", ""},
		{"cache_size", "-8000", "", ""},
		{"mmap_size", "134217728", "", ""},
		{"temp_store", "MEMORY", "", ""},
		{"locking_mode", "NORMAL", "", ""},
		{"cache_spill", "ON", "", ""},
	} {
		if option.shorthand != "" && (params.Has(option.shorthand) || params.Has(option.alias)) {
			continue
		}
		// Modernc may reorder _pragma values, so do not send competing
		// settings and rely on a specific execution order.
		explicit := false
		for _, pragma := range params["_pragma"] {
			if strings.Contains(strings.ToLower(pragma), option.name) {
				explicit = true
				break
			}
		}
		if !explicit {
			defaults.Add("_pragma", option.name+"("+option.value+")")
		}
	}
	if len(defaults) == 0 {
		return dsn
	}
	result := name + "?" + defaults.Encode()
	if options != "" {
		result += "&" + options
	}
	return result
}

func (d *Store) checkSQLite() error {
	configured, ok := d.db.Dialector.(interface{ ConfiguresForeignKeys() bool })
	if !ok || !configured.ConfiguresForeignKeys() {
		return fmt.Errorf("storage: SQLite requires foreign_keys=ON on every connection; configure the SQLite dialector or use xsqlite.Config.ForeignKeysPerConnection for an independently configured pool")
	}
	// Check for an explicit override or misconfiguration, without treating a
	// PRAGMA on one connection as pool-wide initialization.
	var foreignKeys int
	if err := d.db.Raw("PRAGMA foreign_keys").Scan(&foreignKeys).Error; err != nil {
		return fmt.Errorf("storage: check SQLite foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("storage: SQLite foreign_keys is disabled; enable it on every connection")
	}
	return nil
}

func (d *Store) Name() string {
	return d.db.Name()
}

// FixDatabase recovers interrupted work. The caller must exclusively own the database.
func (d *Store) FixDatabase() error {
	const message = "execution failed due to system error"
	interrupted := []models.State{models.StatePending, models.StateRunning, models.StatePaused}
	return d.db.Transaction(func(tx *gorm.DB) error {
		tasks := tx.Model(&models.STask{}).Select("id").Where("state IN ?", interrupted)
		if err := tx.Model(&models.SStep{}).
			Where("task_id IN (?) AND state IN ?", tasks, []models.State{models.StateRunning, models.StatePaused}).
			Updates(map[string]any{"state": models.StateFailed, "code": api.ExecCodeSystemErr, "message": message}).Error; err != nil {
			return err
		}
		return tx.Model(&models.STask{}).Where("state IN ?", interrupted).
			Updates(map[string]any{"state": models.StateFailed, "message": message}).Error
	})
}

func (d *Store) Task(name string) *TaskStore {
	return &TaskStore{
		db:       d.db,
		taskName: name,
	}
}

func (d *Store) TaskCreate(task *models.STask) (err error) {
	err = d.db.Create(task).Error
	if err != nil {
		return fmt.Errorf("save task %d error: %s", task.ID, err)
	}
	return
}

func (d *Store) ReplayCreate(replay *models.SReplay) error {
	return d.db.Create(replay).Error
}

func (d *Store) ReplayImport(rows []models.SReplay) error {
	if len(rows) == 0 {
		return nil
	}
	return d.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "replay_id"}}, DoNothing: true}).CreateInBatches(&rows, 100).Error
}

func (d *Store) ReplayGet(id string) (row models.SReplay, err error) {
	err = d.db.Where("replay_id = ?", id).Take(&row).Error
	return
}

func (d *Store) ReplayList(offset, limit int64) ([]models.SReplay, int64, error) {
	var total int64
	var result []models.SReplay
	err := d.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.SReplay{}).Count(&total).Error; err != nil {
			return err
		}
		if offset >= total {
			return nil
		}
		return tx.Order("date DESC, replay_id DESC").Offset(int(offset)).Limit(int(limit)).Find(&result).Error
	})
	return result, total, err
}

func (d *Store) TaskCount(state models.State) (res int64) {
	if state != models.StateAll {
		d.db.Model(&models.STask{}).Distinct("DISTINCT name").Where("state = ?", state).Count(&res)
		return
	}
	d.db.Model(&models.STask{}).Distinct("DISTINCT name").Count(&res)
	return
}

func (d *Store) TaskList(page, pageSize int64, str string) (res models.STasks, total int64) {
	query := d.db.Model(&models.STask{})
	if str != "" {
		query = query.Where("name LIKE ?", str+"%")
	}
	if err := query.Count(&total).Error; err != nil {
		return
	}
	// Order by creation time before paginating; the primary key breaks ties.
	query.Select("id, name, state, cleanup, message, s_time, e_time").
		Order("created_at DESC, id DESC").Scopes(func(db *gorm.DB) *gorm.DB {
		return models.Paginate(db, page, pageSize)
	}).Find(&res)
	return
}
