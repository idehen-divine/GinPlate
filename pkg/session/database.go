package session

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// sessionRow maps the sessions migration table. The id holds the access
// jti and refresh_jti the linked refresh jti; absolute expiries replace
// Redis TTLs with identical semantics.
type sessionRow struct {
	ID               string    `gorm:"primaryKey;size:36"`
	RefreshJTI       string    `gorm:"column:refresh_jti;size:36;uniqueIndex;not null"`
	UserID           string    `gorm:"column:user_id;size:36;index"`
	AccessExpiresAt  time.Time `gorm:"column:access_expires_at;not null"`
	RefreshExpiresAt time.Time `gorm:"column:refresh_expires_at;not null"`
}

func (sessionRow) TableName() string { return "sessions" }

// databaseStore is a Store backed by the sessions table. Every write also
// deletes already-expired rows, so the table stays small without a cron job.
type databaseStore struct {
	db  *gorm.DB
	now func() time.Time
}

// Database returns a DB-backed Store.
func Database(db *gorm.DB) Store {
	return &databaseStore{db: db, now: time.Now}
}

// Link inserts (or replaces) the session row with absolute expiries.
func (s *databaseStore) Link(ctx context.Context, accessJti, refreshJti, userID string, accessTTL, refreshTTL time.Duration) error {
	now := s.now()
	row := sessionRow{
		ID:               accessJti,
		RefreshJTI:       refreshJti,
		UserID:           userID,
		AccessExpiresAt:  now.Add(accessTTL),
		RefreshExpiresAt: now.Add(refreshTTL),
	}
	if err := s.db.WithContext(ctx).Save(&row).Error; err != nil {
		return err
	}
	return s.db.WithContext(ctx).
		Where("refresh_expires_at <= ?", now).
		Delete(&sessionRow{}).Error
}

// AccessValid returns the linked refresh jti for a live access half.
func (s *databaseStore) AccessValid(ctx context.Context, accessJti string) (string, bool) {
	if accessJti == "" {
		return "", false
	}
	var row sessionRow
	if err := s.db.WithContext(ctx).Where("id = ?", accessJti).First(&row).Error; err != nil {
		return "", false
	}
	if !s.now().Before(row.AccessExpiresAt) {
		_ = s.db.WithContext(ctx).Where("id = ?", accessJti).Delete(&sessionRow{}).Error
		return "", false
	}
	return row.RefreshJTI, true
}

// RefreshValid returns the linked access jti for a live refresh half.
func (s *databaseStore) RefreshValid(ctx context.Context, refreshJti string) (string, bool) {
	if refreshJti == "" {
		return "", false
	}
	var row sessionRow
	if err := s.db.WithContext(ctx).Where("refresh_jti = ?", refreshJti).First(&row).Error; err != nil {
		return "", false
	}
	if !s.now().Before(row.RefreshExpiresAt) {
		_ = s.db.WithContext(ctx).Where("refresh_jti = ?", refreshJti).Delete(&sessionRow{}).Error
		return "", false
	}
	return row.ID, true
}

// Unlink destroys the session row by either half. Missing rows are not errors.
func (s *databaseStore) Unlink(ctx context.Context, accessJti, refreshJti string) error {
	return s.db.WithContext(ctx).
		Where("id = ? OR refresh_jti = ?", accessJti, refreshJti).
		Delete(&sessionRow{}).Error
}
