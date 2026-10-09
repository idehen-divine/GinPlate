package cadmin

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ControlAdmin is a control-plane administrator: it manages tenants and
// never exists inside tenant data. Authentication uses a separate JWT
// secret and token type so tenant tokens can never reach admin routes.
type ControlAdmin struct {
	ID           uuid.UUID `gorm:"type:char(36);primaryKey" json:"id"`
	Name         string    `gorm:"not null" json:"name"`
	Email        string    `gorm:"uniqueIndex;not null" json:"email"`
	PasswordHash string    `gorm:"not null" json:"-"`
	Role         string    `gorm:"not null;default:super_admin" json:"role"`
	IsActive     bool      `gorm:"default:true" json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TableName pins the control admins table.
func (ControlAdmin) TableName() string { return "control_admins" }

// BeforeCreate assigns a UUID primary key when the caller didn't set one.
func (a *ControlAdmin) BeforeCreate(_ *gorm.DB) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	return nil
}
