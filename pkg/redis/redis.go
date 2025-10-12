package redis

import (
	"context"
	"time"

	"github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/redis/go-redis/v9"
)

// NewClient opens a Redis client for the given address, ACL identity, and
// logical database. Empty username means no ACL auth; DB 0 is the default
// index. The caller owns session keys; a nil client disables session
// tracking.
func NewClient(addr, username, password string, db int) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     addr,
		Username: username,
		Password: password,
		DB:       db,
	})
}

// DialOrNil pings Redis and returns nil when it is unreachable, so the app
// boots and serves without it (tokens validate by signature, logout becomes
// a no-op). Callers must already tolerate a nil client.
func DialOrNil(cfg config.Redis, timeout time.Duration) *redis.Client {
	c := NewClient(cfg.Addr(), cfg.User, cfg.Pass, cfg.DB)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil
	}
	return c
}
