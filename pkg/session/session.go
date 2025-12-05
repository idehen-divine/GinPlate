// Package session tracks login sessions behind a driver-selected Store,
// so Redis is one backend among equals instead of a hardcoded dependency.
// Three drivers ship: redis (shared client), database (sessions table),
// and file (one JSON file per session). A nil Store disables tracking and
// tokens validate by signature only.
package session

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Store records linked access+refresh session halves. Lookups fail closed:
// unknown, expired, or unreadable halves report ok=false, so callers treat
// backend outages like revocations. Writes are best-effort by contract;
// callers ignore Link/Unlink errors so a store blip never fails a login.
type Store interface {
	// Link records both halves of a session with their TTLs.
	Link(ctx context.Context, accessJti, refreshJti, userID string, accessTTL, refreshTTL time.Duration) error
	// AccessValid returns the linked refresh jti for a live access half.
	AccessValid(ctx context.Context, accessJti string) (refreshJti string, ok bool)
	// RefreshValid returns the linked access jti for a live refresh half.
	RefreshValid(ctx context.Context, refreshJti string) (accessJti string, ok bool)
	// Unlink destroys both halves. Missing halves are not errors.
	Unlink(ctx context.Context, accessJti, refreshJti string) error
}

// Open selects the session driver. Unknown drivers fail fast at startup.
// A redis selection without a reachable client degrades to a disabled
// (nil) store with no error, preserving boot-anywhere behavior.
func Open(driver string, db *gorm.DB, rdb *redis.Client, dir string) (Store, error) {
	switch driver {
	case "", "file":
		return File(dir)
	case "redis":
		if rdb == nil {
			return nil, nil
		}
		return Redis(rdb), nil
	case "database":
		if db == nil {
			return nil, fmt.Errorf("session: database driver needs a *gorm.DB")
		}
		return Database(db), nil
	default:
		return nil, fmt.Errorf("session: unsupported driver %q", driver)
	}
}
