package tenancy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func guardCtx(t *testing.T, method, target string, tenant *Tenant) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	if tenant != nil {
		c.Set("tenant", tenant)
		c.Set("tenantSlug", tenant.Slug)
	}
	return c, w
}

// TestGuards covers the migration write-shed without any database.
func TestGuards(t *testing.T) {
	migrating := &Tenant{ID: uuid.New(), Slug: "acme", Status: StatusMigrating}
	active := &Tenant{ID: uuid.New(), Slug: "acme", Status: StatusActive}

	t.Run("migrating-blocks-writes", func(t *testing.T) {
		c, w := guardCtx(t, http.MethodPost, "/api/v1/users", migrating)
		BlockWritesWhenMigrating()(c)
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429", w.Code)
		}
		if w.Header().Get("Retry-After") != "120" {
			t.Fatalf("Retry-After = %q", w.Header().Get("Retry-After"))
		}
	})

	t.Run("migrating-allows-reads", func(t *testing.T) {
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
			c, w := guardCtx(t, method, "/api/v1/users", migrating)
			BlockWritesWhenMigrating()(c)
			if w.Code == http.StatusTooManyRequests {
				t.Fatalf("%s blocked", method)
			}
		}
	})

	t.Run("admin-passes-through", func(t *testing.T) {
		c, w := guardCtx(t, http.MethodPost, "/api/v1/admin/tenants", migrating)
		BlockWritesWhenMigrating()(c)
		if w.Code == http.StatusTooManyRequests {
			t.Fatal("admin path blocked")
		}
	})

	t.Run("active-passes", func(t *testing.T) {
		c, w := guardCtx(t, http.MethodPost, "/api/v1/users", active)
		BlockWritesWhenMigrating()(c)
		if w.Code == http.StatusTooManyRequests {
			t.Fatal("active tenant blocked")
		}
	})

	t.Run("slug-validation", func(t *testing.T) {
		// Slugs normalize (trim + lowercase) before validation.
		for _, ok := range []string{"acme", "a1", "x-9-z", "ACME", " Acme "} {
			if !IsValidSlug(ok) {
				t.Errorf("%q rejected", ok)
			}
		}
		for _, bad := range []string{"", "AC ME", "../evil", "a/b", strings.Repeat("a", 64), "-lead"} {
			if IsValidSlug(bad) {
				t.Errorf("%q accepted", bad)
			}
		}
	})
}

// TestAppDSN covers application-role DSN swapping without a database.
func TestAppDSN(t *testing.T) {
	t.Run("empty-user-passthrough", func(t *testing.T) {
		dsn := "postgres://owner:pw@db:5432/app?sslmode=require"
		if got := appDSN("pgsql", dsn, "", ""); got != dsn {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("pgsql-swaps-user", func(t *testing.T) {
		got := appDSN("pgsql", "postgres://owner:pw@db:5432/app?sslmode=require", "app", "s3cret")
		if !strings.Contains(got, "app:s3cret@") || strings.Contains(got, "owner") {
			t.Fatalf("got %q", got)
		}
		if !strings.Contains(got, "/app?") {
			t.Fatalf("dbname lost: %q", got)
		}
	})
	t.Run("mysql-untouched", func(t *testing.T) {
		dsn := "u:p@tcp(db:3306)/app?tls=false"
		if got := appDSN("mysql", dsn, "app", "s3cret"); got != dsn {
			t.Fatalf("got %q", got)
		}
	})
}
