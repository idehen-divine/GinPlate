package redis

import (
	"context"
	"time"

	"github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/redis/go-redis/v9"
)

func NewClient(addr, username, password string, db int) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     addr,
		Username: username,
		Password: password,
		DB:       db,
	})
}

// DialOrNil pings Redis, returning nil when unreachable. Callers must
// tolerate a nil client.
func DialOrNil(config config.Redis, timeout time.Duration) *redis.Client {
	c := NewClient(config.Addr(), config.User, config.Pass, config.DB)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil
	}
	return c
}
