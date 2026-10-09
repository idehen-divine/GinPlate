package auth

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/idehen-divine/GinPlate/pkg/resettoken"
)

// Reset token audiences sharing the password_reset_tokens table,
// discriminated by the kind column (composite PK email+kind).
const (
	ResetKindUser         = "user"
	ResetKindControlAdmin = "control_admin"
)

// ResetTokenTTL is the password-reset link lifetime.
const ResetTokenTTL = time.Hour

// ErrResetInvalid is returned for unknown, mismatched, or already-used
// reset tokens. Callers map it to a generic failure (no oracle).
var ErrResetInvalid = errors.New("invalid reset token")

// ErrResetExpired is returned for well-formed but expired reset tokens.
var ErrResetExpired = errors.New("reset token expired")

// ResetToken maps the polymorphic password_reset_tokens table.
type ResetToken struct {
	Email     string    `gorm:"primaryKey;size:255"`
	Kind      string    `gorm:"primaryKey;size:16"`
	TokenHash string    `gorm:"size:64;uniqueIndex;not null"`
	ExpiresAt time.Time `gorm:"not null"`
	UsedAt    *time.Time
	CreatedAt time.Time
}

// TableName pins the reset tokens table.
func (ResetToken) TableName() string { return "password_reset_tokens" }

// IssueResetToken mints a raw token, stores its hash for (email, kind)
// (replacing any outstanding row), and returns the raw secret for emailing.
// The raw secret never touches the database.
func IssueResetToken(ctx context.Context, db *gorm.DB, email, kind string, ttl time.Duration) (string, error) {
	raw, err := resettoken.Mint()
	if err != nil {
		return "", err
	}
	now := time.Now()
	row := ResetToken{
		Email: email, Kind: kind,
		TokenHash: resettoken.Hash(raw),
		ExpiresAt: now.Add(ttl),
		CreatedAt: now,
	}
	if err := db.WithContext(ctx).Save(&row).Error; err != nil {
		return "", err
	}
	return raw, nil
}

// ConsumeResetToken validates a presented raw token against the stored hash
// for (email, kind) and deletes the row, enforcing single use. Hash-first
// lookup keeps unknown emails and bad tokens indistinguishable.
func ConsumeResetToken(ctx context.Context, db *gorm.DB, email, kind, raw string) error {
	hash := resettoken.Hash(raw)
	var row ResetToken
	if err := db.WithContext(ctx).
		Where("email = ? AND kind = ? AND token_hash = ?", email, kind, hash).
		First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrResetInvalid
		}
		return err
	}
	if !time.Now().Before(row.ExpiresAt) {
		_ = db.WithContext(ctx).Where("email = ? AND kind = ?", email, kind).Delete(&ResetToken{}).Error
		return ErrResetExpired
	}
	if err := db.WithContext(ctx).Where("email = ? AND kind = ?", email, kind).Delete(&ResetToken{}).Error; err != nil {
		return err
	}
	return nil
}
