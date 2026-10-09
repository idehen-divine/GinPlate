package tenantdomains

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Domain maps a customer-owned domain to a tenant for header-based
// resolution (X-Tenant-Domain). Only verified rows resolve: verification
// is flipped by control admins after out-of-band ownership proof (DNS),
// never by the domain claimant.
type Domain struct {
	ID        uuid.UUID `gorm:"type:char(36);primaryKey"`
	TenantID  uuid.UUID `gorm:"type:char(36);index;not null"`
	Domain    string    `gorm:"not null"`
	Verified  bool      `gorm:"not null;default:false"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TableName pins the tenant domains table.
func (Domain) TableName() string { return "tenant_domains" }

// BeforeCreate assigns a UUID primary key when the caller didn't set one.
func (d *Domain) BeforeCreate(_ *gorm.DB) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	return nil
}
