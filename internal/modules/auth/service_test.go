package auth

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/idehen-divine/GinPlate/internal/modules/users"
	"github.com/idehen-divine/GinPlate/pkg/session"
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
		pair, err := svc.issue(testUser())
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
		pair, err := svc.issue(testUser())
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
		pair, err := svc.issue(testUser())
		if err != nil {
			t.Fatal(err)
		}
		ajti, rjti := jtis(t, []byte("test-secret-at-least-32-chars-long!!"), pair)
		if err := store.Unlink(context.Background(), ajti, rjti); err != nil {
			t.Fatal(err)
		}
		if _, _, err := svc.Refresh(nil, pair.RefreshToken); err == nil || err.Error() != "Session revoked." {
			t.Fatalf("revoked refresh: got %v, want Session revoked.", err)
		}
	})

	t.Run("refresh-rejects-garbage", func(t *testing.T) {
		svc, _ := testService(t)
		if _, _, err := svc.Refresh(nil, "bogus.token.here"); err == nil {
			t.Fatal("expected error for malformed token")
		}
	})
}
