// Package cache provides a selectable key-value cache behind a small
// interface so the store can change without touching callers. Three stores
// ship: redis (shared client), database (caches table), and memory
// (process-local, for tests and dev without backends).
package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Store is the cache contract. Keys are namespaced by the configured
// prefix inside every backend. A ttl <= 0 means no expiry.
type Store interface {
	Get(ctx context.Context, key string) (val []byte, ok bool, err error)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	Delete(ctx context.Context, keys ...string) error
	Exists(ctx context.Context, key string) (bool, error)
}

// Open returns the configured store. A redis selection without a reachable
// client degrades to memory (with no error) so the app boots everywhere;
// cache misses only ever cost performance, never correctness. Unknown
// store names fail fast.
func Open(cfg config.Cache, db *gorm.DB, rdb *redis.Client) (Store, error) {
	switch cfg.Store {
	case "", "memory":
		return NewMemory(cfg.Prefix), nil
	case "redis":
		if rdb == nil {
			return NewMemory(cfg.Prefix), nil
		}
		return NewRedis(rdb, cfg.Prefix), nil
	case "database":
		if db == nil {
			return nil, fmt.Errorf("cache: database store needs a *gorm.DB")
		}
		return NewDatabase(db, cfg.Prefix), nil
	default:
		return nil, fmt.Errorf("cache: unsupported store %q", cfg.Store)
	}
}
