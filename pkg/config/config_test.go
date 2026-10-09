package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
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
		plain := Database{Driver: "mysql", Host: "db", Port: "3306", Name: "app", User: "u", Pass: "p", SSLMode: "disable"}
		parsed, err := mysqldriver.ParseDSN(plain.DSN())
		if err != nil {
			t.Fatalf("parse plain DSN: %v", err)
		}
		if parsed.User != "u" || parsed.Passwd != "p" || parsed.Net != "tcp" ||
			parsed.Addr != "db:3306" || parsed.DBName != "app" || parsed.TLSConfig != "false" || !parsed.ParseTime {
			t.Errorf("plain DSN parsed = %+v", parsed)
		}
		for mode, wantTLS := range map[string]string{
			"verify-full": "ginplate-verify", "verify-ca": "ginplate-verify",
			"require": "skip-verify", "true": "skip-verify", "skip-verify": "skip-verify",
			"disable": "false", "": "false", "bogus": "false",
		} {
			d := Database{Driver: "mysql", Host: "db", Port: "3306", Name: "app", User: "u", Pass: "p", SSLMode: mode}
			if got := d.DSN(); !strings.Contains(got, "tls="+wantTLS) {
				t.Errorf("mode %q DSN = %q, want tls=%s", mode, got, wantTLS)
			}
		}
		pg := Database{Driver: "pgsql", Host: "db", Port: "5432", Name: "app", User: "u", Pass: "p@ss:w?rd", SSLMode: "verify-full"}
		got := pg.DSN()
		wantPG := "postgres://u:p%40ss%3Aw%3Frd@db:5432/app?sslmode=verify-full"
		if got != wantPG {
			t.Errorf("pgsql DSN = %q, want %q", got, wantPG)
		}
		if got := (Redis{Host: "r", Port: "6379"}).Addr(); got != "r:6379" {
			t.Errorf("redis addr = %q", got)
		}
	})

	t.Run("database-dsn-escapes-credentials", func(t *testing.T) {
		// Every DSN-significant character class: the driver splits userinfo
		// on the first ':' and the last '@' before the last '/', so
		// passwords may contain anything while usernames may contain
		// anything but ':' (a ':' is the user/password separator by DSN
		// grammar and cannot appear in the username portion).
		for _, creds := range [][2]string{
			{"u@exa mple", "p@ss:w/o?rd%25"},
			{"user", "a/b?c@d:e%f g"},
			{"x@tcp(evil)", "y"},
		} {
			tricky := Database{
				Driver: "mysql", Host: "db", Port: "3306", Name: "app",
				User: creds[0], Pass: creds[1], SSLMode: "disable",
			}
			dsn := tricky.DSN()
			parsed, err := mysqldriver.ParseDSN(dsn)
			if err != nil {
				t.Fatalf("parse DSN %q: %v", dsn, err)
			}
			if parsed.User != creds[0] || parsed.Passwd != creds[1] {
				t.Errorf("round-trip = user %q pass %q, want %q %q",
					parsed.User, parsed.Passwd, creds[0], creds[1])
			}
			if parsed.Net != "tcp" || parsed.Addr != "db:3306" || parsed.DBName != "app" {
				t.Errorf("injected net/addr/dbname: %+v", parsed)
			}
		}
	})

	t.Run("validate-requires-verifying-tls", func(t *testing.T) {
		safe := func() *Config {
			c := &Config{}
			c.App.Env = "production"
			c.App.URL = "https://app.example.com"
			c.App.Port = "8080"
			c.App.HTTP.ReadTimeoutSec = 15
			c.App.HTTP.WriteTimeoutSec = 15
			c.App.HTTP.IdleTimeoutSec = 60
			c.App.HTTP.ShutdownTimeoutSec = 5
			c.App.HTTP.CORSAllowedOrigins = "https://app.example.com"
			c.Mail.Mailer = "smtp"
			c.Session.Driver = "redis"
			c.Database.Redis.Host = "redis"
			c.Database.Host = "db"
			c.Database.MaxOpenConns = 20
			c.Database.MaxIdleConns = 5
			c.Database.ConnMaxLifetime = 1800
			c.Database.ConnMaxIdleTime = 300
			return c
		}
		for _, mode := range []string{"require", "skip-verify", "true", "disable", ""} {
			c := safe()
			c.Database.SSLMode = mode
			if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "DB_SSLMODE") {
				t.Errorf("mode %q accepted or wrong error: %v", mode, err)
			}
		}
		c := safe()
		c.Database.SSLMode = "verify-ca"
		if err := c.Validate(); err != nil {
			t.Errorf("verify-ca rejected: %v", err)
		}
		c = safe()
		c.Database.SSLMode = "verify-full"
		c.Database.SSLRootCert = filepath.Join(t.TempDir(), "missing.pem")
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "DB_SSLROOTCERT") {
			t.Errorf("unreadable CA path accepted: %v", err)
		}
	})

	t.Run("validate-tls-registration-fails-closed", func(t *testing.T) {
		// Verifying mode with an unreadable CA bundle must fail boot in any
		// environment — never silently downgrade to skip-verify.
		d := Database{Driver: "mysql", Host: "db", SSLMode: "verify-full", SSLRootCert: filepath.Join(t.TempDir(), "missing.pem")}
		if err := d.ValidateTLS(); err == nil {
			t.Fatal("expected TLS registration error for missing CA bundle")
		}
		// Non-verifying modes and non-mysql drivers skip registration.
		d = Database{Driver: "mysql", Host: "db", SSLMode: "disable"}
		if err := d.ValidateTLS(); err != nil {
			t.Fatalf("disable must skip registration: %v", err)
		}
		d = Database{Driver: "pgsql", Host: "db", SSLMode: "verify-full"}
		if err := d.ValidateTLS(); err != nil {
			t.Fatalf("pgsql must skip mysql registration: %v", err)
		}
		// A valid verifying config registers without error.
		d = Database{Driver: "mysql", Host: "db", SSLMode: "verify-ca"}
		if err := d.ValidateTLS(); err != nil {
			t.Fatalf("verify-ca rejected: %v", err)
		}
	})

	t.Run("validate-rejects-unsafe-production", func(t *testing.T) {
		c := &Config{}
		c.App.Env = "production"
		c.App.URL = "http://localhost:8080"
		c.App.Port = "8080"
		c.App.HTTP.ReadTimeoutSec = 15
		c.App.HTTP.WriteTimeoutSec = 15
		c.App.HTTP.IdleTimeoutSec = 60
		c.App.HTTP.ShutdownTimeoutSec = 5
		if err := c.Validate(); err == nil {
			t.Fatal("expected production validation to fail for http URL + empty CORS + disabled TLS")
		}
		c.App.URL = "https://app.example.com"
		c.App.HTTP.CORSAllowedOrigins = "https://app.example.com"
		c.Database.SSLMode = "verify-full"
		c.Database.Host = "db"
		c.Database.MaxOpenConns = 20
		c.Database.MaxIdleConns = 5
		c.Database.ConnMaxLifetime = 1800
		c.Database.ConnMaxIdleTime = 300
		c.Mail.Mailer = "smtp"
		c.Session.Driver = "redis"
		c.Database.Redis.Host = "redis"
		if err := c.Validate(); err != nil {
			t.Fatalf("safe production config rejected: %v", err)
		}
	})

	t.Run("public-root-separation", func(t *testing.T) {
		c := &Config{}
		c.Filesystem.PublicRoot = "storage/app/public"
		c.App.Maintenance.Path = "storage/app/public/down"
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "public disk root") {
			t.Fatalf("marker under public root accepted: %v", err)
		}
		c.App.Maintenance.Path = "storage/framework/down"
		c.Logging.Output = "storage/logs"
		if err := c.Validate(); err != nil {
			t.Fatalf("default layout rejected: %v", err)
		}
	})

	t.Run("hardened-envs", func(t *testing.T) {
		for env, want := range map[string]bool{
			"production": true, "prod": true, "staging": true, "stage": true,
			"STAGING": true, "local": false, "test": false, "dev": false, "": false,
		} {
			if got := IsHardenedEnv(env); got != want {
				t.Errorf("IsHardenedEnv(%q) = %v, want %v", env, got, want)
			}
		}
	})

	t.Run("validate-rejects-unsafe-staging", func(t *testing.T) {
		c := &Config{}
		c.App.Env = "staging"
		c.App.Debug = true
		c.App.URL = "http://localhost:8080"
		c.App.Port = "8080"
		c.App.HTTP.ReadTimeoutSec = 15
		c.App.HTTP.WriteTimeoutSec = 15
		c.App.HTTP.IdleTimeoutSec = 60
		c.App.HTTP.ShutdownTimeoutSec = 5
		c.Database.MaxOpenConns = 20
		c.Database.MaxIdleConns = 5
		c.Database.ConnMaxLifetime = 1800
		c.Database.ConnMaxIdleTime = 300
		if err := c.Validate(); err == nil {
			t.Fatal("expected staging validation to fail for debug + http URL")
		}
		// Local stays permissive with the same shape.
		c.App.Env = "local"
		if err := c.Validate(); err != nil {
			t.Fatalf("local config rejected: %v", err)
		}
	})
}
