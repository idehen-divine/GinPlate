// Package tenancy implements elastic multi-tenancy: tenants share pool
// databases by default (row isolation via tenant_id) and individual
// enterprise tenants can move to dedicated databases (placement routing +
// the Migrator). Tenant identity travels in ctx (TenantFromContext) and on
// gin requests (CurrentTenant); data access goes through tenant-scoped
// *gorm.DB handles so services never filter by hand.
package tenancy

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Placement values for tenants.
const (
	PlacementShared    = "shared"
	PlacementDedicated = "dedicated"
)

// Status values for tenants. Migrating tenants serve reads but reject
// writes (see BlockWritesWhenMigrating); suspended tenants are blocked.
const (
	StatusActive    = "active"
	StatusSuspended = "suspended"
	StatusMigrating = "migrating"
)

// Tenant is the request-scoped tenant identity.
type Tenant struct {
	ID        uuid.UUID
	Slug      string
	Name      string
	Placement string
	Pool      string
	Status    string
}

type ctxKey string

const (
	tenantKey     ctxKey = "tenant"
	tenantIDKey   ctxKey = "tenant_id"
	tenantSlugKey ctxKey = "tenant_slug"
	noScopeKey    ctxKey = "no_tenant_scope"
)

// WithTenant stores the tenant identity in ctx for non-HTTP call chains.
func WithTenant(ctx context.Context, t *Tenant) context.Context {
	if t == nil {
		return ctx
	}
	ctx = context.WithValue(ctx, tenantKey, t)
	ctx = context.WithValue(ctx, tenantIDKey, t.ID)
	ctx = context.WithValue(ctx, tenantSlugKey, t.Slug)
	return ctx
}

// WithTenantID stores only the tenant id (data-plane scoping without the
// full record).
func WithTenantID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, tenantIDKey, id)
}

// WithTenantSlug stores only the tenant slug (token, cache, and queue
// binding without the full record).
func WithTenantSlug(ctx context.Context, slug string) context.Context {
	return context.WithValue(ctx, tenantSlugKey, slug)
}

// TenantFromContext extracts the tenant stored by WithTenant.
func TenantFromContext(ctx context.Context) (*Tenant, bool) {
	t, ok := ctx.Value(tenantKey).(*Tenant)
	return t, ok && t != nil
}

// TenantIDFrom extracts the tenant id for query scoping.
func TenantIDFrom(ctx context.Context) (uuid.UUID, bool) {
	if ctx == nil {
		return uuid.Nil, false
	}
	id, ok := ctx.Value(tenantIDKey).(uuid.UUID)
	return id, ok && id != uuid.Nil
}

// TenantSlugFrom extracts the tenant slug for token/cache/queue binding.
func TenantSlugFrom(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	slug, ok := ctx.Value(tenantSlugKey).(string)
	return slug, ok && slug != ""
}

// WithoutTenantScope returns a ctx on which GORM tenant callbacks stay
// silent: control-plane, migration, and system operations use it so shared
// infrastructure tables are never tenant-filtered.
func WithoutTenantScope(ctx context.Context) context.Context {
	return context.WithValue(ctx, noScopeKey, true)
}

// scopeSkipped reports whether callbacks must stay silent on ctx.
func scopeSkipped(ctx context.Context) bool {
	if ctx == nil {
		return true
	}
	skip, _ := ctx.Value(noScopeKey).(bool)
	return skip
}

// CurrentTenant returns the tenant resolved by ResolveTenant for this
// request, or nil when the middleware did not run (health checks, admin
// surface before override).
func CurrentTenant(c *gin.Context) *Tenant {
	if v, ok := c.Get("tenant"); ok {
		if t, ok := v.(*Tenant); ok {
			return t
		}
	}
	return nil
}

// CurrentTenantSlug is the slug shortcut for token and cache binding.
func CurrentTenantSlug(c *gin.Context) string {
	if t := CurrentTenant(c); t != nil {
		return t.Slug
	}
	if slug, _ := c.Get("tenantSlug"); slug != nil {
		if s, ok := slug.(string); ok {
			return s
		}
	}
	return ""
}

// normalizeSlug trims and lowercases a candidate slug.
func normalizeSlug(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
