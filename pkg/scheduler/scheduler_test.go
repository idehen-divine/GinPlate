package scheduler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/idehen-divine/GinPlate/pkg/queue"
	"github.com/redis/go-redis/v9"
)

// pushQueue is a Queue recording pushes for scheduler fire assertions.
type pushQueue struct {
	pushed []queue.Job
}

func (q *pushQueue) Push(_ context.Context, name string, payload []byte) (string, error) {
	q.pushed = append(q.pushed, queue.Job{ID: "pushed", Name: name, Payload: payload, Attempts: 0})
	return "pushed", nil
}

func (q *pushQueue) Reserve(_ context.Context) (queue.Job, bool, error) {
	return queue.Job{}, false, nil
}

func (q *pushQueue) Ack(_ context.Context, _ string) error { return nil }

func (q *pushQueue) Fail(_ context.Context, job queue.Job, _ error) error { return nil }

func testRegistry() *queue.Registry {
	reg := queue.NewRegistry()
	reg.Handle("ping", func(_ context.Context, _ queue.Job) error { return nil })
	return reg
}

func noop(format string, args ...interface{}) {}

// TestScheduler is the single entry point for every scheduler test:
// builders, validation, firing, overlap skipping, server locks, TTL
// recovery, payload envelopes, and both locker backends.
func TestScheduler(t *testing.T) {
	t.Run("builders", func(t *testing.T) {
		if got := New("a").EveryMinute().Spec; got != "* * * * *" {
			t.Fatalf("every-minute = %q", got)
		}
		if got := New("a").Hourly().Spec; got != "0 * * * *" {
			t.Fatalf("hourly = %q", got)
		}
		if got := New("a").DailyAt(2, 30).Spec; got != "30 2 * * *" {
			t.Fatalf("daily = %q", got)
		}
		if got := New("a").Weekly(time.Monday, 6, 0).Spec; got != "0 6 * * 1" {
			t.Fatalf("weekly = %q", got)
		}
		e := New("billing.charge").Named("charge").WithPayload([]byte(`{}`)).WithoutOverlapping().OnOneServer().WithExpireAfter(time.Hour)
		if e.Name != "charge" || !e.Overlap || !e.OneServer || e.ExpireAfter != time.Hour {
			t.Fatalf("builder chain = %+v", e)
		}
		if got := New("billing.charge").Name; got != "billing.charge" {
			t.Fatalf("default name = %q", got)
		}
	})

	t.Run("prepare-validates", func(t *testing.T) {
		now := time.Now()
		if _, err := Prepare([]Entry{*New("ping").EveryMinute()}, testRegistry(), now); err != nil {
			t.Fatalf("valid entry: %v", err)
		}
		if _, err := Prepare([]Entry{*New("missing").EveryMinute()}, testRegistry(), now); err == nil {
			t.Fatal("expected error for unknown job")
		}
		if _, err := Prepare([]Entry{*New("ping").Cron("not a cron")}, testRegistry(), now); err == nil {
			t.Fatal("expected error for bad spec")
		}
	})

	t.Run("fire-pushes-due", func(t *testing.T) {
		reg := testRegistry()
		st, err := Prepare([]Entry{*New("ping").EveryMinute()}, reg, time.Now().Add(-2*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		q := &pushQueue{}
		if err := st.CheckAndFire(context.Background(), q, NewMemoryLocker(), time.Now(), noop); err != nil {
			t.Fatal(err)
		}
		if len(q.pushed) != 1 || q.pushed[0].Name != "ping" {
			t.Fatalf("pushed = %+v", q.pushed)
		}
	})

	t.Run("not-due-skips", func(t *testing.T) {
		st, err := Prepare([]Entry{*New("ping").EveryMinute()}, testRegistry(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		q := &pushQueue{}
		if err := st.CheckAndFire(context.Background(), q, NewMemoryLocker(), time.Now(), noop); err != nil {
			t.Fatal(err)
		}
		if len(q.pushed) != 0 {
			t.Fatalf("fresh schedule should not fire: %+v", q.pushed)
		}
	})

	t.Run("overlap-skips-while-held", func(t *testing.T) {
		st, err := Prepare([]Entry{*New("ping").EveryMinute().WithoutOverlapping()}, testRegistry(), time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		locker := NewMemoryLocker()
		q := &pushQueue{}
		ctx := context.Background()
		now := time.Now()
		if err := st.CheckAndFire(ctx, q, locker, now, noop); err != nil {
			t.Fatal(err)
		}
		if len(q.pushed) != 1 {
			t.Fatalf("first fire should push: %+v", q.pushed)
		}
		// Envelope carries the lock identity for the worker release hook.
		var env envelope
		if err := json.Unmarshal(q.pushed[0].Payload, &env); err != nil || env.Sched == nil {
			t.Fatalf("overlap push missing envelope: %s", q.pushed[0].Payload)
		}
		// Second tick while held: skipped, last-fire still advances (no catch-up storm).
		if err := st.CheckAndFire(ctx, q, locker, now.Add(time.Minute), noop); err != nil {
			t.Fatal(err)
		}
		if len(q.pushed) != 1 {
			t.Fatalf("second fire should skip: %+v", q.pushed)
		}
		// Worker settles the job: hook releases, next tick fires again.
		ReleaseHook(locker, noop)(q.pushed[0])
		st.items[0].last = now.Add(-time.Hour)
		if err := st.CheckAndFire(ctx, q, locker, now.Add(2*time.Minute), noop); err != nil {
			t.Fatal(err)
		}
		if len(q.pushed) != 2 {
			t.Fatalf("post-release fire should push: %+v", q.pushed)
		}
	})

	t.Run("overlap-ttl-recovers", func(t *testing.T) {
		st, err := Prepare([]Entry{*New("ping").EveryMinute().WithoutOverlapping().WithExpireAfter(30 * time.Millisecond)}, testRegistry(), time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		locker := NewMemoryLocker()
		q := &pushQueue{}
		ctx := context.Background()
		if err := st.CheckAndFire(ctx, q, locker, time.Now(), noop); err != nil {
			t.Fatal(err)
		}
		time.Sleep(60 * time.Millisecond)
		st.items[0].last = time.Now().Add(-time.Hour)
		if err := st.CheckAndFire(ctx, q, locker, time.Now(), noop); err != nil {
			t.Fatal(err)
		}
		if len(q.pushed) != 2 {
			t.Fatalf("expired lock should re-fire: %+v", q.pushed)
		}
	})

	t.Run("one-server-locks-decision-only", func(t *testing.T) {
		st, err := Prepare([]Entry{*New("ping").EveryMinute().OnOneServer()}, testRegistry(), time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		locker := NewMemoryLocker()
		q := &pushQueue{}
		if err := st.CheckAndFire(context.Background(), q, locker, time.Now(), noop); err != nil {
			t.Fatal(err)
		}
		if len(q.pushed) != 1 {
			t.Fatalf("one-server fire should push: %+v", q.pushed)
		}
		// Decision lock is released right after push: no lingering key.
		if _, held, _ := locker.Acquire(context.Background(), serverKey("ping"), time.Minute); !held {
			t.Fatal("server lock should be released after fire")
		}
		// And the payload stays raw: no worker coordination needed.
		var env envelope
		if err := json.Unmarshal(q.pushed[0].Payload, &env); err == nil && env.Sched != nil {
			t.Fatalf("one-server push should not carry an envelope: %s", q.pushed[0].Payload)
		}
	})

	t.Run("data-unwraps", func(t *testing.T) {
		raw := []byte(`{"to":"world"}`)
		if got := Data(raw); string(got) != string(raw) {
			t.Fatalf("plain payload altered: %s", got)
		}
		wrapped := wrapPayload(raw, "sched:overlap:x", "tok")
		if got := Data(wrapped); string(got) != string(raw) {
			t.Fatalf("envelope not unwrapped: %s", got)
		}
	})

	t.Run("release-hook-ignores-plain", func(t *testing.T) {
		ReleaseHook(NewMemoryLocker(), noop)(queue.Job{ID: "x", Payload: []byte(`{}`)})
	})

	t.Run("memory-locker", func(t *testing.T) {
		l := NewMemoryLocker()
		ctx := context.Background()
		tok, held, err := l.Acquire(ctx, "k", time.Minute)
		if err != nil || !held || tok == "" {
			t.Fatalf("acquire = %q,%v,%v", tok, held, err)
		}
		if _, held, _ := l.Acquire(ctx, "k", time.Minute); held {
			t.Fatal("double acquire should lose")
		}
		if err := l.Release(ctx, "k", "wrong"); err != nil {
			t.Fatal(err)
		}
		if _, held, _ := l.Acquire(ctx, "k", time.Minute); held {
			t.Fatal("wrong-token release should not free the lock")
		}
		if err := l.Release(ctx, "k", tok); err != nil {
			t.Fatal(err)
		}
		if _, held, _ := l.Acquire(ctx, "k", time.Minute); !held {
			t.Fatal("release should free the lock")
		}
	})

	t.Run("redis-locker", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(mr.Close)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		l := NewRedisLocker(rdb)
		ctx := context.Background()
		tok, held, err := l.Acquire(ctx, "k", time.Minute)
		if err != nil || !held || tok == "" {
			t.Fatalf("acquire = %q,%v,%v", tok, held, err)
		}
		if _, held, _ := l.Acquire(ctx, "k", time.Minute); held {
			t.Fatal("double acquire should lose")
		}
		if err := l.Release(ctx, "k", "wrong"); err != nil {
			t.Fatal(err)
		}
		if _, held, _ := l.Acquire(ctx, "k", time.Minute); held {
			t.Fatal("wrong-token release should not free the lock")
		}
		if err := l.Release(ctx, "k", tok); err != nil {
			t.Fatal(err)
		}
		if _, held, _ := l.Acquire(ctx, "k", time.Minute); !held {
			t.Fatal("release should free the lock")
		}
	})
}
