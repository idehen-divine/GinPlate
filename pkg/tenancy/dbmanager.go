package tenancy

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	glogger "gorm.io/gorm/logger"

	"github.com/idehen-divine/GinPlate/pkg/database"
)

// TenantRecord maps the control-plane tenants table.
type TenantRecord struct {
	ID        uuid.UUID `gorm:"type:char(36);primaryKey"`
	Slug      string    `gorm:"not null"`
	Name      string    `gorm:"not null"`
	Placement string    `gorm:"not null;default:shared"`
	Pool      string    `gorm:"not null;default:shared_1"`
	Status    string    `gorm:"not null;default:active"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TableName pins the control tenants table.
func (TenantRecord) TableName() string { return "tenants" }

// Resolvable reports whether the tenant may serve traffic: active always,
// migrating readably (writes shed by BlockWritesWhenMigrating), suspended
// never.
func (r *TenantRecord) Resolvable() bool {
	return r.Status == StatusActive || r.Status == StatusMigrating
}

// ManagerParams wires a DBManager without a config dependency.
type ManagerParams struct {
	Driver      string
	ControlDSN  string
	Pools       map[string]string
	DefaultPool string
	DSNTemplate string
	AppUser     string
	AppPass     string
	Log         glogger.Interface
}

// DBManager routes tenants to databases: one control handle plus cached
// shared-pool and dedicated handles, keyed by DSN so identical DSNs share
// one pool. Handles opened for tenant data get RegisterTenantScopes;
// control handles never do.
type DBManager struct {
	mu          sync.RWMutex
	driver      string
	control     *gorm.DB
	controlDSN  string
	pools       map[string]*gorm.DB
	dedicated   map[string]*gorm.DB
	poolDSNs    map[string]string
	defaultPool string
	dsnTemplate string
	appUser     string
	appPass     string
	log         glogger.Interface
}

// NewDBManager opens the control database eagerly so boot fails fast when
// it is unreachable. An empty controlDSN falls back to primaryDSN (the
// single-database development topology).
func NewDBManager(p ManagerParams, primaryDSN string) (*DBManager, error) {
	controlDSN := strings.TrimSpace(p.ControlDSN)
	if controlDSN == "" {
		controlDSN = primaryDSN
	}
	m := &DBManager{
		driver:      database.NormalizeDriver(p.Driver),
		controlDSN:  controlDSN,
		pools:       map[string]*gorm.DB{},
		dedicated:   map[string]*gorm.DB{},
		poolDSNs:    p.Pools,
		defaultPool: p.DefaultPool,
		dsnTemplate: p.DSNTemplate,
		appUser:     p.AppUser,
		appPass:     p.AppPass,
		log:         p.Log,
	}
	if m.defaultPool == "" {
		m.defaultPool = "shared_1"
	}
	db, err := database.Connect(m.driver, controlDSN, logOrSilent(p.Log))
	if err != nil {
		return nil, fmt.Errorf("tenancy: control database: %w", err)
	}
	m.control = db
	return m, nil
}

func logOrSilent(log glogger.Interface) glogger.Interface {
	if log == nil {
		return glogger.Default.LogMode(glogger.Silent)
	}
	return log
}

// Control returns the control-plane handle (tenant records, ledger, admins).
func (m *DBManager) Control() *gorm.DB { return m.control }

// DriverName is the normalized engine name.
func (m *DBManager) DriverName() string { return m.driver }

// DSNTemplate returns the configured dedicated DSN template (possibly empty).
func (m *DBManager) DSNTemplate() string { return m.dsnTemplate }

// PoolDSNFor resolves a pool name to its DSN, falling back to the control
// DSN for the default pool in single-database topologies.
func (m *DBManager) PoolDSNFor(name string) (string, error) {
	if dsn, ok := m.poolDSNs[name]; ok && strings.TrimSpace(dsn) != "" {
		return dsn, nil
	}
	if name == m.defaultPool {
		return m.controlDSN, nil
	}
	return "", fmt.Errorf("tenancy: unknown pool %q", name)
}

// TenantBySlug loads a tenant record from the control database.
func (m *DBManager) TenantBySlug(ctx context.Context, slug string) (*TenantRecord, error) {
	var rec TenantRecord
	if err := m.control.WithContext(WithoutTenantScope(ctx)).Where("slug = ?", slug).First(&rec).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

// TenantByDomain resolves a tenant through a verified custom-domain
// mapping. Unverified or unmapped domains behave exactly like unknown
// slugs (not found) so claim state can't be probed.
func (m *DBManager) TenantByDomain(ctx context.Context, domain string) (*TenantRecord, error) {
	var rec TenantRecord
	if err := m.control.WithContext(WithoutTenantScope(ctx)).
		Select("tenants.*").
		Joins("JOIN tenant_domains ON tenant_domains.tenant_id = tenants.id").
		Where("tenant_domains.domain = ? AND tenant_domains.verified = ?", domain, true).
		First(&rec).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

// TenantTarget resolves where a tenant's data lives: a shared pool name or
// a dedicated DSN. Pure and unit-testable.
func TenantTarget(placement, pool, slug, defaultPool, dsnTemplate string) (kind, ref string, err error) {
	switch placement {
	case "", PlacementShared:
		name := strings.TrimSpace(pool)
		if name == "" {
			name = defaultPool
		}
		return "pool", name, nil
	case PlacementDedicated:
		if strings.TrimSpace(dsnTemplate) == "" {
			return "", "", fmt.Errorf("tenancy: dedicated placement needs TENANT_DSN_TEMPLATE")
		}
		if !slugRe.MatchString(slug) {
			return "", "", fmt.Errorf("tenancy: invalid tenant slug %q", slug)
		}
		return "dedicated", fmt.Sprintf(dsnTemplate, slug), nil
	default:
		return "", "", fmt.Errorf("tenancy: unknown placement %q", placement)
	}
}

// DBForTenant returns the tenant's database handle, opening and caching it
// on first use.
func (m *DBManager) DBForTenant(ctx context.Context, rec *TenantRecord) (*gorm.DB, error) {
	kind, ref, err := TenantTarget(rec.Placement, rec.Pool, rec.Slug, m.defaultPool, m.dsnTemplate)
	if err != nil {
		return nil, err
	}
	switch kind {
	case "pool":
		return m.PoolDB(ref)
	default:
		return m.DedicatedDB(rec.Slug, ref)
	}
}

// PoolDB returns the cached shared-pool handle, opening it on first use.
// An empty pool map (or unknown name) falls back to the primary DSN, which
// the default pool entry carries in single-database topologies.
func (m *DBManager) PoolDB(name string) (*gorm.DB, error) {
	dsn, ok := m.poolDSNs[name]
	if !ok || strings.TrimSpace(dsn) == "" {
		if name != m.defaultPool {
			return nil, fmt.Errorf("tenancy: unknown pool %q", name)
		}
		dsn = m.controlDSN
	}
	return m.cached(m.pools, "pool:"+name, dsn)
}

// DedicatedDB returns the cached dedicated handle for a tenant slug.
func (m *DBManager) DedicatedDB(slug, dsn string) (*gorm.DB, error) {
	return m.cached(m.dedicated, "tenant:"+slug, dsn)
}

func (m *DBManager) cached(cache map[string]*gorm.DB, key, dsn string) (*gorm.DB, error) {
	m.mu.RLock()
	if db, ok := cache[key]; ok {
		m.mu.RUnlock()
		return db, nil
	}
	m.mu.RUnlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if db, ok := cache[key]; ok {
		return db, nil
	}
	db, err := database.Connect(m.driver, appDSN(m.driver, dsn, m.appUser, m.appPass), m.log)
	if err != nil {
		return nil, err
	}
	RegisterTenantScopes(db)
	cache[key] = db
	return db, nil
}

// Forget drops a cached dedicated handle (used after cutover/rollback so
// the next resolution re-resolves placement).
func (m *DBManager) Forget(slug string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if db, ok := m.dedicated["tenant:"+slug]; ok {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		delete(m.dedicated, "tenant:"+slug)
	}
}

// Close shuts every managed handle including control.
func (m *DBManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var first error
	closeOne := func(db *gorm.DB) {
		if db == nil {
			return
		}
		if sqlDB, err := db.DB(); err == nil {
			if err := sqlDB.Close(); err != nil && first == nil {
				first = err
			}
		}
	}
	for _, db := range m.pools {
		closeOne(db)
	}
	for _, db := range m.dedicated {
		closeOne(db)
	}
	closeOne(m.control)
	return first
}

// appDSN swaps owner credentials for the limited application role on pgsql
// URL DSNs so RLS policies constrain the runtime. MySQL keeps owner
// credentials (no equivalent URL swap); empty appUser is a no-op for both.
func appDSN(driver, dsn, appUser, appPass string) string {
	if strings.TrimSpace(appUser) == "" {
		return dsn
	}
	if database.NormalizeDriver(driver) != "pgsql" {
		return dsn
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	u.User = url.UserPassword(appUser, appPass)
	return u.String()
}
