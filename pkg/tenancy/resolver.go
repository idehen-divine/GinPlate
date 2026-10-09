package tenancy

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// TenantHeader carries an explicit tenant slug.
const TenantHeader = "X-Tenant-Slug"

// TenantDomainHeader carries a customer-owned domain for tenants resolved
// by custom domain instead of slug (corporate clients pointing their own
// domain at the platform, whose frontends never see the slug).
const TenantDomainHeader = "X-Tenant-Domain"

// domainRe constrains domains to bare hostnames (no scheme, path, or
// port): lowercase alnum/hyphen labels joined by dots.
var domainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// IsValidDomain reports whether s is a routable bare hostname.
func IsValidDomain(s string) bool {
	s = normalizeSlug(s)
	return s != "" && len(s) <= 253 && domainRe.MatchString(s)
}

// TenantResolver looks tenants up by either identity signal.
type TenantResolver interface {
	TenantBySlug(ctx context.Context, slug string) (*TenantRecord, error)
	TenantByDomain(ctx context.Context, domain string) (*TenantRecord, error)
}

// TenantBackend resolves tenant identity and databases. DBManager is the
// live implementation; tests stub the whole surface.
type TenantBackend interface {
	TenantResolver
	DBForTenant(ctx context.Context, rec *TenantRecord) (*gorm.DB, error)
	DriverName() string
}

// ResolveTenant pins the request's tenant and database from exactly one
// identity signal: X-Tenant-Slug (tenants.slug) or X-Tenant-Domain (a
// verified tenant_domains row). It sets the tenant, its canonical slug,
// and a tenant-scoped *gorm.DB on the gin context (keys "tenant",
// "tenantSlug", "db"). Both signals present but disagreeing is a 400;
// neither present is a 400 (identification is mandatory, never defaulted);
// unknown slugs and unknown-or-unverified domains share one 404 so claim
// state can't be probed; only record-not-found maps to 404 while backend
// failures 503. Suspended tenants 403, unreachable tenant databases 503.
// Mount on the tenant API group only — health checks and the control-plane
// admin surface resolve their own handles.
func ResolveTenant(backend TenantBackend) gin.HandlerFunc {
	return func(c *gin.Context) {
		// The control-plane admin surface resolves its own (control)
		// handle in cadmin middleware; tenant resolution must not couple
		// admin calls to tenant health.
		if strings.HasPrefix(c.Request.URL.Path, "/api/v1/admin/") {
			c.Next()
			return
		}
		slug := normalizeSlug(c.GetHeader(TenantHeader))
		domain := normalizeSlug(c.GetHeader(TenantDomainHeader))
		if slug != "" && !slugRe.MatchString(slug) {
			abortJSON(c, http.StatusBadRequest, "Invalid tenant slug.")
			return
		}
		if domain != "" && !IsValidDomain(domain) {
			abortJSON(c, http.StatusBadRequest, "Invalid tenant domain.")
			return
		}
		if slug == "" && domain == "" {
			abortJSON(c, http.StatusBadRequest, "Tenant identification required.")
			return
		}
		ctx := c.Request.Context()
		var rec, other *TenantRecord
		if slug != "" {
			r, err := lookupTenant(func() (*TenantRecord, error) {
				return backend.TenantBySlug(ctx, slug)
			})
			if err != nil {
				abortLookup(c, err)
				return
			}
			rec = r
		}
		if domain != "" {
			r, err := lookupTenant(func() (*TenantRecord, error) {
				return backend.TenantByDomain(ctx, domain)
			})
			if err != nil {
				abortLookup(c, err)
				return
			}
			other = r
		}
		if rec != nil && other != nil && rec.ID != other.ID {
			abortJSON(c, http.StatusBadRequest, "Conflicting tenant identification.")
			return
		}
		if rec == nil {
			rec = other
		}
		if rec.Status == StatusSuspended {
			abortJSON(c, http.StatusForbidden, "Tenant unavailable.")
			return
		}
		db, err := backend.DBForTenant(ctx, rec)
		if err != nil {
			abortJSON(c, http.StatusServiceUnavailable, "Tenant database unavailable.")
			return
		}
		scoped := ScopeToTenant(db, rec.ID, rec.Slug)
		c.Set("tenant", &Tenant{
			ID: rec.ID, Slug: rec.Slug, Name: rec.Name,
			Placement: rec.Placement, Pool: rec.Pool, Status: rec.Status,
		})
		c.Set("tenantSlug", rec.Slug)
		if backend.DriverName() == "pgsql" {
			// Defense in depth with the RLS policies: run the request in a
			// transaction holding SET LOCAL app.tenant_id, committed by
			// FinalizeTx. MySQL has no equivalent and relies on GORM
			// scoping alone.
			tx := scoped.Begin()
			if err := tx.Error; err != nil {
				abortJSON(c, http.StatusServiceUnavailable, "Tenant database unavailable.")
				return
			}
			if err := tx.Exec("SET LOCAL app.tenant_id = ?", rec.ID.String()).Error; err != nil {
				_ = tx.Rollback()
				abortJSON(c, http.StatusServiceUnavailable, "Tenant database unavailable.")
				return
			}
			c.Set("db", ScopeToTenant(tx, rec.ID, rec.Slug))
			c.Set("tenantTx", true)
		} else {
			c.Set("db", scoped)
		}
		c.Next()
	}
}

// errNotFound marks unknown-tenant lookups. Slugs and domains share it so
// claim state can't be probed: unmapped and unverified are identical.
var errNotFound = errors.New("unknown tenant")

// abortJSON writes the standard error envelope.
func abortJSON(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"success": false, "code": status,
		"message": message, "data": nil, "errors": nil,
	})
}

// lookupTenant resolves one identity signal. Record-not-found becomes the
// shared unknown-tenant marker (404 downstream); any other backend failure
// propagates for a 503.
func lookupTenant(fn func() (*TenantRecord, error)) (*TenantRecord, error) {
	rec, err := fn()
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNotFound
		}
		return nil, err
	}
	return rec, nil
}

// abortLookup maps lookup failures to responses.
func abortLookup(c *gin.Context, err error) {
	if errors.Is(err, errNotFound) {
		abortJSON(c, http.StatusNotFound, "Unknown tenant.")
		return
	}
	abortJSON(c, http.StatusServiceUnavailable, "Tenant database unavailable.")
}

// slugRe constrains slugs to DNS-safe lowercase tokens. Resolution rejects
// anything else with 400 before the value can reach DSN templates or queries.
var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// IsValidSlug reports whether s is a routable tenant slug.
func IsValidSlug(s string) bool { return slugRe.MatchString(normalizeSlug(s)) }

// ParsePoolDSNs parses "name=dsn,name=dsn" pool mappings. Malformed entries
// are skipped so one bad pair cannot break the whole map.
func ParsePoolDSNs(s string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		name, dsn, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(dsn) == "" {
			continue
		}
		out[strings.TrimSpace(name)] = strings.TrimSpace(dsn)
	}
	return out
}

// BlockWritesWhenMigrating sheds write load while a tenant migrates between
// placements: reads flow so the app stays usable, writes get 429 with a
// Retry-After so clients back off until cutover completes. Control-plane
// admin paths always pass through.
func BlockWritesWhenMigrating() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, "/api/v1/admin/") {
			c.Next()
			return
		}
		if t := CurrentTenant(c); t != nil && t.Status == StatusMigrating {
			c.Header("Retry-After", "120")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"success": false, "code": http.StatusTooManyRequests,
				"message": "Tenant is being upgraded, retry shortly.", "data": nil, "errors": nil,
			})
			return
		}
		c.Next()
	}
}

// FinalizeTx commits the per-request pgsql transaction opened by
// ResolveTenant, rolling back on handler errors or 5xx responses. It is a
// no-op for MySQL, health checks, and unresolved requests.
func FinalizeTx() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		v, ok := c.Get("tenantTx")
		if !ok {
			return
		}
		on, _ := v.(bool)
		if !on {
			return
		}
		vdb, ok := c.Get("db")
		if !ok {
			return
		}
		tx, ok := vdb.(*gorm.DB)
		if !ok {
			return
		}
		if len(c.Errors) > 0 || c.Writer.Status() >= 500 {
			_ = tx.Rollback()
			return
		}
		_ = tx.Commit()
	}
}
