package cache

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/idehen-divine/GinPlate/pkg/database"
	"github.com/redis/go-redis/v9"
)

// conformance exercises the Store contract against any backend: round-trip,
// miss, overwrite, multi-delete, and expiry. Backends opt in by passing a
// constructor; the DB backend additionally runs live when TEST_MYSQL_DSN
// (or TEST_PGSQL_DSN) is set.
func conformance(t *testing.T, name string, open func(t *testing.T) Store) {
	t.Helper()
	ctx := context.Background()

	t.Run(name+"/roundtrip", func(t *testing.T) {
		s := open(t)
		if err := s.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
			t.Fatal(err)
		}
		val, ok, err := s.Get(ctx, "k")
		if err != nil || !ok || string(val) != "v" {
			t.Fatalf("get = %q,%v,%v", val, ok, err)
		}
		if ok, err := s.Exists(ctx, "k"); err != nil || !ok {
			t.Fatalf("exists = %v,%v", ok, err)
		}
	})

	t.Run(name+"/miss", func(t *testing.T) {
		s := open(t)
		if _, ok, err := s.Get(ctx, "nope"); err != nil || ok {
			t.Fatalf("get missing = %v,%v", ok, err)
		}
		if ok, err := s.Exists(ctx, "nope"); err != nil || ok {
			t.Fatalf("exists missing = %v,%v", ok, err)
		}
		if err := s.Delete(ctx, "nope"); err != nil {
			t.Fatalf("delete missing: %v", err)
		}
	})

	t.Run(name+"/overwrite-delete", func(t *testing.T) {
		s := open(t)
		if err := s.Set(ctx, "k", []byte("v1"), time.Minute); err != nil {
			t.Fatal(err)
		}
		if err := s.Set(ctx, "k", []byte("v2"), time.Minute); err != nil {
			t.Fatal(err)
		}
		val, ok, err := s.Get(ctx, "k")
		if err != nil || !ok || string(val) != "v2" {
			t.Fatalf("overwrite get = %q,%v,%v", val, ok, err)
		}
		if err := s.Delete(ctx, "k", "other"); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := s.Get(ctx, "k"); ok {
			t.Fatal("deleted key still present")
		}
	})

	t.Run(name+"/no-expiry", func(t *testing.T) {
		s := open(t)
		if err := s.Set(ctx, "forever", []byte("v"), 0); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := s.Get(ctx, "forever"); err != nil || !ok {
			t.Fatalf("persistent get = %v,%v", ok, err)
		}
	})
}

// TestCache is the single entry point for every cache test: factory
// rejection, memory conformance plus clock-driven expiry and prefix
// isolation, redis conformance plus TTL expiry, and the live-gated
// database conformance.
func TestCache(t *testing.T) {
	t.Run("open/rejects-unknown-store", func(t *testing.T) {
		if _, err := Open(config.Cache{Store: "bogus"}, nil, nil); err == nil {
			t.Fatal("expected error for unknown store")
		}
	})

	t.Run("open/requires-db", func(t *testing.T) {
		if _, err := Open(config.Cache{Store: "database"}, nil, nil); err == nil {
			t.Fatal("expected error for database store without *gorm.DB")
		}
	})

	t.Run("memory/conformance", func(t *testing.T) {
		conformance(t, "memory", func(t *testing.T) Store {
			t.Helper()
			return NewMemory("test:")
		})
	})

	t.Run("memory/expiry", func(t *testing.T) {
		now := time.Now()
		s := &memoryStore{prefix: "test:", now: func() time.Time { return now }, items: map[string]memItem{}}
		ctx := context.Background()
		if err := s.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
			t.Fatal(err)
		}
		now = now.Add(2 * time.Minute)
		if _, ok, err := s.Get(ctx, "k"); err != nil || ok {
			t.Fatalf("expired get = %v,%v", ok, err)
		}
		if ok, err := s.Exists(ctx, "k"); err != nil || ok {
			t.Fatalf("expired exists = %v,%v", ok, err)
		}
	})

	t.Run("memory/prefix-isolation", func(t *testing.T) {
		ctx := context.Background()
		a := NewMemory("a:")
		b := NewMemory("b:")
		if err := a.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := b.Get(ctx, "k"); ok {
			t.Fatal("prefixes leaked across namespaces")
		}
	})

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
			return NewRedis(rdb, "test:")
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
		s := NewRedis(rdb, "test:")
		ctx := context.Background()
		if err := s.Set(ctx, "k", []byte("v"), time.Second); err != nil {
			t.Fatal(err)
		}
		mr.FastForward(2 * time.Second)
		if _, ok, err := s.Get(ctx, "k"); err != nil || ok {
			t.Fatalf("expired get = %v,%v", ok, err)
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
			t.Skip("set TEST_MYSQL_DSN or TEST_PGSQL_DSN for the live database cache test")
		}
		db, err := database.Connect(driver, dsn)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		if err := db.Exec(`DROP TABLE IF EXISTS caches`).Error; err != nil {
			t.Fatal(err)
		}
		ts := "TIMESTAMP NULL DEFAULT NULL"
		if driver == "pgsql" {
			ts = "TIMESTAMPTZ"
		}
		if err := db.Exec(`CREATE TABLE caches (
			cache_key VARCHAR(255) PRIMARY KEY,
			value TEXT NOT NULL,
			expires_at ` + ts + `,
			INDEX idx_caches_expires (expires_at)
		)`).Error; err != nil {
			// pgsql has no inline INDEX: retry without it.
			if err := db.Exec(`CREATE TABLE caches (
				cache_key VARCHAR(255) PRIMARY KEY,
				value TEXT NOT NULL,
				expires_at ` + ts + `
			)`).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_caches_expires ON caches(expires_at)`).Error; err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() { _ = db.Exec(`DROP TABLE IF EXISTS caches`).Error })
		conformance(t, "database", func(t *testing.T) Store {
			t.Helper()
			return NewDatabase(db, "test:")
		})
	})
}
