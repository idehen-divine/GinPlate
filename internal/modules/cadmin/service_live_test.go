package cadmin

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/idehen-divine/GinPlate/internal/modules/auth"
	"github.com/idehen-divine/GinPlate/pkg/database"
	"github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/session"
)

// TestControlLive exercises seed/login/logout and forgot/reset end to end
// against a live database only when TEST_MYSQL_DSN or TEST_PGSQL_DSN is
// set. Tables mirror the goose migrations.
func TestControlLive(t *testing.T) {
	dsn, driver := os.Getenv("TEST_MYSQL_DSN"), "mysql"
	if dsn == "" {
		dsn, driver = os.Getenv("TEST_PGSQL_DSN"), "pgsql"
	}
	if dsn == "" {
		t.Skip("set TEST_MYSQL_DSN or TEST_PGSQL_DSN for the live control test")
	}
	db, err := database.Connect(driver, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	active := "TINYINT(1) NOT NULL DEFAULT 1"
	ts := "TIMESTAMP NULL DEFAULT NULL"
	now := "TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP"
	unique := "UNIQUE KEY uq_control_admins_email (email)"
	if driver == "pgsql" {
		active = "BOOLEAN NOT NULL DEFAULT TRUE"
		ts = "TIMESTAMPTZ"
		now = "TIMESTAMPTZ NOT NULL DEFAULT now()"
		unique = "UNIQUE (email)"
	}
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS password_reset_tokens`,
		`DROP TABLE IF EXISTS sessions`,
		`DROP TABLE IF EXISTS control_admins`,
		`CREATE TABLE control_admins (
			id CHAR(36) PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			email VARCHAR(255) NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			role VARCHAR(32) NOT NULL DEFAULT 'super_admin',
			is_active ` + active + `,
			created_at ` + now + `,
			updated_at ` + now + `,
			` + unique + `
		)`,
		`CREATE TABLE password_reset_tokens (
			email VARCHAR(255) NOT NULL,
			kind VARCHAR(16) NOT NULL DEFAULT 'user',
			token_hash VARCHAR(64) NOT NULL,
			expires_at ` + ts + `,
			used_at ` + ts + `,
			created_at ` + now + `,
			PRIMARY KEY (email, kind)
		)`,
		`CREATE TABLE sessions (
			id CHAR(36) PRIMARY KEY,
			tenant_id CHAR(36) NOT NULL DEFAULT '',
			refresh_jti CHAR(36) NOT NULL,
			user_id CHAR(36) NULL,
			access_expires_at ` + ts + `,
			refresh_expires_at ` + ts + `
		)`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DROP TABLE IF EXISTS password_reset_tokens`,
			`DROP TABLE IF EXISTS sessions`,
			`DROP TABLE IF EXISTS control_admins`,
		} {
			_ = db.Exec(stmt).Error
		}
	})

	ctx := context.Background()
	const secret = "test-control-secret-at-least-32-chars!"
	svc := NewService([]byte(secret)).
		WithStore(session.Database(db)).
		WithMailer(mail.NewLog("test@example.com", "Test"), "https://example.com", "Test")

	t.Run("seed-idempotent", func(t *testing.T) {
		a, created, err := SeedAdmin(db, "Root", "root@example.com", "controlpassword123")
		if err != nil || !created {
			t.Fatalf("seed = %+v,%v,%v", a, created, err)
		}
		_, created, err = SeedAdmin(db, "Root", "root@example.com", "controlpassword123")
		if err != nil || created {
			t.Fatalf("reseed = %v,%v", created, err)
		}
		if _, _, err := SeedAdmin(db, "Root", "other@example.com", "short"); err == nil {
			t.Fatal("short seed password accepted")
		}
	})

	t.Run("login-logout", func(t *testing.T) {
		a, tok, err := svc.Login(db, "root@example.com", "controlpassword123")
		if err != nil {
			t.Fatalf("login: %v", err)
		}
		claims, err := svc.Parse(tok)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		store := session.Database(db)
		if _, ok := store.AccessValid(ctx, claims.SessionID); !ok {
			t.Fatal("control session not tracked")
		}
		if _, _, err := svc.Login(db, "root@example.com", "wrongpassword123"); err == nil {
			t.Fatal("wrong password accepted")
		}
		if err := svc.Logout(tok); err != nil {
			t.Fatalf("logout: %v", err)
		}
		if _, ok := store.AccessValid(ctx, claims.SessionID); ok {
			t.Fatal("logged-out control session survived")
		}
		if err := svc.Logout("bogus"); err != nil {
			t.Fatalf("logout bogus: %v", err)
		}
		_ = a
	})

	t.Run("forgot-reset", func(t *testing.T) {
		if err := svc.ForgotPassword(db, "ghost@example.com"); err != nil {
			t.Fatalf("forgot unknown: %v", err)
		}
		if err := svc.ForgotPassword(db, "root@example.com"); err != nil {
			t.Fatalf("forgot: %v", err)
		}
		raw, err := auth.IssueResetToken(ctx, db, "root@example.com", auth.ResetKindControlAdmin, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.ResetPassword(db, raw, "newcontrolpassword123"); err != nil {
			t.Fatalf("reset: %v", err)
		}
		if _, _, err := svc.Login(db, "root@example.com", "newcontrolpassword123"); err != nil {
			t.Fatalf("login with new password: %v", err)
		}
		if err := svc.ResetPassword(db, raw, "anotherpassword123"); err == nil ||
			!strings.Contains(err.Error(), "Invalid or expired") {
			t.Fatalf("replay: %v", err)
		}
		// A tenant-kind row for the same address never satisfies control reset.
		userRaw, err := auth.IssueResetToken(ctx, db, "root@example.com", auth.ResetKindUser, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.ResetPassword(db, userRaw, "anotherpassword123"); err == nil ||
			!strings.Contains(err.Error(), "Invalid or expired") {
			t.Fatalf("cross-kind: %v", err)
		}
		_ = db.Where("email = ?", "root@example.com").Delete(&auth.ResetToken{})
	})
}
