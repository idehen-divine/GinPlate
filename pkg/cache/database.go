package cache

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// cacheRow maps the caches migration table. Expiry is absolute UTC;
// NULL expires_at means the row lives forever.
type cacheRow struct {
	Key       string     `gorm:"column:cache_key;primaryKey;size:255"`
	Value     []byte     `gorm:"type:text;not null"`
	ExpiresAt *time.Time `gorm:"index"`
}

func (cacheRow) TableName() string { return "caches" }

// databaseStore is a Store backed by the caches table.
type databaseStore struct {
	db     *gorm.DB
	prefix string
	now    func() time.Time
}

// NewDatabase returns a DB-backed Store namespaced by prefix.
func NewDatabase(db *gorm.DB, prefix string) Store {
	return &databaseStore{db: db, prefix: prefix, now: time.Now}
}

// Get returns the value, treating expired rows as missing (and dropping them).
func (s *databaseStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	var row cacheRow
	err := s.db.WithContext(ctx).Where("cache_key = ?", s.prefix+key).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if row.ExpiresAt != nil && !s.now().Before(*row.ExpiresAt) {
		_ = s.db.WithContext(ctx).Where("cache_key = ?", s.prefix+key).Delete(&cacheRow{}).Error
		return nil, false, nil
	}
	return row.Value, true, nil
}

// Set upserts the value; ttl <= 0 means no expiry. Expired rows are swept
// opportunistically so the table stays small without a cron job.
func (s *databaseStore) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	var exp *time.Time
	if ttl > 0 {
		t := s.now().Add(ttl).UTC()
		exp = &t
	}
	full := s.prefix + key
	_ = s.db.WithContext(ctx).
		Where("expires_at IS NOT NULL AND expires_at <= ?", s.now().UTC()).
		Delete(&cacheRow{}).Error
	return s.db.WithContext(ctx).Save(&cacheRow{Key: full, Value: val, ExpiresAt: exp}).Error
}

// Delete removes keys; missing keys are not an error.
func (s *databaseStore) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	full := make([]string, len(keys))
	for i, k := range keys {
		full[i] = s.prefix + k
	}
	return s.db.WithContext(ctx).Where("cache_key IN ?", full).Delete(&cacheRow{}).Error
}

// Exists reports whether key is present and unexpired.
func (s *databaseStore) Exists(ctx context.Context, key string) (bool, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&cacheRow{}).
		Where("cache_key = ? AND (expires_at IS NULL OR expires_at > ?)", s.prefix+key, s.now().UTC()).
		Count(&n).Error
	if err != nil {
		return false, err
	}
	return n == 1, nil
}
