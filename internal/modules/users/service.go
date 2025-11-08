package users

import (
	"context"
	"net/http"

	"github.com/idehen-divine/GinPlate/pkg/web"
	"gorm.io/gorm"
)

// Service is the business-logic layer over Repository (mockable in tests).
type Service struct{ repo Repository }

// NewService wires a Repository to the user Service. A nil repo selects the
// GORM implementation, so callers only pass one in tests.
func NewService(repo Repository) *Service {
	if repo == nil {
		repo = NewGormRepository()
	}
	return &Service{repo: repo}
}

// List returns users as a paged result, wrapping repository failures so
// handlers render them without classifying.
func (s *Service) List(ctx context.Context, db *gorm.DB, f web.ListFilter) (web.ListResult[User], error) {
	rows, total, err := s.repo.List(ctx, db, f)
	if err != nil {
		return web.ListResult[User]{}, web.Wrap(http.StatusInternalServerError, "Could not list users.", err)
	}
	return web.PagedResult(rows, total, f), nil
}
