package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/idehen-divine/GinPlate/pkg/database"
	"github.com/redis/go-redis/v9"
)

// conformance exercises the Store contract against any backend: link both
// halves, unlink destroys both, missing halves read invalid, unlink is
// idempotent, and overwrite replaces. Backends opt in by constructor.
func conformance(t *testing.T, name string, open func(t *testing.T) Store) {
	t.Helper()
	ctx := context.Background()

	t.Run(name+"/link-valid", func(t *testing.T) {
		s := open(t)
		if err := s.Link(ctx, "a1", "r1", "u1", time.Minute, time.Hour); err != nil {
			t.Fatal(err)
		}
		if rjti, ok := s.AccessValid(ctx, "a1"); !ok || rjti != "r1" {
			t.Fatalf("access = %q,%v", rjti, ok)
		}
		if ajti, ok := s.RefreshValid(ctx, "r1"); !ok || ajti != "a1" {
			t.Fatalf("refresh = %q,%v", ajti, ok)
		}
	})

	t.Run(name+"/miss", func(t *testing.T) {
		s := open(t)
		if _, ok := s.AccessValid(ctx, "nope"); ok {
			t.Fatal("missing access half should be invalid")
		}
		if _, ok := s.RefreshValid(ctx, "nope"); ok {
			t.Fatal("missing refresh half should be invalid")
		}
		if err := s.Unlink(ctx, "nope", "nope"); err != nil {
			t.Fatalf("unlink missing: %v", err)
		}
	})

	t.Run(name+"/unlink", func(t *testing.T) {
		s := open(t)
		if err := s.Link(ctx, "a2", "r2", "u2", time.Minute, time.Hour); err != nil {
			t.Fatal(err)
		}
		if err := s.Unlink(ctx, "a2", "r2"); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.AccessValid(ctx, "a2"); ok {
			t.Fatal("access half survived unlink")
		}
		if _, ok := s.RefreshValid(ctx, "r2"); ok {
			t.Fatal("refresh half survived unlink")
		}
	})

	t.Run(name+"/overwrite", func(t *testing.T) {
		s := open(t)
		if err := s.Link(ctx, "a3", "r3", "u3", time.Minute, time.Hour); err != nil {
			t.Fatal(err)
		}
		if err := s.Link(ctx, "a3", "r4", "u3", time.Minute, time.Hour); err != nil {
			t.Fatal(err)
		}
		if rjti, ok := s.AccessValid(ctx, "a3"); !ok || rjti != "r4" {
			t.Fatalf("overwrite access = %q,%v", rjti, ok)
		}
	})

	t.Run(name+"/consume-single-use", func(t *testing.T) {
		s := open(t)
		if err := s.Link(ctx, "a4", "r4", "u4", time.Minute, time.Hour); err != nil {
			t.Fatal(err)
		}
		ajti, ok, err := s.ConsumeRefresh(ctx, "r4")
		if err != nil || !ok || ajti != "a4" {
			t.Fatalf("consume = %q,%v,%v", ajti, ok, err)
		}
		if _, ok, _ := s.ConsumeRefresh(ctx, "r4"); ok {
			t.Fatal("replay consumed twice")
		}
		if _, ok := s.AccessValid(ctx, "a4"); ok {
			t.Fatal("access half survived consume")
		}
		if _, ok := s.RefreshValid(ctx, "r4"); ok {
			t.Fatal("refresh half survived consume")
		}
	})

	t.Run(name+"/replace-single-use", func(t *testing.T) {
		s := open(t)
		if err := s.Link(ctx, "a5", "r5", "u5", time.Minute, time.Hour); err != nil {
			t.Fatal(err)
		}
		old, ok, err := s.ReplaceRefresh(ctx, "r5", "a6", "r6", "u5", time.Minute, time.Hour)
		if err != nil || !ok || old != "a5" {
			t.Fatalf("replace = %q,%v,%v", old, ok, err)
		}
		if _, ok := s.AccessValid(ctx, "a5"); ok {
			t.Fatal("old access half survived replacement")
		}
		if _, ok := s.RefreshValid(ctx, "r5"); ok {
			t.Fatal("old refresh half survived replacement")
		}
		if got, ok := s.RefreshValid(ctx, "r6"); !ok || got != "a6" {
			t.Fatalf("replacement halves = %q,%v", got, ok)
		}
		if _, ok, _ := s.ReplaceRefresh(ctx, "r5", "a7", "r7", "u5", time.Minute, time.Hour); ok {
			t.Fatal("replay replaced twice")
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
			user_id CHAR(36) NULL,
			refresh_jti CHAR(36) NOT NULL,
			access_expires_at TIMESTAMP NULL DEFAULT NULL,
			refresh_expires_at TIMESTAMP NULL DEFAULT NULL,
			INDEX idx_sessions_user (user_id),
			UNIQUE KEY uq_sessions_refresh (refresh_jti)
		)`
		indexes := []string{}
		if driver == "pgsql" {
			create = `CREATE TABLE sessions (
				id CHAR(36) PRIMARY KEY,
				user_id CHAR(36) NULL,
				refresh_jti CHAR(36) NOT NULL,
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
			return Database(db)
		})
	})
}
