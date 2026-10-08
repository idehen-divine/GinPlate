package session

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	if accessJti == "" || refreshJti == "" {
		return errEmptySessionID
	}
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

// ReplaceRefresh validates the old refresh half and records the
// replacement session in one transaction: row lock, old-row deletion
// (RowsAffected must be 1, else a concurrent rotation won), then new-row
// insertion. Any failure rolls the transaction back, so the old session
// survives storage errors and the user can retry instead of being stranded
// without a session.
func (s *databaseStore) ReplaceRefresh(ctx context.Context, oldRefreshJti, newAccessJti, newRefreshJti, userID string, accessTTL, refreshTTL time.Duration) (string, bool, error) {
	if oldRefreshJti == "" || newAccessJti == "" || newRefreshJti == "" {
		return "", false, errEmptySessionID
	}
	now := s.now()
	var oldAccessJti string
	var replaced bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row sessionRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("refresh_jti = ?", oldRefreshJti).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil // replay or unknown: ok=false, no error
			}
			return err
		}
		if !now.Before(row.RefreshExpiresAt) {
			res := tx.Where("refresh_jti = ?", oldRefreshJti).Delete(&sessionRow{})
			if res.Error != nil {
				return res.Error
			}
			return nil // expired: reaped, replay either way
		}
		res := tx.Where("refresh_jti = ?", oldRefreshJti).Delete(&sessionRow{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil // racer already consumed it: replay
		}
		next := sessionRow{
			ID:               newAccessJti,
			RefreshJTI:       newRefreshJti,
			UserID:           userID,
			AccessExpiresAt:  now.Add(accessTTL),
			RefreshExpiresAt: now.Add(refreshTTL),
		}
		if err := tx.Create(&next).Error; err != nil {
			return err // rolls back the deletion: old session survives
		}
		oldAccessJti = row.ID
		replaced = true
		return nil
	})
	if err != nil {
		return "", false, err
	}
	return oldAccessJti, replaced, nil
}

// ConsumeRefresh validates the refresh half and deletes the session row in
// one transaction, so concurrent refresh requests cannot both succeed. The
// row is locked with SELECT ... FOR UPDATE and the DELETE must affect
// exactly one row: RowsAffected == 0 means a concurrent consumer already
// deleted it, which is reported as replay (ok=false), not success.
// Expired rows are reaped and likewise reported as replay.
func (s *databaseStore) ConsumeRefresh(ctx context.Context, refreshJti string) (string, bool, error) {
	if refreshJti == "" {
		return "", false, nil
	}
	var accessJti string
	var consumed bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row sessionRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("refresh_jti = ?", refreshJti).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil // replay or unknown: ok=false, no error
			}
			return err
		}
		if !s.now().Before(row.RefreshExpiresAt) {
			res := tx.Where("refresh_jti = ?", refreshJti).Delete(&sessionRow{})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return nil // racer already reaped it: replay
			}
			return nil
		}
		res := tx.Where("refresh_jti = ?", refreshJti).Delete(&sessionRow{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil // racer already consumed it: replay
		}
		accessJti = row.ID
		consumed = true
		return nil
	})
	if err != nil {
		return "", false, err
	}
	return accessJti, consumed, nil
}

// Unlink destroys the session row by either half. Missing rows are not errors.
func (s *databaseStore) Unlink(ctx context.Context, accessJti, refreshJti string) error {
	return s.db.WithContext(ctx).
		Where("id = ? OR refresh_jti = ?", accessJti, refreshJti).
		Delete(&sessionRow{}).Error
}
