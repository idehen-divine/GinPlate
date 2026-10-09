// Package session tracks login sessions behind a driver-selected Store
// (redis, database, file). Construction is fail-closed: no reachable
// backend is an error, never a silent downgrade to untracked tokens.
package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var errEmptySessionID = errors.New("session: empty session id")

// Store records linked access+refresh session halves. Lookups fail closed
// (unknown/expired/unreadable → ok=false); link failures must fail
// authentication. Refresh rotation must use ReplaceRefresh, never
// consume-then-link, or a failed replacement strands the user sessionless.
type Store interface {
	Link(ctx context.Context, accessJti, refreshJti, userID string, accessTTL, refreshTTL time.Duration) error
	AccessValid(ctx context.Context, accessJti string) (refreshJti string, ok bool)
	RefreshValid(ctx context.Context, refreshJti string) (accessJti string, ok bool)
	// ConsumeRefresh atomically validates the refresh half and deletes the
	// whole session, returning the linked access jti. It is for one-way
	// invalidation; refresh rotation must use ReplaceRefresh. The second
	// concurrent consumer reports ok=false, which is how replay is detected.
	// Storage errors are returned so callers can distinguish outage (500)
	// from replay (401).
	ConsumeRefresh(ctx context.Context, refreshJti string) (accessJti string, ok bool, err error)
	// ReplaceRefresh atomically validates and consumes the old refresh half
	// while recording the replacement session, returning the replaced access
	// jti. Exactly one concurrent caller wins (ok=true); the rest observe
	// ok=false (replay). On storage error the rotation is rolled back where
	// the backend allows (Redis script atomicity, DB transaction); the caller
	// must discard the minted pair either way.
	ReplaceRefresh(ctx context.Context, oldRefreshJti, newAccessJti, newRefreshJti, userID string, accessTTL, refreshTTL time.Duration) (oldAccessJti string, ok bool, err error)
	// Unlink destroys both halves. Missing halves are not errors.
	Unlink(ctx context.Context, accessJti, refreshJti string) error
}

// Open selects the session driver (unknown drivers fail fast; redis without
// a reachable client is a construction error, never untracked).
func Open(driver string, db *gorm.DB, rdb *redis.Client, dir string) (Store, error) {
	switch driver {
	case "", "file":
		return File(dir)
	case "redis":
		if rdb == nil {
			return nil, fmt.Errorf("session: redis driver needs a reachable client (refusing to run untracked)")
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
