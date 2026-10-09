package users

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/idehen-divine/GinPlate/internal/middleware"
	"github.com/idehen-divine/GinPlate/pkg/web"
	"gorm.io/gorm"
)

const currentUserKey = "current_user"

// mapUserErr maps lookup failures: deleted account → 401, else 500.
func mapUserErr(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return web.Unauthorized("Unauthenticated.")
	}
	return web.Wrap(http.StatusInternalServerError, "Could not load user.", err)
}

// fetchUserByID is a var so tests can stub the query and count calls.
var fetchUserByID = func(ctx context.Context, db *gorm.DB, id uuid.UUID) (*User, error) {
	var u User
	if err := db.WithContext(ctx).Where("id = ?", id).First(&u).Error; err != nil {
		return nil, mapUserErr(err)
	}
	return &u, nil
}

// CurrentUser returns the authenticated caller, memoized per request
// (first call queries, later calls are free). Needs RequireAuth + ProvideDB.
func CurrentUser(c *gin.Context) (*User, error) {
	if v, ok := c.Get(currentUserKey); ok {
		if u, ok := v.(*User); ok && u != nil {
			return u, nil
		}
	}
	cl := middleware.CurrentClaims(c)
	if cl == nil {
		return nil, web.Unauthorized("Unauthenticated.")
	}
	u, err := fetchUserByID(c.Request.Context(), web.MustDB(c), cl.UserID)
	if err != nil {
		return nil, err
	}
	c.Set(currentUserKey, u)
	return u, nil
}

func init() {
	// Fold the active check into RequireAuth: no per-route wiring. Skipped
	// without a DB handle (tests, listing); production always has one.
	middleware.ActiveCheck = func(c *gin.Context, _ uuid.UUID) error {
		v, ok := c.Get("db")
		if !ok || v == nil {
			return nil
		}
		if db, ok := v.(*gorm.DB); !ok || db == nil {
			return nil
		}
		u, err := CurrentUser(c)
		if err != nil {
			return err
		}
		if !u.IsActive {
			return web.Unauthorized("Account deactivated.")
		}
		if cl := middleware.CurrentClaims(c); cl != nil && u.AuthVersion != cl.AuthVersion {
			return web.Unauthorized("Session revoked.")
		}
		return nil
	}
}
