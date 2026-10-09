package tenantmigrations

import (
	"context"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/idehen-divine/GinPlate/pkg/tenancy"
)

// TenantStore reads and writes tenant records for move execution. It is
// satisfied structurally by the parent tenants package's GORM
// implementation; declared locally so this package never imports its
// parent (which would cycle, since the parent composes this package).
type TenantStore interface {
	FindBySlug(ctx context.Context, db *gorm.DB, slug string) (*tenancy.TenantRecord, error)
	FindTenantByID(ctx context.Context, db *gorm.DB, id uuid.UUID) (*tenancy.TenantRecord, error)
	SaveTenant(ctx context.Context, db *gorm.DB, rec *tenancy.TenantRecord) error
}

// LedgerStore persists move ledger rows. Declared locally for the same
// cycle-free reason as TenantStore.
type LedgerStore interface {
	CreateMigration(ctx context.Context, db *gorm.DB, m *Migration) error
	SaveMigration(ctx context.Context, db *gorm.DB, m *Migration) error
	FindMigration(ctx context.Context, db *gorm.DB, id uuid.UUID) (*Migration, error)
	LatestMigration(ctx context.Context, db *gorm.DB, tenantID uuid.UUID) (*Migration, error)
}
