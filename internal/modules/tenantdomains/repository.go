package tenantdomains

import (
	"context"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// TenantDomainRepository persists custom-domain mappings. It is the
// domains unit's own seam; the shared GORM implementation lives with the
// parent tenants package and satisfies it structurally.
type TenantDomainRepository interface {
	CreateDomain(ctx context.Context, db *gorm.DB, d *Domain) error
	SaveDomain(ctx context.Context, db *gorm.DB, d *Domain) error
	DeleteDomain(ctx context.Context, db *gorm.DB, id uuid.UUID) error
	ListDomains(ctx context.Context, db *gorm.DB, tenantID uuid.UUID) ([]Domain, error)
	FindDomainByID(ctx context.Context, db *gorm.DB, id uuid.UUID) (*Domain, error)
}
