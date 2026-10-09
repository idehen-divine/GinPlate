package users

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/idehen-divine/GinPlate/internal/middleware"
	"github.com/idehen-divine/GinPlate/pkg/web"
	"gorm.io/gorm"
)

// UserService is the behavior boundary handlers depend on: the smallest
// useful interface over the user domain.
type UserService interface {
	List(ctx context.Context, db *gorm.DB, filter web.ListFilter) (web.ListResult[User], error)
}

type Service struct{ repository Repository }

// NewService wires a Repository; nil selects the GORM implementation.
func NewService(repository Repository) *Service {
	if repository == nil {
		repository = NewGormRepository()
	}
	return &Service{repository: repository}
}

func (s *Service) List(ctx context.Context, db *gorm.DB, filter web.ListFilter) (web.ListResult[User], error) {
	rows, total, err := s.repository.List(ctx, db, filter)
	if err != nil {
		return web.ListResult[User]{}, web.Wrap(http.StatusInternalServerError, "Could not list users.", err)
	}
	return web.PagedResult(rows, total, filter), nil
}

// BumpAuthVersion increments the user's auth version, revoking all
// outstanding tokens (their aver no longer matches). Call it whenever
// roles change.
func (s *Service) BumpAuthVersion(ctx context.Context, db *gorm.DB, id uuid.UUID) error {
	if err := s.repository.IncrementAuthVersion(ctx, db, id); err != nil {
		return web.Wrap(http.StatusInternalServerError, "Could not revoke sessions.", err)
	}
	return nil
}

// SetRole changes a user's role and bumps auth_version atomically, so
// outstanding tokens die with the old privilege. This is the only
// supported path for role changes.
func (s *Service) SetRole(ctx context.Context, db *gorm.DB, id uuid.UUID, role string) error {
	if role != string(middleware.RoleAdmin) && role != string(middleware.RoleMember) {
		return web.BadRequest("Unknown role.")
	}
	if err := s.repository.SetRole(ctx, db, id, role); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return web.Wrap(http.StatusInternalServerError, "Could not change role.", err)
	}
	return nil
}

// SetActive flips a user's active flag and bumps auth_version atomically.
// This is the only supported path for activation changes.
func (s *Service) SetActive(ctx context.Context, db *gorm.DB, id uuid.UUID, active bool) error {
	if err := s.repository.SetActive(ctx, db, id, active); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return web.Wrap(http.StatusInternalServerError, "Could not change status.", err)
	}
	return nil
}
