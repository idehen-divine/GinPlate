package tenantmigrations

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Migration phases for shared-to-dedicated moves.
const (
	PhaseProvision = "provision"
	PhaseCopy      = "copy"
	PhaseVerify    = "verify"
	PhaseCutover   = "cutover"
	PhaseRetaining = "retaining"
	PhaseDone      = "done"
	PhaseFailed    = "failed"
)

// Migration is one shared-to-dedicated move ledger row: resumable,
// idempotent, and auditable after the fact.
type Migration struct {
	ID        uuid.UUID `gorm:"type:char(36);primaryKey"`
	TenantID  uuid.UUID `gorm:"type:char(36);index;not null"`
	Phase     string    `gorm:"not null;default:provision"`
	Reason    string    `gorm:"type:text"`
	Watermark *time.Time
	Checksums string `gorm:"type:text"`
	Progress  string `gorm:"type:text"`
	Attempts  int    `gorm:"not null;default:0"`
	Error     string `gorm:"type:text"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TableName pins the migration ledger table.
func (Migration) TableName() string { return "tenant_migrations" }

// BeforeCreate assigns a UUID primary key when the caller didn't set one.
func (m *Migration) BeforeCreate(_ *gorm.DB) error {
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	return nil
}

// Terminal reports whether the migration reached an end state.
func (m Migration) Terminal() bool {
	return m.Phase == PhaseDone || m.Phase == PhaseFailed
}
