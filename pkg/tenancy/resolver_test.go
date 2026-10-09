package tenancy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// stubBackend is a hermetic TenantBackend: slugs and domains map to
// records, missing keys report record-not-found, and "boom" simulates a
// backend outage. DBForTenant hands out an offline (DryRun) handle except
// for the nodata tenant, which simulates a pool outage.
type stubBackend struct {
	bySlug   map[string]*TenantRecord
	byDomain map[string]*TenantRecord
	db       *gorm.DB
}

func testRecord(slug string) *TenantRecord {
	return &TenantRecord{
		ID: uuid.New(), Slug: slug, Name: slug,
		Placement: PlacementShared, Pool: "shared_1", Status: StatusActive,
	}
}

func (s *stubBackend) TenantBySlug(_ context.Context, slug string) (*TenantRecord, error) {
	if slug == "boom" {
		return nil, errors.New("control database down")
	}
	if r, ok := s.bySlug[slug]; ok {
		return r, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (s *stubBackend) TenantByDomain(_ context.Context, domain string) (*TenantRecord, error) {
	if domain == "boom.example.com" {
		return nil, errors.New("control database down")
	}
	if r, ok := s.byDomain[domain]; ok {
		return r, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (s *stubBackend) DBForTenant(_ context.Context, rec *TenantRecord) (*gorm.DB, error) {
	if rec.Slug == "nodata" {
		return nil, errors.New("pool down")
	}
	return s.db, nil
}

func (s *stubBackend) DriverName() string { return "mysql" }

func resolveStatus(t *testing.T, h gin.HandlerFunc, method, target, slug, domain string) int {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	if slug != "" {
		c.Request.Header.Set(TenantHeader, slug)
	}
	if domain != "" {
		c.Request.Header.Set(TenantDomainHeader, domain)
	}
	h(c)
	return w.Code
}

func tenantOf(t *testing.T, h gin.HandlerFunc, slug, domain string) *Tenant {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	if slug != "" {
		c.Request.Header.Set(TenantHeader, slug)
	}
	if domain != "" {
		c.Request.Header.Set(TenantDomainHeader, domain)
	}
	var got *Tenant
	r := gin.New()
	r.Use(h)
	r.GET("/api/v1/users", func(c *gin.Context) {
		got = CurrentTenant(c)
		c.Status(http.StatusOK)
	})
	r.ServeHTTP(w, c.Request)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	return got
}

// TestResolver is the identification matrix: every signal combination and
// tenant state resolves to exactly one status, with no database.
func TestResolver(t *testing.T) {
	acme := testRecord("acme")
	globex := testRecord("globex")
	suspended := testRecord("frozen")
	suspended.Status = StatusSuspended
	migrating := testRecord("moving")
	migrating.Status = StatusMigrating
	nodata := testRecord("nodata")

	backend := &stubBackend{
		bySlug: map[string]*TenantRecord{
			"acme": acme, "frozen": suspended, "moving": migrating, "nodata": nodata,
		},
		byDomain: map[string]*TenantRecord{
			"app.acmecorp.com":   acme,
			"portal.globex.io":   globex,
			"frozen.example.com": suspended,
		},
		db: openDry(t),
	}
	h := ResolveTenant(backend)

	cases := []struct {
		name   string
		method string
		slug   string
		domain string
		want   int
	}{
		{"slug resolves", http.MethodGet, "acme", "", http.StatusOK},
		{"domain resolves", http.MethodGet, "", "app.acmecorp.com", http.StatusOK},
		{"both agreeing pass", http.MethodGet, "acme", "app.acmecorp.com", http.StatusOK},
		{"both conflicting 400", http.MethodGet, "acme", "portal.globex.io", http.StatusBadRequest},
		{"neither 400", http.MethodGet, "", "", http.StatusBadRequest},
		{"bad slug 400", http.MethodGet, "../evil", "", http.StatusBadRequest},
		{"bad domain scheme 400", http.MethodGet, "", "https://app.acmecorp.com", http.StatusBadRequest},
		{"bad domain port 400", http.MethodGet, "", "app.acmecorp.com:8080", http.StatusBadRequest},
		{"unknown slug 404", http.MethodGet, "ghost", "", http.StatusNotFound},
		{"unknown domain 404", http.MethodGet, "", "ghost.example.com", http.StatusNotFound},
		{"control outage 503", http.MethodGet, "boom", "", http.StatusServiceUnavailable},
		{"domain outage 503", http.MethodGet, "", "boom.example.com", http.StatusServiceUnavailable},
		{"suspended 403", http.MethodGet, "frozen", "", http.StatusForbidden},
		{"suspended via domain 403", http.MethodGet, "", "frozen.example.com", http.StatusForbidden},
		{"pool outage 503", http.MethodGet, "nodata", "", http.StatusServiceUnavailable},
		{"migrating read passes", http.MethodGet, "moving", "", http.StatusOK},
		{"write method still resolves", http.MethodPost, "acme", "", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveStatus(t, h, tc.method, "/api/v1/users", tc.slug, tc.domain); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}

	t.Run("canonical slug from domain", func(t *testing.T) {
		got := tenantOf(t, h, "", "app.acmecorp.com")
		if got == nil || got.Slug != "acme" || got.ID != acme.ID {
			t.Fatalf("tenant = %+v", got)
		}
	})

	t.Run("admin path skips resolution", func(t *testing.T) {
		if got := resolveStatus(t, h, http.MethodGet, "/api/v1/admin/tenants", "", ""); got == http.StatusBadRequest {
			t.Fatal("admin path should skip tenant resolution")
		}
	})
}
