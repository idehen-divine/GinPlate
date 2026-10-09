package cadmin

import (
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/idehen-divine/GinPlate/pkg/session"
)

func testAdminID() uuid.UUID { return uuid.MustParse("22222222-2222-2222-2222-222222222222") }

func base64Of32() string {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func resolveEmptyErr() string {
	_, _, err := ResolveSecret("", "")
	if err == nil {
		return ""
	}
	return err.Error()
}

// TestCadmin covers control token issuance/parsing and secret resolution
// without a database (Login/SeedAdmin need a live control DB).
func TestCadmin(t *testing.T) {
	const secret = "test-control-secret-at-least-32-chars!"

	t.Run("issue-parse-round-trip", func(t *testing.T) {
		svc := NewService([]byte(secret))
		a := &ControlAdmin{ID: testAdminID(), Email: "root@example.com", Role: "super_admin"}
		tok, err := svc.Issue(a)
		if err != nil {
			t.Fatal(err)
		}
		claims, err := svc.Parse(tok)
		if err != nil {
			t.Fatal(err)
		}
		if claims.AdminID != a.ID || claims.Role != "super_admin" || claims.Type != ControlTokenType {
			t.Fatalf("claims = %+v", claims)
		}
	})

	t.Run("parse-rejects-tenant-tokens", func(t *testing.T) {
		svc := NewService([]byte(secret))
		if _, err := svc.Parse("bogus.token.here"); err == nil {
			t.Fatal("garbage accepted")
		}
	})

	t.Run("resolve-secret", func(t *testing.T) {
		key, fallback, err := ResolveSecret("", "base64:"+base64Of32())
		if err != nil || !fallback || len(key) != 32 {
			t.Fatalf("fallback = %v,%v,%v", len(key), fallback, err)
		}
		if _, _, err := ResolveSecret("", "short"); err == nil {
			t.Fatal("short fallback accepted")
		}
		if _, fallback, err := ResolveSecret(secret, "ignored"); err != nil || fallback {
			t.Fatalf("explicit = %v,%v,%v", len(secret), fallback, err)
		}
		if !strings.Contains(resolveEmptyErr(), "empty") {
			t.Fatalf("empty = %q", resolveEmptyErr())
		}
	})

	t.Run("logout-revokes-session", func(t *testing.T) {
		store := newStubSessionStore()
		svc := NewService([]byte(secret)).WithStore(store)
		a := &ControlAdmin{ID: testAdminID(), Email: "root@example.com", Role: "super_admin"}
		// Login needs a DB; exercise link/logout directly around Issue.
		tok, err := svc.Issue(a)
		if err != nil {
			t.Fatal(err)
		}
		claims, err := svc.Parse(tok)
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		if err := store.Link(ctx, claims.SessionID, claims.SessionID, a.ID.String(), time.Hour, time.Hour); err != nil {
			t.Fatal(err)
		}
		if _, ok := store.AccessValid(ctx, claims.SessionID); !ok {
			t.Fatal("linked session invalid")
		}
		if err := svc.Logout(tok); err != nil {
			t.Fatalf("logout: %v", err)
		}
		if _, ok := store.AccessValid(ctx, claims.SessionID); ok {
			t.Fatal("logged-out session survived")
		}
		if err := svc.Logout("bogus"); err != nil {
			t.Fatalf("logout bogus: %v", err)
		}
	})
}

// stubSessionStore is a hermetic session.Store.
type stubSessionStore struct {
	mu       sync.Mutex
	sessions map[string]string
}

func newStubSessionStore() *stubSessionStore {
	return &stubSessionStore{sessions: map[string]string{}}
}

func (s *stubSessionStore) Link(_ context.Context, accessJti, refreshJti, _ string, _, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[accessJti] = refreshJti
	return nil
}

func (s *stubSessionStore) AccessValid(_ context.Context, accessJti string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rjti, ok := s.sessions[accessJti]
	return rjti, ok
}

func (s *stubSessionStore) RefreshValid(_ context.Context, refreshJti string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for a, r := range s.sessions {
		if r == refreshJti {
			return a, true
		}
	}
	return "", false
}

func (s *stubSessionStore) ConsumeRefresh(_ context.Context, refreshJti string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for a, r := range s.sessions {
		if r == refreshJti {
			delete(s.sessions, a)
			return a, true, nil
		}
	}
	return "", false, nil
}

func (s *stubSessionStore) ReplaceRefresh(_ context.Context, _, newAccessJti, newRefreshJti, _ string, _, _ time.Duration) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[newAccessJti] = newRefreshJti
	return "", true, nil
}

func (s *stubSessionStore) Unlink(_ context.Context, accessJti, refreshJti string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, accessJti)
	for a, r := range s.sessions {
		if r == refreshJti {
			delete(s.sessions, a)
		}
	}
	return nil
}

func (s *stubSessionStore) RevokeUser(_ context.Context, _ string) error { return nil }

var _ session.Store = (*stubSessionStore)(nil)
