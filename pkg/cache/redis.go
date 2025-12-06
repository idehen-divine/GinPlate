package cache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisStore is a Store backed by a shared Redis client.
type redisStore struct {
	rdb    *redis.Client
	prefix string
}

// NewRedis returns a Redis-backed Store. Keys are stored as prefix+key.
func NewRedis(rdb *redis.Client, prefix string) Store {
	return &redisStore{rdb: rdb, prefix: prefix}
}

// Get returns the value or ok=false on miss (including redis.Nil).
func (s *redisStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	val, err := s.rdb.Get(ctx, s.prefix+key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return val, true, nil
}

// Set stores val with ttl (<=0 means persist).
func (s *redisStore) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	return s.rdb.Set(ctx, s.prefix+key, val, ttl).Err()
}

// Delete removes keys; missing keys are not an error.
func (s *redisStore) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	full := make([]string, len(keys))
	for i, k := range keys {
		full[i] = s.prefix + k
	}
	return s.rdb.Del(ctx, full...).Err()
}

// Exists reports whether key is present (and unexpired).
func (s *redisStore) Exists(ctx context.Context, key string) (bool, error) {
	n, err := s.rdb.Exists(ctx, s.prefix+key).Result()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}
