package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// withEnvDir runs fn inside a temp dir holding the given .env content,
// with APP_KEY preset valid unless the content overrides it.
func withEnvDir(t *testing.T, env string, fn func()) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	fn()
}

// TestConfig is the single entry point for every config test: nested
// population with null normalization and ${VAR} expansion, fail-fast on
// empty APP_KEY, and DSN assembly.
func TestConfig(t *testing.T) {
	t.Run("load-nested-populate", func(t *testing.T) {
		t.Setenv("APP_KEY", "base64:"+base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
		withEnvDir(t, "APP_NAME=Demo\nMAIL_FROM_NAME=${APP_NAME} Team\nMAIL_PORT=null\nREDIS_PASSWORD=null\nDB_CONNECTION=pgsql\n", func() {
			c, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if c.App.Name != "Demo" {
				t.Errorf("App.Name = %q", c.App.Name)
			}
			if c.Database.Driver != "pgsql" {
				t.Errorf("Database.Driver = %q", c.Database.Driver)
			}
			if c.Database.Redis.Pass != "" {
				t.Errorf("REDIS_PASSWORD null not normalized: %q", c.Database.Redis.Pass)
			}
			if c.Mail.Port != 0 {
				t.Errorf("MAIL_PORT null not normalized: %d", c.Mail.Port)
			}
			if c.Mail.From.Name != "Demo Team" {
				t.Errorf("From.Name not expanded: %q", c.Mail.From.Name)
			}
			if c.Auth.JWT.AccessTTLMin != 60 {
				t.Errorf("default TTL = %d, want 60", c.Auth.JWT.AccessTTLMin)
			}
		})
	})

	t.Run("load-rejects-empty-key", func(t *testing.T) {
		t.Setenv("APP_KEY", "")
		withEnvDir(t, "", func() {
			if _, err := Load(); err == nil {
				t.Fatal("expected error for empty APP_KEY")
			}
		})
	})

	t.Run("database-dsn", func(t *testing.T) {
		mysql := Database{Driver: "mysql", Host: "db", Port: "3306", Name: "app", User: "u", Pass: "p"}
		want := "u:p@tcp(db:3306)/app?charset=utf8mb4&parseTime=True&loc=Local"
		if got := mysql.DSN(); got != want {
			t.Errorf("mysql DSN = %q, want %q", got, want)
		}
		pg := Database{Driver: "pgsql", Host: "db", Port: "5432", Name: "app", User: "u", Pass: "p"}
		wantPG := "postgres://u:p@db:5432/app?sslmode=disable"
		if got := pg.DSN(); got != wantPG {
			t.Errorf("pgsql DSN = %q, want %q", got, wantPG)
		}
		if got := (Redis{Host: "r", Port: "6379"}).Addr(); got != "r:6379" {
			t.Errorf("redis addr = %q", got)
		}
	})
}
