package auth

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/idehen-divine/GinPlate/pkg/database"
	"github.com/idehen-divine/GinPlate/pkg/session"
)

// TestPasswordResetLive exercises forgot/reset end to end against a live
// database only when TEST_MYSQL_DSN or TEST_PGSQL_DSN is set: token issue,
// generic failures, expiry, single use, session revocation, and kind
// isolation between audiences. Tables mirror the goose migrations.
func TestPasswordResetLive(t *testing.T) {
	dsn, driver := os.Getenv("TEST_MYSQL_DSN"), "mysql"
	if dsn == "" {
		dsn, driver = os.Getenv("TEST_PGSQL_DSN"), "pgsql"
	}
	if dsn == "" {
		t.Skip("set TEST_MYSQL_DSN or TEST_PGSQL_DSN for the live password-reset test")
	}
	db, err := database.Connect(driver, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS password_reset_tokens`,
		`DROP TABLE IF EXISTS sessions`,
		`DROP TABLE IF EXISTS users`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatal(err)
		}
	}
	if driver == "pgsql" {
		for _, stmt := range []string{
			`CREATE TABLE users (
				id CHAR(36) PRIMARY KEY,
				tenant_id CHAR(36) NOT NULL DEFAULT '',
				name VARCHAR(255) NOT NULL,
				email VARCHAR(255) NOT NULL,
				password_hash VARCHAR(255) NOT NULL,
				role VARCHAR(32) NOT NULL DEFAULT 'member',
				is_active BOOLEAN NOT NULL DEFAULT TRUE,
				created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
			)`,
			`CREATE UNIQUE INDEX uq_users_tenant_email ON users(tenant_id, email)`,
			`CREATE TABLE password_reset_tokens (
				email VARCHAR(255) NOT NULL,
				kind VARCHAR(16) NOT NULL DEFAULT 'user',
				token_hash VARCHAR(64) NOT NULL,
				expires_at TIMESTAMPTZ,
				used_at TIMESTAMPTZ,
				created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
				PRIMARY KEY (email, kind)
			)`,
			`CREATE TABLE sessions (
				id CHAR(36) PRIMARY KEY,
				tenant_id CHAR(36) NOT NULL DEFAULT '',
				refresh_jti CHAR(36) NOT NULL,
				user_id CHAR(36) NULL,
				access_expires_at TIMESTAMPTZ,
				refresh_expires_at TIMESTAMPTZ
			)`,
		} {
			if err := db.Exec(stmt).Error; err != nil {
				t.Fatal(err)
			}
		}
	} else {
		for _, stmt := range []string{
			`CREATE TABLE users (
				id CHAR(36) PRIMARY KEY,
				tenant_id CHAR(36) NOT NULL DEFAULT '',
				name VARCHAR(255) NOT NULL,
				email VARCHAR(255) NOT NULL,
				password_hash VARCHAR(255) NOT NULL,
				role VARCHAR(32) NOT NULL DEFAULT 'member',
				is_active TINYINT(1) NOT NULL DEFAULT 1,
				created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
				UNIQUE KEY uq_users_tenant_email (tenant_id, email)
			)`,
			`CREATE TABLE password_reset_tokens (
				email VARCHAR(255) NOT NULL,
				kind VARCHAR(16) NOT NULL DEFAULT 'user',
				token_hash VARCHAR(64) NOT NULL,
				expires_at TIMESTAMP NULL DEFAULT NULL,
				used_at TIMESTAMP NULL DEFAULT NULL,
				created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
				PRIMARY KEY (email, kind)
			)`,
			`CREATE TABLE sessions (
				id CHAR(36) PRIMARY KEY,
				tenant_id CHAR(36) NOT NULL DEFAULT '',
				refresh_jti CHAR(36) NOT NULL,
				user_id CHAR(36) NULL,
				access_expires_at TIMESTAMP NULL DEFAULT NULL,
				refresh_expires_at TIMESTAMP NULL DEFAULT NULL
			)`,
		} {
			if err := db.Exec(stmt).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DROP TABLE IF EXISTS password_reset_tokens`,
			`DROP TABLE IF EXISTS sessions`,
			`DROP TABLE IF EXISTS users`,
		} {
			_ = db.Exec(stmt).Error
		}
	})

	ctx := context.Background()
	svc := NewService([]byte("test-secret-at-least-32-chars-long!!"), 60, session.Database(db))

	u, err := svc.Register(db, SignupDTO{Name: "Ada", Email: "ada@example.com", Password: "password123"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	_, pair, err := svc.Login(db, LoginDTO{Email: "ada@example.com", Password: "password123"}, "acme")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	accessJti, _ := jtis(t, []byte("test-secret-at-least-32-chars-long!!"), pair)

	t.Run("forgot-unknown-is-silent", func(t *testing.T) {
		if err := svc.ForgotPassword(db, "ghost@example.com"); err != nil {
			t.Fatalf("forgot unknown: %v", err)
		}
		var n int64
		if err := db.Table("password_reset_tokens").Count(&n).Error; err != nil || n != 0 {
			t.Fatalf("rows = %d,%v", n, err)
		}
	})

	t.Run("forgot-issues-row", func(t *testing.T) {
		if err := svc.ForgotPassword(db, "ada@example.com"); err != nil {
			t.Fatalf("forgot: %v", err)
		}
		var row ResetToken
		if err := db.Where("email = ? AND kind = ?", "ada@example.com", ResetKindUser).First(&row).Error; err != nil {
			t.Fatalf("no token row: %v", err)
		}
		_ = u
	})

	t.Run("reset-round-trip", func(t *testing.T) {
		raw, err := IssueResetToken(ctx, db, "ada@example.com", ResetKindUser, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.ResetPassword(db, "bogus", "newpassword123"); err == nil ||
			!strings.Contains(err.Error(), "Invalid or expired") {
			t.Fatalf("bogus token: %v", err)
		}
		if err := svc.ResetPassword(db, raw, "newpassword123"); err != nil {
			t.Fatalf("reset: %v", err)
		}
		if _, _, err := svc.Login(db, LoginDTO{Email: "ada@example.com", Password: "newpassword123"}, "acme"); err != nil {
			t.Fatalf("login with new password: %v", err)
		}
		if _, _, err := svc.Login(db, LoginDTO{Email: "ada@example.com", Password: "password123"}, "acme"); err == nil {
			t.Fatal("old password still works")
		}
		store := session.Database(db)
		if _, ok := store.AccessValid(ctx, accessJti); ok {
			t.Fatal("pre-reset session survived")
		}
		if err := svc.ResetPassword(db, raw, "anotherpassword123"); err == nil ||
			!strings.Contains(err.Error(), "Invalid or expired") {
			t.Fatalf("replay: %v", err)
		}
	})

	t.Run("expiry-and-kind", func(t *testing.T) {
		raw, err := IssueResetToken(ctx, db, "ada@example.com", ResetKindUser, -time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.ResetPassword(db, raw, "newpassword123"); err == nil ||
			!strings.Contains(err.Error(), "Invalid or expired") {
			t.Fatalf("expired: %v", err)
		}
		fresh, err := IssueResetToken(ctx, db, "ada@example.com", ResetKindUser, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err := ConsumeResetToken(ctx, db, "ada@example.com", ResetKindControlAdmin, fresh); err != ErrResetInvalid {
			t.Fatalf("cross-kind consume: %v", err)
		}
		_ = db.Where("email = ? AND kind = ?", "ada@example.com", ResetKindUser).Delete(&ResetToken{})
	})
}
