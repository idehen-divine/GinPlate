package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/idehen-divine/GinPlate/internal/modules/users"
	"github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/session"
	"github.com/idehen-divine/GinPlate/pkg/web"
	"github.com/redis/go-redis/v9"
)

// testService returns a Service backed by an in-memory Redis through the
// session interface. No database is involved: tests below never reach the
// DB layer.
func testService(t *testing.T) (*Service, session.Store) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewService([]byte("test-secret-at-least-32-chars-long!!"), 60, session.Redis(rdb)), session.Redis(rdb)
}

func testUser() *users.User {
	return &users.User{ID: uuid.New(), Name: "Ada", Email: "ada@example.com", Role: "member", IsActive: true}
}

// jtis parses both tokens of a pair and returns (accessJti, refreshJti).
func jtis(t *testing.T, secret []byte, pair *TokenPair) (string, string) {
	t.Helper()
	parse := func(tok string) string {
		parsed, err := jwt.ParseWithClaims(tok, jwt.MapClaims{}, func(t *jwt.Token) (interface{}, error) {
			return secret, nil
		})
		if err != nil || !parsed.Valid {
			t.Fatalf("parse token: %v", err)
		}
		jti, _ := parsed.Claims.(jwt.MapClaims)["jti"].(string)
		if jti == "" {
			t.Fatal("token has no jti")
		}
		return jti
	}
	return parse(pair.AccessToken), parse(pair.RefreshToken)
}

// TestAuth is the single entry point for every auth service test: session
// linking on issue, logout invalidation, and refresh rejection for revoked
// or malformed tokens.
func TestAuth(t *testing.T) {
	t.Run("issue-links-both-halves", func(t *testing.T) {
		svc, store := testService(t)
		pair, err := svc.issue(testUser(), "acme")
		if err != nil {
			t.Fatal(err)
		}
		ajti, rjti := jtis(t, []byte("test-secret-at-least-32-chars-long!!"), pair)
		ctx := context.Background()
		if got, ok := store.AccessValid(ctx, ajti); !ok || got != rjti {
			t.Fatalf("access half = %q,%v; want refresh jti %q", got, ok, rjti)
		}
		if got, ok := store.RefreshValid(ctx, rjti); !ok || got != ajti {
			t.Fatalf("refresh half = %q,%v; want access jti %q", got, ok, ajti)
		}
	})

	t.Run("logout-kills-both-halves", func(t *testing.T) {
		svc, store := testService(t)
		pair, err := svc.issue(testUser(), "acme")
		if err != nil {
			t.Fatal(err)
		}
		ajti, rjti := jtis(t, []byte("test-secret-at-least-32-chars-long!!"), pair)
		svc.Logout(ajti)
		ctx := context.Background()
		if _, ok := store.AccessValid(ctx, ajti); ok {
			t.Fatal("access half survived logout")
		}
		if _, ok := store.RefreshValid(ctx, rjti); ok {
			t.Fatal("refresh half survived logout")
		}
		svc.Logout(ajti) // idempotent: no panic, no error
	})

	t.Run("refresh-rejects-revoked", func(t *testing.T) {
		svc, store := testService(t)
		pair, err := svc.issue(testUser(), "acme")
		if err != nil {
			t.Fatal(err)
		}
		ajti, rjti := jtis(t, []byte("test-secret-at-least-32-chars-long!!"), pair)
		if err := store.Unlink(context.Background(), ajti, rjti); err != nil {
			t.Fatal(err)
		}
		if _, _, err := svc.Refresh(nil, pair.RefreshToken, "acme"); err == nil || err.Error() != "Session revoked." {
			t.Fatalf("revoked refresh: got %v, want Session revoked.", err)
		}
	})

	t.Run("refresh-rejects-garbage", func(t *testing.T) {
		svc, _ := testService(t)
		if _, _, err := svc.Refresh(nil, "bogus.token.here", "acme"); err == nil {
			t.Fatal("expected error for malformed token")
		}
	})

	t.Run("refresh-single-use", func(t *testing.T) {
		svc, store := testService(t)
		pair, err := svc.issue(testUser(), "acme")
		if err != nil {
			t.Fatal(err)
		}
		_, rjti := jtis(t, []byte("test-secret-at-least-32-chars-long!!"), pair)
		// Concurrent rotations of the same refresh half: exactly one wins,
		// and the winner's replacement halves validate while the old halves
		// do not.
		const racers = 8
		type outcome struct {
			ok         bool
			newA, newR string
		}
		won := make(chan outcome, racers)
		for i := 0; i < racers; i++ {
			go func() {
				newA, newR := uuid.NewString(), uuid.NewString()
				_, ok, err := store.ReplaceRefresh(context.Background(), rjti, newA, newR, "u1", time.Minute, time.Hour)
				if err != nil {
					t.Error(err)
					won <- outcome{}
					return
				}
				won <- outcome{ok: ok, newA: newA, newR: newR}
			}()
		}
		var winner outcome
		wins := 0
		for i := 0; i < racers; i++ {
			if o := <-won; o.ok {
				wins++
				winner = o
			}
		}
		if wins != 1 {
			t.Fatalf("concurrent refresh wins = %d, want exactly 1", wins)
		}
		ctx := context.Background()
		if _, ok := store.RefreshValid(ctx, rjti); ok {
			t.Fatal("old refresh half survived replacement")
		}
		if got, ok := store.RefreshValid(ctx, winner.newR); !ok || got != winner.newA {
			t.Fatalf("replacement halves = %q,%v", got, ok)
		}
		// The consumed token now reports revoked through the service.
		if _, _, err := svc.Refresh(nil, pair.RefreshToken, "acme"); err == nil || err.Error() != "Session revoked." {
			t.Fatalf("replayed refresh: got %v, want Session revoked.", err)
		}
	})

	t.Run("refresh-rejects-foreign-tenant", func(t *testing.T) {
		svc, _ := testService(t)
		pair, err := svc.issue(testUser(), "acme")
		if err != nil {
			t.Fatal(err)
		}
		// Same token under another tenant: cross-tenant replay, rejected
		// before any session or database access.
		if _, _, err := svc.Refresh(nil, pair.RefreshToken, "globex"); err == nil || err.Error() != "Invalid refresh token." {
			t.Fatalf("foreign refresh: got %v, want Invalid refresh token.", err)
		}
	})

	t.Run("refresh-rejects-empty-slug", func(t *testing.T) {
		svc, _ := testService(t)
		pair, err := svc.issue(testUser(), "acme")
		if err != nil {
			t.Fatal(err)
		}
		// Unresolved tenant is a server misconfiguration: 500, never 401.
		_, _, err = svc.Refresh(nil, pair.RefreshToken, "")
		var ae *web.AppError
		if err == nil || !errors.As(err, &ae) || ae.Status != http.StatusInternalServerError {
			t.Fatalf("empty-slug refresh: got %v, want 500", err)
		}
	})

	t.Run("refresh-rejects-empty-slug-even-when-tid-empty", func(t *testing.T) {
		svc, _ := testService(t)
		pair, err := svc.issue(testUser(), "")
		if err != nil {
			t.Fatal(err)
		}
		// tid=="" must not match slug=="": empty callers always fail 500.
		_, _, err = svc.Refresh(nil, pair.RefreshToken, "")
		var ae *web.AppError
		if err == nil || !errors.As(err, &ae) || ae.Status != http.StatusInternalServerError {
			t.Fatalf("empty==empty refresh: got %v, want 500", err)
		}
	})

	t.Run("refresh-rejects-access-token", func(t *testing.T) {
		svc, _ := testService(t)
		pair, err := svc.issue(testUser(), "acme")
		if err != nil {
			t.Fatal(err)
		}
		// Access tokens are never valid refresh tokens, even under the
		// right tenant.
		if _, _, err := svc.Refresh(nil, pair.AccessToken, "acme"); err == nil || err.Error() != "Invalid refresh token." {
			t.Fatalf("access-as-refresh: got %v, want Invalid refresh token.", err)
		}
	})

	t.Run("reset-token-hash", func(t *testing.T) {
		raw, err := MintResetToken()
		if err != nil {
			t.Fatal(err)
		}
		if raw == "" {
			t.Fatal("empty reset token")
		}
		h1 := HashResetToken(raw)
		h2 := HashResetToken(raw)
		if len(h1) != 64 || h1 != h2 {
			t.Fatalf("unstable hash: %q vs %q", h1, h2)
		}
		if h1 == raw {
			t.Fatal("hash must not equal raw token")
		}
		if HashResetToken(raw+"x") == h1 {
			t.Fatal("distinct tokens must hash distinctly")
		}
	})
}

// stubSender accepts any message, for mailer-injection tests.
type stubSender struct {
	got *mail.Message
}

func (s stubSender) Send(_ context.Context, msg mail.Message) error {
	if s.got != nil {
		*s.got = msg
	}
	return nil
}

func TestSendPasswordResetNeedsMailer(t *testing.T) {
	svc, _ := testService(t)
	if err := svc.SendPasswordReset(context.Background(), "https://app.com", "Ada", "a@b.c", "tok", 60); err == nil {
		t.Fatal("nil mailer: expected error")
	}
}

func TestSendPasswordResetDelivers(t *testing.T) {
	svc, _ := testService(t)
	var got mail.Message
	svc.WithMailer(stubSender{got: &got})
	if err := svc.SendPasswordReset(context.Background(), "https://app.com", "Ada", "a@b.c", "tok", 60); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(got.To) != 1 || got.To[0] != "a@b.c" {
		t.Fatalf("to = %v", got.To)
	}
}
