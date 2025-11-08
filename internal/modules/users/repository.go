package users

import (
	"context"

	"github.com/idehen-divine/GinPlate/pkg/web"
	"gorm.io/gorm"
)

// Repository abstracts user reads.
// The gorm implementation is used at runtime; tests supply a stub.
type Repository interface {
	List(ctx context.Context, db *gorm.DB, f web.ListFilter) ([]User, int64, error)
}

// GormRepository reads User rows through the request handle.
type GormRepository struct{}

// NewGormRepository returns the database-backed Repository.
func NewGormRepository() *GormRepository { return &GormRepository{} }

var userSorts = map[string]string{
	"id":         "id",
	"name":       "name",
	"email":      "email",
	"created_at": "created_at",
}

// List counts all matching users, then returns one filter page. Search
// matches name or email substrings; sort columns are allow-listed.
func (GormRepository) List(ctx context.Context, db *gorm.DB, f web.ListFilter) ([]User, int64, error) {
	q := db.WithContext(ctx).Model(&User{})
	if f.Search != "" {
		like := "%" + f.Search + "%"
		q = q.Where("name LIKE ? OR email LIKE ?", like, like)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []User
	if err := web.ApplyPaging(q, f, userSorts).Find(&out).Error; err != nil {
		return nil, 0, err
	}
	return out, total, nil
}
