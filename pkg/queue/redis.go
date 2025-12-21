package queue

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// redisEnvelope is the list payload: job fields plus scheduling metadata
// the broker itself cannot express (attempts, not-before time).
type redisEnvelope struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Payload     []byte `json:"payload"`
	Attempts    int    `json:"attempts"`
	AvailableAt int64  `json:"available_at"`
}

// redisQueue is a Queue over one Redis list (FIFO via LPUSH/BRPOP). Delayed
// jobs sit at the head until due: Reserve re-queues not-yet-available heads
// and reports empty, so the worker sleeps instead of spinning.
type redisQueue struct {
	rdb   *redis.Client
	key   string
	tries int
}

// NewRedis returns a list-backed Queue on key. An empty key selects the
// default queue name. tries caps total runs (min 1: run once, no retry).
func NewRedis(rdb *redis.Client, key string, tries int) Queue {
	if key == "" {
		key = "queue:default"
	}
	if tries < 1 {
		tries = 1
	}
	return &redisQueue{rdb: rdb, key: key, tries: tries}
}

// Push appends a first-attempt job to the tail.
func (q *redisQueue) Push(ctx context.Context, name string, payload []byte) (string, error) {
	id := uuid.NewString()
	env := redisEnvelope{ID: id, Name: name, Payload: payload, Attempts: 0, AvailableAt: time.Now().Unix()}
	raw, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	if err := q.rdb.LPush(ctx, q.key, raw).Err(); err != nil {
		return "", err
	}
	return id, nil
}

// serverNow reads the broker clock so delayed retries stay consistent
// across workers and honor time travel in tests (miniredis FastForward).
func (q *redisQueue) serverNow(ctx context.Context) (time.Time, error) {
	return q.rdb.Time(ctx).Result()
}

// Reserve pops the head with a short blocking wait. A head that is not yet
// due is pushed back and reported as empty; callers sleep briefly.
func (q *redisQueue) Reserve(ctx context.Context) (Job, bool, error) {
	raw, err := q.rdb.BRPop(ctx, 2*time.Second, q.key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return Job{}, false, nil
		}
		return Job{}, false, err
	}
	if len(raw) < 2 {
		return Job{}, false, nil
	}
	var env redisEnvelope
	if err := json.Unmarshal([]byte(raw[1]), &env); err != nil {
		return Job{}, false, err
	}
	now, err := q.serverNow(ctx)
	if err != nil {
		_ = q.rdb.LPush(ctx, q.key, raw[1]).Err()
		return Job{}, false, err
	}
	if now.Unix() < env.AvailableAt {
		_ = q.rdb.LPush(ctx, q.key, raw[1]).Err()
		return Job{}, false, nil
	}
	return Job{ID: env.ID, Name: env.Name, Payload: env.Payload, Attempts: env.Attempts + 1, AvailableAt: time.Unix(env.AvailableAt, 0)}, true, nil
}

// Ack is a no-op: popped jobs already left the list.
func (q *redisQueue) Ack(_ context.Context, _ string) error { return nil }

// Fail requeues with backoff, or drops the job past max attempts and
// reports ErrJobBuried so the worker logs it distinctly.
func (q *redisQueue) Fail(ctx context.Context, job Job, _ error) error {
	if job.Attempts >= q.tries {
		return ErrJobBuried
	}
	now, err := q.serverNow(ctx)
	if err != nil {
		return err
	}
	env := redisEnvelope{
		ID: job.ID, Name: job.Name, Payload: job.Payload,
		Attempts: job.Attempts, AvailableAt: now.Add(retryDelay(job.Attempts)).Unix(),
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return q.rdb.LPush(ctx, q.key, raw).Err()
}
