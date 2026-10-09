package users

import (
	"context"

	"github.com/google/uuid"
	"github.com/idehen-divine/GinPlate/pkg/web"
	"gorm.io/gorm"
)

type Repository interface {
	List(ctx context.Context, db *gorm.DB, filter web.ListFilter) ([]User, int64, error)
	IncrementAuthVersion(ctx context.Context, db *gorm.DB, id uuid.UUID) error
	// SetRole and SetActive change privilege-bearing fields and bump
	// auth_version in the same statement, so outstanding tokens die with
	// the change. Missing rows report gorm.ErrRecordNotFound.
	SetRole(ctx context.Context, db *gorm.DB, id uuid.UUID, role string) error
	SetActive(ctx context.Context, db *gorm.DB, id uuid.UUID, active bool) error
}

type GormRepository struct{}

func NewGormRepository() *GormRepository { return &GormRepository{} }

var userSorts = map[string]string{
	"id":         "id",
	"name":       "name",
	"email":      "email",
	"created_at": "created_at",
}

// List returns one filter page (search matches name/email; sorts allow-listed).
func (GormRepository) List(ctx context.Context, db *gorm.DB, filter web.ListFilter) ([]User, int64, error) {
	q := db.WithContext(ctx).Model(&User{})
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		q = q.Where("name LIKE ? OR email LIKE ?", like, like)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []User
	if err := web.ApplyPaging(q, filter, userSorts).Find(&out).Error; err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// IncrementAuthVersion bumps the user's auth version, revoking tokens
// minted before the bump.
func (GormRepository) IncrementAuthVersion(ctx context.Context, db *gorm.DB, id uuid.UUID) error {
	return db.WithContext(ctx).Model(&User{}).Where("id = ?", id).
		UpdateColumn("auth_version", gorm.Expr("auth_version + ?", 1)).Error
}

func (GormRepository) SetRole(ctx context.Context, db *gorm.DB, id uuid.UUID, role string) error {
	res := db.WithContext(ctx).Model(&User{}).Where("id = ?", id).
		Updates(map[string]any{"role": role, "auth_version": gorm.Expr("auth_version + ?", 1)})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (GormRepository) SetActive(ctx context.Context, db *gorm.DB, id uuid.UUID, active bool) error {
	res := db.WithContext(ctx).Model(&User{}).Where("id = ?", id).
		Updates(map[string]any{"is_active": active, "auth_version": gorm.Expr("auth_version + ?", 1)})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
