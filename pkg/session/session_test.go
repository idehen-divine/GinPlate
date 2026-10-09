package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/idehen-divine/GinPlate/pkg/database"
	"github.com/redis/go-redis/v9"
)

// conformance exercises the Store contract against any backend: link both
// halves, unlink destroys both, missing halves read invalid, unlink is
// idempotent, and overwrite replaces. Backends opt in by constructor.
func conformance(t *testing.T, name string, open func(t *testing.T) Store) {
	t.Helper()
	ctx := context.Background()

	// JTIs are fresh UUIDs per subtest: the pgsql schema types them UUID,
	// so short fakes like "r1" are rejected by the database itself.
	t.Run(name+"/link-valid", func(t *testing.T) {
		s := open(t)
		access, refresh, user := uuid.NewString(), uuid.NewString(), uuid.NewString()
		if err := s.Link(ctx, access, refresh, user, time.Minute, time.Hour); err != nil {
			t.Fatal(err)
		}
		if rjti, ok := s.AccessValid(ctx, access); !ok || rjti != refresh {
			t.Fatalf("access = %q,%v", rjti, ok)
		}
		if ajti, ok := s.RefreshValid(ctx, refresh); !ok || ajti != access {
			t.Fatalf("refresh = %q,%v", ajti, ok)
		}
	})

	t.Run(name+"/miss", func(t *testing.T) {
		s := open(t)
		missing := uuid.NewString()
		if _, ok := s.AccessValid(ctx, missing); ok {
			t.Fatal("missing access half should be invalid")
		}
		if _, ok := s.RefreshValid(ctx, missing); ok {
			t.Fatal("missing refresh half should be invalid")
		}
		if err := s.Unlink(ctx, missing, missing); err != nil {
			t.Fatalf("unlink missing: %v", err)
		}
	})

	t.Run(name+"/unlink", func(t *testing.T) {
		s := open(t)
		access, refresh, user := uuid.NewString(), uuid.NewString(), uuid.NewString()
		if err := s.Link(ctx, access, refresh, user, time.Minute, time.Hour); err != nil {
			t.Fatal(err)
		}
		if err := s.Unlink(ctx, access, refresh); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.AccessValid(ctx, access); ok {
			t.Fatal("access half survived unlink")
		}
		if _, ok := s.RefreshValid(ctx, refresh); ok {
			t.Fatal("refresh half survived unlink")
		}
	})

	t.Run(name+"/overwrite", func(t *testing.T) {
		s := open(t)
		access, first, second, user := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
		if err := s.Link(ctx, access, first, user, time.Minute, time.Hour); err != nil {
			t.Fatal(err)
		}
		if err := s.Link(ctx, access, second, user, time.Minute, time.Hour); err != nil {
			t.Fatal(err)
		}
		if rjti, ok := s.AccessValid(ctx, access); !ok || rjti != second {
			t.Fatalf("overwrite access = %q,%v", rjti, ok)
		}
	})

	t.Run(name+"/consume-single-use", func(t *testing.T) {
		s := open(t)
		access, refresh, user := uuid.NewString(), uuid.NewString(), uuid.NewString()
		if err := s.Link(ctx, access, refresh, user, time.Minute, time.Hour); err != nil {
			t.Fatal(err)
		}
		ajti, ok, err := s.ConsumeRefresh(ctx, refresh)
		if err != nil || !ok || ajti != access {
			t.Fatalf("consume = %q,%v,%v", ajti, ok, err)
		}
		if _, ok, _ := s.ConsumeRefresh(ctx, refresh); ok {
			t.Fatal("replay consumed twice")
		}
		if _, ok := s.AccessValid(ctx, access); ok {
			t.Fatal("access half survived consume")
		}
		if _, ok := s.RefreshValid(ctx, refresh); ok {
			t.Fatal("refresh half survived consume")
		}
	})

	t.Run(name+"/replace-single-use", func(t *testing.T) {
		s := open(t)
		access, refresh, user := uuid.NewString(), uuid.NewString(), uuid.NewString()
		if err := s.Link(ctx, access, refresh, user, time.Minute, time.Hour); err != nil {
			t.Fatal(err)
		}
		newAccess, newRefresh := uuid.NewString(), uuid.NewString()
		old, ok, err := s.ReplaceRefresh(ctx, refresh, newAccess, newRefresh, user, time.Minute, time.Hour)
		if err != nil || !ok || old != access {
			t.Fatalf("replace = %q,%v,%v", old, ok, err)
		}
		if _, ok := s.AccessValid(ctx, access); ok {
			t.Fatal("old access half survived replacement")
		}
		if _, ok := s.RefreshValid(ctx, refresh); ok {
			t.Fatal("old refresh half survived replacement")
		}
		if got, ok := s.RefreshValid(ctx, newRefresh); !ok || got != newAccess {
			t.Fatalf("replacement halves = %q,%v", got, ok)
		}
		if _, ok, _ := s.ReplaceRefresh(ctx, refresh, uuid.NewString(), uuid.NewString(), user, time.Minute, time.Hour); ok {
			t.Fatal("replay replaced twice")
		}
	})

	t.Run(name+"/revoke-user", func(t *testing.T) {
		s := open(t)
		a1, r1 := uuid.NewString(), uuid.NewString()
		a2, r2 := uuid.NewString(), uuid.NewString()
		a3, r3 := uuid.NewString(), uuid.NewString()
		user, other := uuid.NewString(), uuid.NewString()
		if err := s.Link(ctx, a1, r1, user, time.Minute, time.Hour); err != nil {
			t.Fatal(err)
		}
		if err := s.Link(ctx, a2, r2, user, time.Minute, time.Hour); err != nil {
			t.Fatal(err)
		}
		if err := s.Link(ctx, a3, r3, other, time.Minute, time.Hour); err != nil {
			t.Fatal(err)
		}
		if err := s.RevokeUser(ctx, user); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.AccessValid(ctx, a1); ok {
			t.Fatal("revoked access half survived")
		}
		if _, ok := s.RefreshValid(ctx, r2); ok {
			t.Fatal("revoked refresh half survived")
		}
		if _, ok := s.AccessValid(ctx, a3); !ok {
			t.Fatal("other user's session revoked")
		}
		if err := s.RevokeUser(ctx, uuid.NewString()); err != nil {
			t.Fatalf("unknown user: %v", err)
		}
	})
}

// TestSession is the single entry point for every session test: redis and
// file conformance plus TTL expiry, bad-dir rejection, factory selection,
// and the live-gated database conformance.
func TestSession(t *testing.T) {
	t.Run("redis/conformance", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(mr.Close)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		conformance(t, "redis", func(t *testing.T) Store {
			t.Helper()
			return Redis(rdb)
		})
	})

	t.Run("redis/expiry", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(mr.Close)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		s := Redis(rdb)
		ctx := context.Background()
		if err := s.Link(ctx, "a", "r", "u", time.Second, time.Hour); err != nil {
			t.Fatal(err)
		}
		if ttl := rdb.TTL(ctx, "session:a").Val(); ttl <= 0 || ttl > time.Minute {
			t.Fatalf("bad session TTL: %v", ttl)
		}
		if ttl := rdb.TTL(ctx, "refresh:r").Val(); ttl <= 0 {
			t.Fatalf("bad refresh TTL: %v", ttl)
		}
		mr.FastForward(2 * time.Second)
		if _, ok := s.AccessValid(ctx, "a"); ok {
			t.Fatal("expired access half should be invalid")
		}
		if _, ok := s.RefreshValid(ctx, "r"); !ok {
			t.Fatal("live refresh half should stay valid")
		}
	})

	t.Run("file/conformance", func(t *testing.T) {
		conformance(t, "file", func(t *testing.T) Store {
			t.Helper()
			s, err := File(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			return s
		})
	})

	t.Run("file/expiry", func(t *testing.T) {
		now := time.Now()
		s := &fileStore{dir: t.TempDir(), now: func() time.Time { return now }}
		ctx := context.Background()
		if err := s.Link(ctx, "a", "r", "u", time.Minute, time.Hour); err != nil {
			t.Fatal(err)
		}
		now = now.Add(2 * time.Hour)
		if _, ok := s.AccessValid(ctx, "a"); ok {
			t.Fatal("expired access half should be invalid")
		}
		if _, ok := s.RefreshValid(ctx, "r"); ok {
			t.Fatal("expired refresh half should be invalid")
		}
	})

	t.Run("file/caps-sessions-after-prune", func(t *testing.T) {
		oldMax := maxFileSessions
		maxFileSessions = 2
		t.Cleanup(func() { maxFileSessions = oldMax })
		s := &fileStore{dir: t.TempDir(), now: time.Now}
		ctx := context.Background()
		// Fill with one live and one expired session.
		if err := s.Link(ctx, "live", "r1", "u", time.Hour, time.Hour); err != nil {
			t.Fatal(err)
		}
		if err := s.Link(ctx, "dead", "r2", "u", -time.Hour, -time.Hour); err != nil {
			t.Fatal(err)
		}
		// At capacity: the expired file is pruned, the new link succeeds.
		if err := s.Link(ctx, "next", "r3", "u", time.Hour, time.Hour); err != nil {
			t.Fatalf("prune should make room: %v", err)
		}
		if _, ok := s.AccessValid(ctx, "next"); !ok {
			t.Fatal("new session should be valid")
		}
		// Still at capacity with no expired files left: refuse.
		if err := s.Link(ctx, "overflow", "r4", "u", time.Hour, time.Hour); err == nil {
			t.Fatal("expected full-store error")
		}
	})

	t.Run("file/rejects-bad-dir", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "blocker")
		if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := File(filepath.Join(blocker, "sessions")); err == nil {
			t.Fatal("expected error for uncreatable dir")
		}
	})

	t.Run("open/selects-driver", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(mr.Close)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })

		if _, err := Open("bogus", nil, nil, ""); err == nil {
			t.Fatal("expected error for unknown driver")
		}
		if s, err := Open("redis", nil, nil, ""); err == nil || s != nil {
			t.Fatalf("redis without client must fail, got store=%v err=%v", s, err)
		}
		if _, err := Open("redis", nil, rdb, ""); err != nil {
			t.Fatalf("redis with client: %v", err)
		}
		if _, err := Open("database", nil, nil, ""); err == nil {
			t.Fatal("expected error for database driver without *gorm.DB")
		}
		if _, err := Open("file", nil, nil, t.TempDir()); err != nil {
			t.Fatalf("file driver: %v", err)
		}
		if _, err := Open("", nil, nil, t.TempDir()); err != nil {
			t.Fatalf("empty driver defaults to file: %v", err)
		}
	})

	// Runs against a live database only when TEST_MYSQL_DSN or
	// TEST_PGSQL_DSN is set; otherwise it skips so CI stays hermetic.
	t.Run("database/conformance", func(t *testing.T) {
		dsn, driver := os.Getenv("TEST_MYSQL_DSN"), "mysql"
		if dsn == "" {
			dsn, driver = os.Getenv("TEST_PGSQL_DSN"), "pgsql"
		}
		if dsn == "" {
			t.Skip("set TEST_MYSQL_DSN or TEST_PGSQL_DSN for the live database session test")
		}
		db, err := database.Connect(driver, dsn)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		if err := db.Exec(`DROP TABLE IF EXISTS sessions`).Error; err != nil {
			t.Fatal(err)
		}
		// pgsql has no inline INDEX: same table, indexes as extra statements.
		create := `CREATE TABLE sessions (
			id CHAR(36) PRIMARY KEY,
			tenant_id CHAR(36) NOT NULL DEFAULT '',
			user_id CHAR(36) NULL,
			refresh_jti CHAR(36) NOT NULL,
			access_expires_at TIMESTAMP NULL DEFAULT NULL,
			refresh_expires_at TIMESTAMP NULL DEFAULT NULL,
			INDEX idx_sessions_user (user_id),
			UNIQUE KEY uq_sessions_refresh (refresh_jti)
		)`
		indexes := []string{}
		if driver == "pgsql" {
			// Mirrors the goose migration: UUID columns, not CHAR(36)
			// (CHAR pads on read and breaks JTI comparisons).
			create = `CREATE TABLE sessions (
				id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
				tenant_id CHAR(36) NOT NULL DEFAULT '',
				user_id UUID NULL,
				refresh_jti UUID NOT NULL,
				access_expires_at TIMESTAMPTZ,
				refresh_expires_at TIMESTAMPTZ
			)`
			indexes = []string{
				`CREATE INDEX idx_sessions_user ON sessions(user_id)`,
				`CREATE UNIQUE INDEX uq_sessions_refresh ON sessions(refresh_jti)`,
			}
		}
		if err := db.Exec(create).Error; err != nil {
			t.Fatal(err)
		}
		for _, stmt := range indexes {
			if err := db.Exec(stmt).Error; err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() { _ = db.Exec(`DROP TABLE IF EXISTS sessions`).Error })
		conformance(t, "database", func(t *testing.T) Store {
			t.Helper()
			// Fresh table per subtest: hardcoded JTIs repeat across
			// subtests, and rows would otherwise leak between them.
			if err := db.Exec(`DELETE FROM sessions`).Error; err != nil {
				t.Fatal(err)
			}
			return Database(db)
		})
	})
}
