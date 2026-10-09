package tenants

import (
	"context"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/idehen-divine/GinPlate/internal/modules/tenantdomains"
	"github.com/idehen-divine/GinPlate/internal/modules/tenantmigrations"
	"github.com/idehen-divine/GinPlate/pkg/tenancy"
)

// TenantsRepository persists tenant records. It is one of two narrow seams
// in this package (with TenantMigrationsRepository; domains owns its own)
// so each service depends only on the rows it owns; GormRepository
// implements them all.
type TenantsRepository interface {
	CreateTenant(ctx context.Context, db *gorm.DB, rec *tenancy.TenantRecord) error
	SaveTenant(ctx context.Context, db *gorm.DB, rec *tenancy.TenantRecord) error
	FindBySlug(ctx context.Context, db *gorm.DB, slug string) (*tenancy.TenantRecord, error)
	FindTenantByID(ctx context.Context, db *gorm.DB, id uuid.UUID) (*tenancy.TenantRecord, error)
	ListTenants(ctx context.Context, db *gorm.DB, limit, offset int) ([]tenancy.TenantRecord, int64, error)
	DeleteTenant(ctx context.Context, db *gorm.DB, id uuid.UUID) error
}

// TenantMigrationsRepository persists move ledger rows.
type TenantMigrationsRepository interface {
	CreateMigration(ctx context.Context, db *gorm.DB, m *tenantmigrations.Migration) error
	SaveMigration(ctx context.Context, db *gorm.DB, m *tenantmigrations.Migration) error
	FindMigration(ctx context.Context, db *gorm.DB, id uuid.UUID) (*tenantmigrations.Migration, error)
	LatestMigration(ctx context.Context, db *gorm.DB, tenantID uuid.UUID) (*tenantmigrations.Migration, error)
}

// GormRepository is the GORM-backed implementation of this package's seams
// plus the domains seam (satisfied structurally; domains owns the
// interface).
type GormRepository struct{}

// Compile-time proof that one struct satisfies every seam it serves.
var (
	_ TenantsRepository                    = GormRepository{}
	_ TenantMigrationsRepository           = GormRepository{}
	_ tenantdomains.TenantDomainRepository = GormRepository{}
)

// NewGormRepository returns the live repository.
func NewGormRepository() *GormRepository { return &GormRepository{} }

// CreateTenant inserts a tenant record.
func (GormRepository) CreateTenant(ctx context.Context, db *gorm.DB, rec *tenancy.TenantRecord) error {
	return db.WithContext(ctx).Create(rec).Error
}

// SaveTenant persists tenant edits (placement, pool, status).
func (GormRepository) SaveTenant(ctx context.Context, db *gorm.DB, rec *tenancy.TenantRecord) error {
	return db.WithContext(ctx).Save(rec).Error
}

// FindBySlug loads a tenant by slug.
func (GormRepository) FindBySlug(ctx context.Context, db *gorm.DB, slug string) (*tenancy.TenantRecord, error) {
	var rec tenancy.TenantRecord
	if err := db.WithContext(ctx).Where("slug = ?", slug).First(&rec).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

// FindTenantByID loads a tenant by id.
func (GormRepository) FindTenantByID(ctx context.Context, db *gorm.DB, id uuid.UUID) (*tenancy.TenantRecord, error) {
	var rec tenancy.TenantRecord
	if err := db.WithContext(ctx).Where("id = ?", id.String()).First(&rec).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

// ListTenants pages tenants newest last (slug order is stable for admins).
func (GormRepository) ListTenants(ctx context.Context, db *gorm.DB, limit, offset int) ([]tenancy.TenantRecord, int64, error) {
	var rows []tenancy.TenantRecord
	var total int64
	if err := db.WithContext(ctx).Model(&tenancy.TenantRecord{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := db.WithContext(ctx).Order("slug ASC").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// DeleteTenant deletes a tenant record. FK cascades take its domains and
// ledger rows; pool data rows are purged beforehand by the caller.
func (GormRepository) DeleteTenant(ctx context.Context, db *gorm.DB, id uuid.UUID) error {
	return db.WithContext(ctx).Where("id = ?", id.String()).Delete(&tenancy.TenantRecord{}).Error
}

// CreateMigration inserts a migration ledger row.
func (GormRepository) CreateMigration(ctx context.Context, db *gorm.DB, m *tenantmigrations.Migration) error {
	return db.WithContext(ctx).Create(m).Error
}

// SaveMigration persists ledger progress.
func (GormRepository) SaveMigration(ctx context.Context, db *gorm.DB, m *tenantmigrations.Migration) error {
	return db.WithContext(ctx).Save(m).Error
}

// FindMigration loads a ledger row by id.
func (GormRepository) FindMigration(ctx context.Context, db *gorm.DB, id uuid.UUID) (*tenantmigrations.Migration, error) {
	var m tenantmigrations.Migration
	if err := db.WithContext(ctx).Where("id = ?", id.String()).First(&m).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// LatestMigration loads the newest ledger row for a tenant, or
// gorm.ErrRecordNotFound when none exists.
func (GormRepository) LatestMigration(ctx context.Context, db *gorm.DB, tenantID uuid.UUID) (*tenantmigrations.Migration, error) {
	var m tenantmigrations.Migration
	if err := db.WithContext(ctx).Where("tenant_id = ?", tenantID.String()).Order("created_at DESC").First(&m).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// CreateDomain inserts a custom-domain mapping (starts unverified).
func (GormRepository) CreateDomain(ctx context.Context, db *gorm.DB, d *tenantdomains.Domain) error {
	return db.WithContext(ctx).Create(d).Error
}

// SaveDomain persists domain edits (verification flips).
func (GormRepository) SaveDomain(ctx context.Context, db *gorm.DB, d *tenantdomains.Domain) error {
	return db.WithContext(ctx).Save(d).Error
}

// DeleteDomain removes a custom-domain mapping.
func (GormRepository) DeleteDomain(ctx context.Context, db *gorm.DB, id uuid.UUID) error {
	return db.WithContext(ctx).Where("id = ?", id.String()).Delete(&tenantdomains.Domain{}).Error
}

// ListDomains lists a tenant's custom-domain mappings.
func (GormRepository) ListDomains(ctx context.Context, db *gorm.DB, tenantID uuid.UUID) ([]tenantdomains.Domain, error) {
	var rows []tenantdomains.Domain
	if err := db.WithContext(ctx).Where("tenant_id = ?", tenantID.String()).Order("domain ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// FindDomainByID loads one custom-domain mapping.
func (GormRepository) FindDomainByID(ctx context.Context, db *gorm.DB, id uuid.UUID) (*tenantdomains.Domain, error) {
	var d tenantdomains.Domain
	if err := db.WithContext(ctx).Where("id = ?", id.String()).First(&d).Error; err != nil {
		return nil, err
	}
	return &d, nil
}
