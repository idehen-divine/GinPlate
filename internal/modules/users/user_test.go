package users

import (
	"context"
	"errors"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/idehen-divine/GinPlate/internal/middleware"
	"github.com/idehen-divine/GinPlate/pkg/web"
	"gorm.io/gorm"
)

type stubRepo struct {
	rows  []User
	total int64
	err   error
}

// List replays canned rows, total, and error without touching a database.
func (s stubRepo) List(_ context.Context, _ *gorm.DB, _ web.ListFilter) ([]User, int64, error) {
	return s.rows, s.total, s.err
}

// IncrementAuthVersion is a no-op stub satisfying the Repository seam.
func (s stubRepo) IncrementAuthVersion(_ context.Context, _ *gorm.DB, _ uuid.UUID) error {
	return s.err
}

// SetRole is a no-op stub satisfying the Repository seam.
func (s stubRepo) SetRole(_ context.Context, _ *gorm.DB, _ uuid.UUID, _ string) error {
	return s.err
}

// SetActive is a no-op stub satisfying the Repository seam.
func (s stubRepo) SetActive(_ context.Context, _ *gorm.DB, _ uuid.UUID, _ bool) error {
	return s.err
}

// TestUsers is the single entry point for every users service test: paged
// passthrough, error propagation, and role-gated resource shaping.
func TestUsers(t *testing.T) {
	t.Run("list-returns-paged-result", func(t *testing.T) {
		id := uuid.New()
		repo := stubRepo{rows: []User{{ID: id, Name: "Ada", Email: "ada@example.com"}}, total: 1}
		svc := NewService(repo)
		res, err := svc.List(context.Background(), nil, web.ListFilter{Limit: 25, Offset: 0})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if res.Total != 1 || len(res.Data) != 1 || res.Data[0].Email != "ada@example.com" {
			t.Fatalf("unexpected result: %+v", res)
		}
	})

	t.Run("list-propagates-errors", func(t *testing.T) {
		svc := NewService(stubRepo{err: errors.New("boom")})
		if _, err := svc.List(context.Background(), nil, web.ListFilter{}); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("resource-gates-fields-by-role", func(t *testing.T) {
		id := uuid.New()
		user := User{ID: id, Name: "Ada", Email: "ada@example.com", Role: "member", IsActive: true}
		admin := &middleware.Claims{UserID: uuid.New(), Role: "admin"}
		self := &middleware.Claims{UserID: id, Role: "member"}
		other := &middleware.Claims{UserID: uuid.New(), Role: "member"}

		selfView := NewUserResource(user, self).ToMap()
		if selfView["email"] != "ada@example.com" {
			t.Fatalf("self must see email: %v", selfView)
		}
		if _, ok := selfView["is_active"]; ok {
			t.Fatalf("member must not see is_active: %v", selfView)
		}
		adminView := NewUserResource(user, admin).ToMap()
		if adminView["email"] != "ada@example.com" || adminView["is_active"] != true {
			t.Fatalf("admin view = %v", adminView)
		}
		otherView := NewUserResource(user, other).ToMap()
		if _, ok := otherView["email"]; ok {
			t.Fatalf("other member must not see email: %v", otherView)
		}
		if otherView["name"] != "Ada" || otherView["role"] != "member" {
			t.Fatalf("public fields = %v", otherView)
		}

		page := web.PagedResult([]User{user}, 1, web.ListFilter{Limit: 25})
		coll := UserCollection(page, admin)
		items, ok := coll["data"].([]gin.H)
		if !ok || len(items) != 1 || items[0]["email"] != "ada@example.com" {
			t.Fatalf("collection = %v", coll)
		}
		if coll["total"] != int64(1) {
			t.Fatalf("collection paging = %v", coll)
		}
	})
}
