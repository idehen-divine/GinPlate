package queue

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/idehen-divine/GinPlate/pkg/database"
	"github.com/redis/go-redis/v9"
)

// errTestBoom is the canned handler failure for retry/bury cases.
var errTestBoom = errors.New("boom")

// fakeJobQueue is a scripted Queue: scripted jobs drain in order, then it
// reports empty. acked and failed record outcomes for assertions.
type fakeJobQueue struct {
	jobs   []Job
	acked  []string
	failed []string
	buried []string
}

func (f *fakeJobQueue) Push(_ context.Context, name string, payload []byte) (string, error) {
	f.jobs = append(f.jobs, Job{ID: "fake", Name: name, Payload: payload, Attempts: 1})
	return "fake", nil
}

func (f *fakeJobQueue) Reserve(_ context.Context) (Job, bool, error) {
	if len(f.jobs) == 0 {
		return Job{}, false, nil
	}
	job := f.jobs[0]
	f.jobs = f.jobs[1:]
	return job, true, nil
}

func (f *fakeJobQueue) Ack(_ context.Context, id string) error {
	f.acked = append(f.acked, id)
	return nil
}

func (f *fakeJobQueue) Fail(_ context.Context, job Job, _ error) error {
	f.failed = append(f.failed, job.ID)
	return nil
}

// buryQueue is a Queue that buries everything: Fail always reports burial,
// driving the OnBuried path without a database.
type buryQueue struct{ fakeJobQueue }

func (q *buryQueue) Fail(_ context.Context, job Job, _ error) error { return ErrJobBuried }

// ackFailQueue fails every Ack to pin the ack-failure logging path.
type ackFailQueue struct{ fakeJobQueue }

func (q *ackFailQueue) Ack(_ context.Context, _ string) error { return errTestBoom }

// recordingFailedStore is a FailedStore keeping rows in memory.
type recordingFailedStore struct{ rows []FailedJob }

func (s *recordingFailedStore) Record(_ context.Context, job FailedJob) error {
	s.rows = append(s.rows, job)
	return nil
}

func (s *recordingFailedStore) List(_ context.Context, limit int) ([]FailedJob, error) {
	if limit > 0 && limit < len(s.rows) {
		return append([]FailedJob(nil), s.rows[:limit]...), nil
	}
	return append([]FailedJob(nil), s.rows...), nil
}

func (s *recordingFailedStore) Get(_ context.Context, id string) (FailedJob, error) {
	for _, row := range s.rows {
		if row.ID == id || row.Name == id {
			return row, nil
		}
	}
	return FailedJob{}, errors.New("failed job not found")
}

func (s *recordingFailedStore) Delete(_ context.Context, ids ...string) error {
	keep := s.rows[:0]
	for _, row := range s.rows {
		drop := false
		for _, id := range ids {
			if row.ID == id || row.Name == id {
				drop = true
				break
			}
		}
		if !drop {
			keep = append(keep, row)
		}
	}
	s.rows = keep
	return nil
}

func (s *recordingFailedStore) Flush(_ context.Context) error {
	s.rows = nil
	return nil
}

// reserveSoon polls Reserve until a job arrives or the deadline passes.
// MySQL TIMESTAMP has second precision, so a push late in a second can
// land fractionally in the future; workers poll in production, tests poll
// here instead of assuming instant visibility.
func reserveSoon(ctx context.Context, t *testing.T, q Queue) (Job, bool, error) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		job, ok, err := q.Reserve(ctx)
		if err != nil || ok {
			return job, ok, err
		}
		if time.Now().After(deadline) {
			return job, false, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// conformance exercises the Queue contract against any backend: push and
// reserve in FIFO order with attempt counting, empty reads, ack removal,
// retry-with-backoff on fail, and burial past max tries.
func conformance(t *testing.T, name string, open func(t *testing.T) Queue) {
	t.Helper()
	ctx := context.Background()

	t.Run(name+"/push-reserve-fifo", func(t *testing.T) {
		q := open(t)
		id1, err := q.Push(ctx, "a", []byte(`{"n":1}`))
		if err != nil || id1 == "" {
			t.Fatalf("push 1: %v %q", err, id1)
		}
		id2, err := q.Push(ctx, "b", []byte(`{"n":2}`))
		if err != nil || id2 == "" || id2 == id1 {
			t.Fatalf("push 2: %v %q", err, id2)
		}
		first, ok, err := reserveSoon(ctx, t, q)
		if err != nil || !ok || first.Name != "a" || first.Attempts != 1 {
			t.Fatalf("first reserve = %+v,%v,%v", first, ok, err)
		}
		second, ok, err := q.Reserve(ctx)
		if err != nil || !ok || second.Name != "b" {
			t.Fatalf("second reserve = %+v,%v,%v", first, ok, err)
		}
		if err := q.Ack(ctx, first.ID); err != nil {
			t.Fatal(err)
		}
		if err := q.Ack(ctx, second.ID); err != nil {
			t.Fatal(err)
		}
	})

	t.Run(name+"/empty", func(t *testing.T) {
		q := open(t)
		if _, ok, err := q.Reserve(ctx); err != nil || ok {
			t.Fatalf("empty reserve = %v,%v", ok, err)
		}
		// Nil UUID: valid shape on every backend (pgsql rejects malformed
		// UUID literals), guaranteed absent so the miss paths trigger.
		if err := q.Ack(ctx, "00000000-0000-0000-0000-000000000000"); err != nil {
			t.Fatalf("ack missing: %v", err)
		}
		if err := q.Fail(ctx, Job{ID: "00000000-0000-0000-0000-000000000000", Name: "x", Attempts: 99}, errTestBoom); err == nil {
			t.Fatal("fail past tries should bury")
		}
	})

	t.Run(name+"/retry-then-bury", func(t *testing.T) {
		q := open(t)
		id, err := q.Push(ctx, "flaky", []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		job, ok, err := reserveSoon(ctx, t, q)
		if err != nil || !ok || job.ID != id {
			t.Fatalf("reserve = %+v,%v,%v", job, ok, err)
		}
		if err := q.Fail(ctx, job, errTestBoom); err != nil {
			t.Fatalf("first fail should requeue: %v", err)
		}
		again, ok, err := reserveSoon(ctx, t, q)
		if err != nil || !ok || again.ID != id || again.Attempts != job.Attempts+1 {
			t.Fatalf("retry reserve = %+v,%v,%v", again, ok, err)
		}
		if err := q.Fail(ctx, again, errTestBoom); err == nil {
			t.Fatal("second fail should bury")
		}
	})
}

// zeroRetry runs fn with instantaneous retries so retry flows complete
// without waiting out real backoffs.
func zeroRetry(t *testing.T, fn func()) {
	t.Helper()
	old := retryDelay
	retryDelay = func(int) time.Duration { return 0 }
	t.Cleanup(func() { retryDelay = old })
	fn()
}

// TestQueue is the single entry point for every queue test: backoff math,
// sync inline dispatch, redis conformance + delayed-retry expiry, the
// live-gated database conformance, and worker dispatch/loop behavior.
func TestQueue(t *testing.T) {
	t.Run("backoff", func(t *testing.T) {
		if got := backoff(1); got != 30*time.Second {
			t.Fatalf("attempt 1 = %v", got)
		}
		if got := backoff(2); got != time.Minute {
			t.Fatalf("attempt 2 = %v", got)
		}
		if got := backoff(100); got != time.Hour {
			t.Fatalf("huge attempts = %v, want cap", got)
		}
		if got := backoff(0); got != 30*time.Second {
			t.Fatalf("attempt 0 = %v", got)
		}
	})

	// The sync driver dispatches inline on Push and never queues, so the
	// store contract does not apply; its behavior is pinned here instead.
	t.Run("sync/dispatch-inline", func(t *testing.T) {
		reg := NewRegistry()
		ran := make(chan string, 1)
		reg.Handle("hi", func(_ context.Context, job Job) error {
			ran <- string(job.Payload)
			return nil
		})
		q := NewSync(reg)
		id, err := q.Push(context.Background(), "hi", []byte("yo"))
		if err != nil || id == "" {
			t.Fatalf("push = %q,%v", id, err)
		}
		select {
		case got := <-ran:
			if got != "yo" {
				t.Fatalf("handler got %q", got)
			}
		default:
			t.Fatal("handler did not run inline")
		}
	})

	t.Run("sync/unknown-buried", func(t *testing.T) {
		q := NewSync(NewRegistry())
		if _, err := q.Push(context.Background(), "nope", nil); err != ErrJobBuried {
			t.Fatalf("push unknown = %v, want buried", err)
		}
	})

	t.Run("redis/conformance", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(mr.Close)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		zeroRetry(t, func() {
			conformance(t, "redis", func(t *testing.T) Queue {
				t.Helper()
				return NewRedis(rdb, "", 2)
			})
		})
	})

	t.Run("redis/expiry", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(mr.Close)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		old := retryDelay
		retryDelay = func(int) time.Duration { return 2 * time.Second }
		t.Cleanup(func() { retryDelay = old })
		s := NewRedis(rdb, "", 2)
		ctx := context.Background()
		// Baseline the broker clock: the driver schedules off server TIME,
		// so advancing miniredis's clock (not wall time) makes the retry due.
		t0, err := rdb.Time(ctx).Result()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Push(ctx, "x", []byte("y")); err != nil {
			t.Fatal(err)
		}
		job, ok, err := s.Reserve(ctx)
		if err != nil || !ok {
			t.Fatalf("reserve = %+v,%v,%v", job, ok, err)
		}
		if err := s.Fail(ctx, job, errTestBoom); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := s.Reserve(ctx); err != nil || ok {
			t.Fatalf("not-due retry visible: %v,%v", ok, err)
		}
		mr.SetTime(t0.Add(3 * time.Second))
		job, ok, err = s.Reserve(ctx)
		if err != nil || !ok || job.Attempts != 2 {
			t.Fatalf("due retry = %+v,%v,%v", job, ok, err)
		}
	})

	// Runs against a live database only when TEST_MYSQL_DSN or
	// TEST_PGSQL_DSN is set; otherwise it skips so CI stays hermetic.
	t.Run("database/conformance", func(t *testing.T) {
		dsn, driver := os.Getenv("TEST_MYSQL_DSN"), "mysql"
		if dsn == "" {
			dsn, driver = os.Getenv("TEST_PGSQL_DSN"), "pgsql"
		}
		if dsn == "" {
			t.Skip("set TEST_MYSQL_DSN or TEST_PGSQL_DSN for the live database queue test")
		}
		db, err := database.Connect(driver, dsn)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		create := `CREATE TABLE IF NOT EXISTS jobs (
			id CHAR(36) PRIMARY KEY,
			name VARCHAR(64) NOT NULL,
			payload TEXT NOT NULL,
			attempts INT NOT NULL DEFAULT 0,
			available_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			reserved_at TIMESTAMP(6) NULL DEFAULT NULL,
			created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			INDEX idx_jobs_available (available_at)
		)`
		if driver == "pgsql" {
			create = `CREATE TABLE IF NOT EXISTS jobs (
				id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
				name VARCHAR(64) NOT NULL,
				payload TEXT NOT NULL,
				attempts INT NOT NULL DEFAULT 0,
				available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
				reserved_at TIMESTAMPTZ,
				created_at TIMESTAMPTZ NOT NULL DEFAULT now()
			)`
		}
		if err := db.Exec(create).Error; err != nil {
			t.Fatal(err)
		}
		if driver == "pgsql" {
			if err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_jobs_available ON jobs(available_at)`).Error; err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() { _ = db.Exec(`DROP TABLE IF EXISTS jobs`).Error })
		zeroRetry(t, func() {
			conformance(t, "database", func(t *testing.T) Queue {
				t.Helper()
				if err := db.Exec(`DELETE FROM jobs`).Error; err != nil {
					t.Fatal(err)
				}
				return NewDatabase(db, 2)
			})
		})
	})

	t.Run("worker/dispatch-routes", func(t *testing.T) {
		ctx := context.Background()
		noop := func(format string, args ...interface{}) {}
		reg := NewRegistry()

		q := &fakeJobQueue{jobs: []Job{{ID: "u1", Name: "missing", Attempts: 1}}}
		Dispatch(ctx, q, reg, Job{ID: "u1", Name: "missing", Attempts: 1}, noop)
		if len(q.acked) != 1 || len(q.failed) != 0 {
			t.Fatalf("unknown should ack, got acked=%v failed=%v", q.acked, q.failed)
		}

		reg.Handle("boom", func(_ context.Context, _ Job) error { return errors.New("bang") })
		q = &fakeJobQueue{}
		Dispatch(ctx, q, reg, Job{ID: "f1", Name: "boom", Attempts: 1}, noop)
		if len(q.failed) != 1 || len(q.acked) != 0 {
			t.Fatalf("failure should fail, got acked=%v failed=%v", q.acked, q.failed)
		}

		reg.Handle("ok", func(_ context.Context, _ Job) error { return nil })
		q = &fakeJobQueue{}
		Dispatch(ctx, q, reg, Job{ID: "s1", Name: "ok", Attempts: 1}, noop)
		if len(q.acked) != 1 || len(q.failed) != 0 {
			t.Fatalf("success should ack, got acked=%v failed=%v", q.acked, q.failed)
		}
	})

	t.Run("worker/run-stops-on-cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		reg := NewRegistry()
		done := make(chan string, 1)
		reg.Handle("once", func(_ context.Context, job Job) error {
			done <- string(job.Payload)
			cancel()
			return nil
		})
		q := &fakeJobQueue{jobs: []Job{{ID: "w1", Name: "once", Payload: []byte("hi"), Attempts: 1}}}
		noop := func(format string, args ...interface{}) {}
		if err := Run(ctx, q, reg, noop); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-done:
			if got != "hi" {
				t.Fatalf("handler got %q", got)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("handler never ran")
		}
		if len(q.acked) != 1 {
			t.Fatalf("acked = %v", q.acked)
		}
	})

	t.Run("failed/record-on-bury", func(t *testing.T) {
		ctx := context.Background()
		noop := func(format string, args ...interface{}) {}
		reg := NewRegistry()
		reg.Handle("flaky", func(_ context.Context, _ Job) error { return errTestBoom })
		store := &recordingFailedStore{}
		q := &buryQueue{}
		DispatchBuried(ctx, q, reg, Job{ID: "b1", Name: "flaky", Payload: []byte(`{}`), Attempts: 3},
			noop, []OnBuried{RecordHook(store, "sync", noop)})
		if len(store.rows) != 1 {
			t.Fatalf("recorded = %+v", store.rows)
		}
		row := store.rows[0]
		if row.Name != "flaky" || row.Exception != "boom" || row.Attempts != 3 || row.Connection != "sync" {
			t.Fatalf("row = %+v", row)
		}
	})

	t.Run("failed/unknown-name-records", func(t *testing.T) {
		ctx := context.Background()
		noop := func(format string, args ...interface{}) {}
		store := &recordingFailedStore{}
		DispatchBuried(ctx, &buryQueue{}, NewRegistry(), Job{ID: "u9", Name: "ghost", Attempts: 1},
			noop, []OnBuried{RecordHook(store, "redis", noop)})
		if len(store.rows) != 1 || !strings.Contains(store.rows[0].Exception, "ghost") {
			t.Fatalf("recorded = %+v", store.rows)
		}
	})

	t.Run("failed/retry-round-trip", func(t *testing.T) {
		ctx := context.Background()
		store := &recordingFailedStore{}
		if err := store.Record(ctx, FailedJob{ID: "r1", Connection: "database", Name: "flaky", Payload: []byte(`{"n":1}`), Exception: "boom", Attempts: 3}); err != nil {
			t.Fatal(err)
		}
		row, err := store.Get(ctx, "r1")
		if err != nil {
			t.Fatal(err)
		}
		q := &fakeJobQueue{}
		if _, err := q.Push(ctx, row.Name, row.Payload); err != nil {
			t.Fatal(err)
		}
		if len(q.jobs) != 1 || string(q.jobs[0].Payload) != `{"n":1}` {
			t.Fatalf("re-pushed = %+v", q.jobs)
		}
		if err := store.Delete(ctx, row.ID); err != nil {
			t.Fatal(err)
		}
		if len(store.rows) != 0 {
			t.Fatalf("rows after delete = %+v", store.rows)
		}
	})

	t.Run("dispatch/ack-failure-logged", func(t *testing.T) {
		ctx := context.Background()
		var logged []string
		logf := func(format string, args ...interface{}) {
			logged = append(logged, format)
		}
		reg := NewRegistry()
		reg.Handle("ok", func(_ context.Context, _ Job) error { return nil })
		q := &ackFailQueue{fakeJobQueue: fakeJobQueue{jobs: []Job{{ID: "a1", Name: "ok", Attempts: 1}}}}
		Dispatch(ctx, q, reg, Job{ID: "a1", Name: "ok", Attempts: 1}, logf)
		found := false
		for _, m := range logged {
			if strings.Contains(m, "ack job") {
				found = true
			}
		}
		if !found {
			t.Fatalf("ack failure not logged: %v", logged)
		}
	})

	t.Run("dispatch/unknown-name-buried", func(t *testing.T) {
		ctx := context.Background()
		noop := func(format string, args ...interface{}) {}
		q := &fakeJobQueue{}
		Dispatch(ctx, q, NewRegistry(), Job{ID: "u1", Name: "ghost", Attempts: 1}, noop)
		if len(q.acked) != 1 {
			t.Fatalf("unknown job not acked: %+v", q.acked)
		}
	})

	t.Run("reserve-timeout-configurable", func(t *testing.T) {
		old := retryAfter
		t.Cleanup(func() { retryAfter = old })
		SetReservationTimeout(time.Minute)
		if retryAfter != time.Minute {
			t.Fatalf("retryAfter = %v", retryAfter)
		}
		SetReservationTimeout(0)
		if retryAfter != 60*time.Second {
			t.Fatalf("reset retryAfter = %v", retryAfter)
		}
	})

	// Runs against a live database only when TEST_MYSQL_DSN or
	// TEST_PGSQL_DSN is set; otherwise it skips so CI stays hermetic.
	t.Run("failed/db-store", func(t *testing.T) {
		dsn, driver := os.Getenv("TEST_MYSQL_DSN"), "mysql"
		if dsn == "" {
			dsn, driver = os.Getenv("TEST_PGSQL_DSN"), "pgsql"
		}
		if dsn == "" {
			t.Skip("set TEST_MYSQL_DSN or TEST_PGSQL_DSN for the live failed-jobs store test")
		}
		db, err := database.Connect(driver, dsn)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		create := `CREATE TABLE IF NOT EXISTS failed_jobs (
			id CHAR(36) PRIMARY KEY,
			connection VARCHAR(32) NOT NULL,
			queue VARCHAR(64) NOT NULL DEFAULT 'default',
			name VARCHAR(64) NOT NULL,
			payload TEXT NOT NULL,
			exception TEXT NOT NULL,
			attempts INT NOT NULL DEFAULT 0,
			failed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_failed_jobs_failed_at (failed_at)
		)`
		if driver == "pgsql" {
			create = `CREATE TABLE IF NOT EXISTS failed_jobs (
				id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
				connection VARCHAR(32) NOT NULL,
				queue VARCHAR(64) NOT NULL DEFAULT 'default',
				name VARCHAR(64) NOT NULL,
				payload TEXT NOT NULL,
				exception TEXT NOT NULL,
				attempts INT NOT NULL DEFAULT 0,
				failed_at TIMESTAMPTZ NOT NULL DEFAULT now()
			)`
		}
		if err := db.Exec(create).Error; err != nil {
			t.Fatal(err)
		}
		if driver == "pgsql" {
			if err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_failed_jobs_failed_at ON failed_jobs(failed_at)`).Error; err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() { _ = db.Exec(`DROP TABLE IF EXISTS failed_jobs`).Error })
		ctx := context.Background()
		store := NewDatabaseFailedStore(db)
		if err := store.Record(ctx, FailedJob{Connection: "database", Name: "flaky", Payload: []byte(`{}`), Exception: "boom", Attempts: 3}); err != nil {
			t.Fatal(err)
		}
		rows, err := store.List(ctx, 10)
		if err != nil || len(rows) != 1 {
			t.Fatalf("list = %+v,%v", rows, err)
		}
		if rows[0].Queue != "default" || rows[0].Exception != "boom" || rows[0].ID == "" {
			t.Fatalf("row = %+v", rows[0])
		}
		got, err := store.Get(ctx, rows[0].ID)
		if err != nil || got.Name != "flaky" {
			t.Fatalf("get = %+v,%v", got, err)
		}
		if err := store.Delete(ctx, rows[0].ID); err != nil {
			t.Fatal(err)
		}
		if rows, _ := store.List(ctx, 10); len(rows) != 0 {
			t.Fatalf("rows after delete = %+v", rows)
		}
		if err := store.Record(ctx, FailedJob{Connection: "database", Name: "x", Payload: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
		if err := store.Flush(ctx); err != nil {
			t.Fatal(err)
		}
		if rows, _ := store.List(ctx, 10); len(rows) != 0 {
			t.Fatalf("rows after flush = %+v", rows)
		}
	})
}
