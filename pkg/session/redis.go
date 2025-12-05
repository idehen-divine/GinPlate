package session

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisStore is a Store backed by a shared Redis client using the
// session:{accessJti} -> refreshJti and refresh:{refreshJti} -> accessJti
// key scheme. Either half locates the other half for destruction.
type redisStore struct {
	rdb *redis.Client
}

// Redis returns a Redis-backed Store over an existing client.
func Redis(rdb *redis.Client) Store { return &redisStore{rdb: rdb} }

// Link records both halves with their TTLs.
func (s *redisStore) Link(ctx context.Context, accessJti, refreshJti, _ string, accessTTL, refreshTTL time.Duration) error {
	if err := s.rdb.Set(ctx, "session:"+accessJti, refreshJti, accessTTL).Err(); err != nil {
		return err
	}
	return s.rdb.Set(ctx, "refresh:"+refreshJti, accessJti, refreshTTL).Err()
}

// AccessValid returns the linked refresh jti, or "" when unknown/expired.
func (s *redisStore) AccessValid(ctx context.Context, accessJti string) (string, bool) {
	if accessJti == "" {
		return "", false
	}
	rjti, err := s.rdb.Get(ctx, "session:"+accessJti).Result()
	if err != nil || rjti == "" {
		return "", false
	}
	return rjti, true
}

// RefreshValid returns the linked access jti, or "" when unknown/expired.
func (s *redisStore) RefreshValid(ctx context.Context, refreshJti string) (string, bool) {
	if refreshJti == "" {
		return "", false
	}
	ajti, err := s.rdb.Get(ctx, "refresh:"+refreshJti).Result()
	if err != nil || ajti == "" {
		return "", false
	}
	return ajti, true
}

// Unlink destroys both halves. Missing keys are not errors.
func (s *redisStore) Unlink(ctx context.Context, accessJti, refreshJti string) error {
	keys := make([]string, 0, 2)
	if accessJti != "" {
		keys = append(keys, "session:"+accessJti)
	}
	if refreshJti != "" {
		keys = append(keys, "refresh:"+refreshJti)
	}
	if len(keys) == 0 {
		return nil
	}
	return s.rdb.Del(ctx, keys...).Err()
}
