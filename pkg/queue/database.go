package queue

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// retryAfter bounds reservations; handlers outliving it may run concurrently,
// so all handlers must be idempotent.
var retryAfter = 60 * time.Second

func SetReservationTimeout(d time.Duration) {
	if d <= 0 {
		d = 60 * time.Second
	}
	retryAfter = d
}

type jobRow struct {
	ID          string    `gorm:"primaryKey;size:36"`
	Name        string    `gorm:"size:64;not null"`
	Payload     []byte    `gorm:"type:text;not null"`
	Attempts    int       `gorm:"not null;default:0"`
	AvailableAt time.Time `gorm:"not null;index"`
	ReservedAt  *time.Time
	CreatedAt   time.Time
}

func (jobRow) TableName() string { return "jobs" }

type databaseQueue struct {
	db    *gorm.DB
	tries int
	now   func() time.Time
}

func NewDatabase(db *gorm.DB, tries int) Queue {
	if tries < 1 {
		tries = 1
	}
	return &databaseQueue{db: db, tries: tries, now: time.Now}
}

func (q *databaseQueue) Push(ctx context.Context, name string, payload []byte) (string, error) {
	id := uuidString()
	row := jobRow{ID: id, Name: name, Payload: payload, AvailableAt: q.now()}
	if err := q.db.WithContext(ctx).Create(&row).Error; err != nil {
		return "", err
	}
	return id, nil
}

func (q *databaseQueue) Reserve(ctx context.Context) (Job, bool, error) {
	var row jobRow
	now := q.now()
	err := q.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("available_at <= ? AND (reserved_at IS NULL OR reserved_at < ?)", now, now.Add(-retryAfter)).
			Order("available_at ASC, id ASC").
			First(&row).Error; err != nil {
			return err
		}
		return tx.Model(&jobRow{}).Where("id = ?", row.ID).
			Updates(map[string]interface{}{"attempts": row.Attempts + 1, "reserved_at": now}).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Job{}, false, nil
		}
		return Job{}, false, err
	}
	row.Attempts++
	return Job{ID: row.ID, Name: row.Name, Payload: row.Payload, Attempts: row.Attempts, AvailableAt: row.AvailableAt}, true, nil
}

func (q *databaseQueue) Ack(ctx context.Context, id string) error {
	return q.db.WithContext(ctx).Where("id = ?", id).Delete(&jobRow{}).Error
}

func (q *databaseQueue) Fail(ctx context.Context, job Job, _ error) error {
	if job.Attempts >= q.tries {
		if err := q.db.WithContext(ctx).Where("id = ?", job.ID).Delete(&jobRow{}).Error; err != nil {
			return err
		}
		return ErrJobBuried
	}
	return q.db.WithContext(ctx).Model(&jobRow{}).Where("id = ?", job.ID).
		Updates(map[string]interface{}{
			"available_at": q.now().Add(retryDelay(job.Attempts)),
			"reserved_at":  nil,
		}).Error
}
