package users

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// User is an account. AuthVersion revokes tokens on privilege change:
// JWTs carry aver and RequireAuth rejects mismatches, so bump it whenever
// roles change (see BumpAuthVersion). Email uniqueness is composite
// (tenant_id,email) so the same address can own accounts in different
// tenants; tenant_id is filled by GORM scoping from request context
// (see pkg/tenancy).
type User struct {
	ID           uuid.UUID `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID     uuid.UUID `gorm:"type:char(36);uniqueIndex:uq_users_tenant_email;index;not null" json:"tenant_id"`
	Name         string    `gorm:"size:255;not null" json:"name"`
	Email        string    `gorm:"size:255;uniqueIndex:uq_users_tenant_email;not null" json:"email"`
	PasswordHash string    `gorm:"size:255;not null" json:"-"`
	Role         string    `gorm:"size:32;not null;default:member" json:"role"`
	IsActive     bool      `gorm:"default:true" json:"is_active"`
	AuthVersion  int       `gorm:"not null;default:1" json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (User) TableName() string { return "users" }

func (u User) GetTenantID() uuid.UUID { return u.TenantID }

func (u *User) BeforeCreate(_ *gorm.DB) error {
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	return nil
}
