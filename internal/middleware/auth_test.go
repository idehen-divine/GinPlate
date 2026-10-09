package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// stubSessionStore is a hermetic session.Store for middleware tests.
type stubSessionStore struct {
	mu       sync.Mutex
	sessions map[string]string // accessJti -> refreshJti
	refresh  map[string]string // refreshJti -> accessJti
}

func newStubSessionStore() *stubSessionStore {
	return &stubSessionStore{sessions: map[string]string{}, refresh: map[string]string{}}
}

func (s *stubSessionStore) Link(_ context.Context, accessJti, refreshJti, _ string, _, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[accessJti] = refreshJti
	s.refresh[refreshJti] = accessJti
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
	ajti, ok := s.refresh[refreshJti]
	return ajti, ok
}

func (s *stubSessionStore) ConsumeRefresh(_ context.Context, refreshJti string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ajti, ok := s.refresh[refreshJti]
	if !ok {
		return "", false, nil
	}
	delete(s.refresh, refreshJti)
	delete(s.sessions, ajti)
	return ajti, true, nil
}

func (s *stubSessionStore) Unlink(_ context.Context, accessJti, refreshJti string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, accessJti)
	delete(s.refresh, refreshJti)
	return nil
}

func (s *stubSessionStore) ReplaceRefresh(_ context.Context, oldRefreshJti, newAccessJti, newRefreshJti, _ string, _, _ time.Duration) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ajti, ok := s.refresh[oldRefreshJti]
	if !ok {
		return "", false, nil
	}
	delete(s.refresh, oldRefreshJti)
	delete(s.sessions, ajti)
	s.sessions[newAccessJti] = newRefreshJti
	s.refresh[newRefreshJti] = newAccessJti
	return ajti, true, nil
}

func testCtx(t *testing.T, method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	return c, w
}

// signAccess mints a valid access token for role with a linked session.
func signAccess(t *testing.T, key []byte, store *stubSessionStore, uid, role string) string {
	t.Helper()
	accessJTI, refreshJTI := uuid.NewString(), uuid.NewString()
	if err := store.Link(context.Background(), accessJTI, refreshJTI, uid, time.Hour, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": uid, "jti": accessJTI, "ver": TokenVersion, "type": "access", "role": role,
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	})
	signed, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

// TestMiddlewareChain proves a route accepts multiple middlewares in order:
// RequireAuth → RequireRole → custom. Each stage runs only when the previous
// called c.Next().
func TestMiddlewareChain(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	store := newStubSessionStore()
	admin := signAccess(t, key, store, uuid.NewString(), "admin")
	member := signAccess(t, key, store, uuid.NewString(), "member")

	run := func(token string) (int, bool) {
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/users", nil)
		if token != "" {
			c.Request.Header.Set("Authorization", "Bearer "+token)
		}
		ran := false
		custom := func(c *gin.Context) {
			ran = true
			c.Next()
		}
		// Same shape as routes.go: chain as many as the route needs.
		for _, mw := range []gin.HandlerFunc{RequireAuth(key, store), RequireRole(RoleAdmin), custom} {
			mw(c)
			if c.IsAborted() {
				break
			}
		}
		return w.Code, ran
	}

	if code, ran := run(admin); code != http.StatusOK || !ran {
		t.Fatalf("admin = %d ran=%v, want 200 true", code, ran)
	}
	if code, ran := run(""); code != http.StatusUnauthorized || ran {
		t.Fatalf("anonymous = %d ran=%v, want 401 false", code, ran)
	}
	if code, ran := run(member); code != http.StatusForbidden || ran {
		t.Fatalf("member = %d ran=%v, want 403 false", code, ran)
	}
}

// TestAuth is the single entry point for auth guard tests: role gating,
// stale versions, refresh-as-access, wrong algorithms, and revocation.
func TestAuth(t *testing.T) {
	t.Run("require-role", func(t *testing.T) {
		run := func(role Role) *httptest.ResponseRecorder {
			c, w := testCtx(t, "GET", "/users")
			if role != "" {
				c.Set("claims", &Claims{UserID: uuid.New(), Role: role})
			}
			RequireRole("admin")(c)
			return w
		}
		if w := run(""); w.Code != http.StatusUnauthorized {
			t.Fatalf("no claims = %d, want 401", w.Code)
		}
		if w := run("member"); w.Code != http.StatusForbidden {
			t.Fatalf("member = %d, want 403", w.Code)
		}
		if w := run("admin"); w.Code != http.StatusOK {
			t.Fatalf("admin = %d, want 200", w.Code)
		}
	})

	t.Run("stale-version-rejected", func(t *testing.T) {
		key := []byte("0123456789abcdef0123456789abcdef")
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": uuid.NewString(), "ver": "v0", "type": "access",
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		})
		signed, err := tok.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		c, w := testCtx(t, "GET", "/users")
		c.Request.Header.Set("Authorization", "Bearer "+signed)
		RequireAuth(key, nil)(c)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("stale version = %d, want 401", w.Code)
		}
	})

	t.Run("refresh-rejected-as-access", func(t *testing.T) {
		key := []byte("0123456789abcdef0123456789abcdef")
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": uuid.NewString(), "jti": uuid.NewString(), "ver": TokenVersion, "type": "refresh",
			"iat": time.Now().Unix(), "exp": time.Now().Add(24 * time.Hour).Unix(),
		})
		signed, err := tok.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		c, w := testCtx(t, "GET", "/users")
		c.Request.Header.Set("Authorization", "Bearer "+signed)
		RequireAuth(key, nil)(c)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("refresh as access = %d, want 401", w.Code)
		}
	})

	t.Run("wrong-alg-rejected", func(t *testing.T) {
		key := []byte("0123456789abcdef0123456789abcdef")
		tok := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
			"sub": uuid.NewString(), "jti": uuid.NewString(), "ver": TokenVersion, "type": "access",
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		})
		signed, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
		if err != nil {
			t.Fatal(err)
		}
		c, w := testCtx(t, "GET", "/users")
		c.Request.Header.Set("Authorization", "Bearer "+signed)
		RequireAuth(key, nil)(c)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("none alg = %d, want 401", w.Code)
		}
	})

	t.Run("revoked-rejected", func(t *testing.T) {
		key := []byte("0123456789abcdef0123456789abcdef")
		store := newStubSessionStore()
		uid := uuid.NewString()
		accessJTI := uuid.NewString()
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": uid, "jti": accessJTI, "ver": TokenVersion, "type": "access",
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		})
		signed, err := tok.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		// No session linked: revoked/unknown must fail closed.
		c, w := testCtx(t, "GET", "/users")
		c.Request.Header.Set("Authorization", "Bearer "+signed)
		RequireAuth(key, store)(c)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("revoked = %d, want 401", w.Code)
		}
	})
}
