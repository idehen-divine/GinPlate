// Package cache provides a selectable key-value cache (redis/database/memory).
package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Store is the cache contract (ttl <= 0 means no expiry).
type Store interface {
	Get(ctx context.Context, key string) (val []byte, ok bool, err error)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	Delete(ctx context.Context, keys ...string) error
	Exists(ctx context.Context, key string) (bool, error)
}

// Open returns the configured store (unreachable redis degrades to memory;
// cache misses only cost performance, never correctness).
func Open(config config.Cache, db *gorm.DB, rdb *redis.Client) (Store, error) {
	switch config.Store {
	case "", "memory":
		return NewMemory(config.Prefix), nil
	case "redis":
		if rdb == nil {
			return NewMemory(config.Prefix), nil
		}
		return NewRedis(rdb, config.Prefix), nil
	case "database":
		if db == nil {
			return nil, fmt.Errorf("cache: database store needs a *gorm.DB")
		}
		return NewDatabase(db, config.Prefix), nil
	default:
		return nil, fmt.Errorf("cache: unsupported store %q", config.Store)
	}
}
