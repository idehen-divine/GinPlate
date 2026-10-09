package scheduler

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Locker is expiring mutual exclusion: Release drops only on token match,
// so an expired holder can never release the next holder's lock.
type Locker interface {
	Acquire(ctx context.Context, key string, ttl time.Duration) (token string, held bool, err error)
	Release(ctx context.Context, key, token string) error
}

// MemoryLocker guards a single process (fleet-wide needs RedisLocker).
type MemoryLocker struct {
	mu    sync.Mutex
	locks map[string]memLock
}

type memLock struct {
	token   string
	expires time.Time
}

func NewMemoryLocker() *MemoryLocker { return &MemoryLocker{locks: map[string]memLock{}} }

func (l *MemoryLocker) Acquire(_ context.Context, key string, ttl time.Duration) (string, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if cur, ok := l.locks[key]; ok && time.Now().Before(cur.expires) {
		return "", false, nil
	}
	token := uuid.NewString()
	l.locks[key] = memLock{token: token, expires: time.Now().Add(ttl)}
	return token, true, nil
}

func (l *MemoryLocker) Release(_ context.Context, key, token string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if cur, ok := l.locks[key]; ok && cur.token == token {
		delete(l.locks, key)
	}
	return nil
}

// RedisLocker shares exclusion across replicas (SET NX EX + Lua compare-del).
type RedisLocker struct {
	rdb *redis.Client
}

func NewRedisLocker(rdb *redis.Client) *RedisLocker { return &RedisLocker{rdb: rdb} }

func (l *RedisLocker) Acquire(ctx context.Context, key string, ttl time.Duration) (string, bool, error) {
	token := uuid.NewString()
	err := l.rdb.SetArgs(ctx, key, token, redis.SetArgs{Mode: "NX", TTL: ttl}).Err()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", false, nil
		}
		return "", false, err
	}
	return token, true, nil
}

var releaseScript = redis.NewScript(`if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) else return 0 end`)

func (l *RedisLocker) Release(ctx context.Context, key, token string) error {
	return releaseScript.Run(ctx, l.rdb, []string{key}, token).Err()
}
