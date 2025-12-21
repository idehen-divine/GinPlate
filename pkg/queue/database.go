package queue

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// retryAfter bounds how long a reservation may run before another worker
// may take the job. It must exceed any plausible handler runtime; crashed
// workers' jobs become eligible again after this window instead of
// sticking forever.
const retryAfter = 60 * time.Second

// jobRow maps the jobs migration table. Attempts counts pops including the
// current one; a NULL reserved_at means never reserved or released back.
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

// databaseQueue is a Queue over the jobs table. Reservation takes the
// oldest due job under SKIP LOCKED so concurrent workers never share one.
type databaseQueue struct {
	db    *gorm.DB
	tries int
	now   func() time.Time
}

// NewDatabase returns a table-backed Queue. tries caps total runs (min 1).
func NewDatabase(db *gorm.DB, tries int) Queue {
	if tries < 1 {
		tries = 1
	}
	return &databaseQueue{db: db, tries: tries, now: time.Now}
}

// Push inserts a first-attempt job due immediately.
func (q *databaseQueue) Push(ctx context.Context, name string, payload []byte) (string, error) {
	id := uuidString()
	row := jobRow{ID: id, Name: name, Payload: payload, AvailableAt: q.now()}
	if err := q.db.WithContext(ctx).Create(&row).Error; err != nil {
		return "", err
	}
	return id, nil
}

// Reserve takes the oldest due, unreserved-or-stale job and counts the pop.
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

// Ack deletes a finished job.
func (q *databaseQueue) Ack(ctx context.Context, id string) error {
	return q.db.WithContext(ctx).Where("id = ?", id).Delete(&jobRow{}).Error
}

// Fail requeues with backoff and releases the reservation, or deletes past
// max attempts and reports ErrJobBuried so the worker logs it distinctly.
func (q *databaseQueue) Fail(ctx context.Context, job Job, _ error) error {
	if job.Attempts >= q.tries {
		if err := q.db.WithContext(ctx).Where("id = ?", job.ID).Delete(&jobRow{}).Error; err != nil {
			return err
		}
		return ErrJobBuried
	}
	return q.db.WithContext(ctx).Where("id = ?", job.ID).
		Updates(map[string]interface{}{
			"available_at": q.now().Add(retryDelay(job.Attempts)),
			"reserved_at":  nil,
		}).Error
}
