package queue

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// FailedJob is one buried job: what it was, what it carried, and the last
// error that killed it. Connection names the broker that buried it; Queue
// is 'default' until named queues exist (kept for Laravel parity).
type FailedJob struct {
	ID         string
	Connection string
	Queue      string
	Name       string
	Payload    []byte
	Exception  string
	Attempts   int
	FailedAt   time.Time
}

// FailedStore persists buried jobs for inspection and retry. A nil store
// means record-less burial (log only), for deployments without a database.
type FailedStore interface {
	Record(ctx context.Context, job FailedJob) error
	List(ctx context.Context, limit int) ([]FailedJob, error)
	Get(ctx context.Context, id string) (FailedJob, error)
	Delete(ctx context.Context, ids ...string) error
	Flush(ctx context.Context) error
}

// failedRow maps the failed_jobs migration table.
type failedRow struct {
	ID         string    `gorm:"primaryKey;size:36"`
	Connection string    `gorm:"size:32;not null"`
	Queue      string    `gorm:"size:64;not null;default:default"`
	Name       string    `gorm:"size:64;not null"`
	Payload    []byte    `gorm:"type:text;not null"`
	Exception  string    `gorm:"type:text;not null"`
	Attempts   int       `gorm:"not null;default:0"`
	FailedAt   time.Time `gorm:"not null;index"`
}

func (failedRow) TableName() string { return "failed_jobs" }

func toRow(job FailedJob) *failedRow {
	if job.ID == "" {
		job.ID = uuid.NewString()
	}
	if job.Queue == "" {
		job.Queue = "default"
	}
	if job.FailedAt.IsZero() {
		job.FailedAt = time.Now()
	}
	return &failedRow{
		ID: job.ID, Connection: job.Connection, Queue: job.Queue,
		Name: job.Name, Payload: job.Payload, Exception: job.Exception,
		Attempts: job.Attempts, FailedAt: job.FailedAt,
	}
}

func fromRow(row failedRow) FailedJob {
	return FailedJob{
		ID: row.ID, Connection: row.Connection, Queue: row.Queue,
		Name: row.Name, Payload: row.Payload, Exception: row.Exception,
		Attempts: row.Attempts, FailedAt: row.FailedAt,
	}
}

// databaseFailedStore is a FailedStore over the failed_jobs table.
type databaseFailedStore struct {
	db *gorm.DB
}

// NewDatabaseFailedStore returns a table-backed FailedStore. The table
// comes from the create_failed_jobs_table migration; Record does not
// create it.
func NewDatabaseFailedStore(db *gorm.DB) FailedStore {
	return &databaseFailedStore{db: db}
}

func (s *databaseFailedStore) Record(ctx context.Context, job FailedJob) error {
	return s.db.WithContext(ctx).Create(toRow(job)).Error
}

func (s *databaseFailedStore) List(ctx context.Context, limit int) ([]FailedJob, error) {
	if limit < 1 {
		limit = 100
	}
	var rows []failedRow
	if err := s.db.WithContext(ctx).Order("failed_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	jobs := make([]FailedJob, 0, len(rows))
	for _, row := range rows {
		jobs = append(jobs, fromRow(row))
	}
	return jobs, nil
}

func (s *databaseFailedStore) Get(ctx context.Context, id string) (FailedJob, error) {
	var row failedRow
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return FailedJob{}, err
	}
	return fromRow(row), nil
}

func (s *databaseFailedStore) Delete(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Where("id IN ?", ids).Delete(&failedRow{}).Error
}

func (s *databaseFailedStore) Flush(ctx context.Context) error {
	return s.db.WithContext(ctx).Where("1 = 1").Delete(&failedRow{}).Error
}

// OnBuried runs when a job exhausts its retries, carrying the handler's
// last error. The failed-jobs recorder uses it; pass nil (or nothing) to
// keep log-only burial.
type OnBuried func(job Job, cause error)

// RecordHook returns an OnBuried hook persisting every burial with its
// exception. Unknown-name burials record too (cause names the missing
// handler) — that forensics is the point.
func RecordHook(store FailedStore, connection string, logf func(format string, args ...interface{})) OnBuried {
	return func(job Job, cause error) {
		exception := "<unknown>"
		if cause != nil {
			exception = cause.Error()
		}
		err := store.Record(context.Background(), FailedJob{
			Connection: connection,
			Name:       job.Name,
			Payload:    job.Payload,
			Exception:  exception,
			Attempts:   job.Attempts,
		})
		if err != nil && logf != nil {
			logf("queue: record buried job %s: %v", job.ID, err)
		}
	}
}
