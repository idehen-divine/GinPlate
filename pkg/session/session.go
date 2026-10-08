// Package session tracks login sessions behind a driver-selected Store,
// so Redis is one backend among equals instead of a hardcoded dependency.
// Three drivers ship: redis (shared client), database (sessions table),
// and file (one JSON file per session, development-only). Construction is
// fail-closed: requesting redis without a reachable client is an error,
// never a silent downgrade to untracked tokens.
package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// errEmptySessionID rejects empty JTIs so callers cannot link sessions that
// can never be addressed or revoked.
var errEmptySessionID = errors.New("session: empty session id")

// Store records linked access+refresh session halves. Lookups fail closed:
// unknown, expired, or unreadable halves report ok=false, so callers treat
// backend outages like revocations. Link failures must fail authentication;
// callers must not ignore them. ConsumeRefresh atomically validates and
// deletes the refresh half for one-way invalidation. Callers performing
// refresh rotation must use ReplaceRefresh, never consume-then-link: only
// the atomic form guarantees a failed replacement cannot strand the user
// with no session. Storage errors are returned so callers can distinguish
// outage (500) from replay (401).
type Store interface {
	// Link records both halves of a session with their TTLs.
	Link(ctx context.Context, accessJti, refreshJti, userID string, accessTTL, refreshTTL time.Duration) error
	// AccessValid returns the linked refresh jti for a live access half.
	AccessValid(ctx context.Context, accessJti string) (refreshJti string, ok bool)
	// RefreshValid returns the linked access jti for a live refresh half.
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

// Open selects the session driver. Unknown drivers fail fast at startup.
// A redis selection without a client is a construction error: a nil store
// would silently disable logout and revocation, so callers must supply a
// reachable client instead of booting untracked.
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
