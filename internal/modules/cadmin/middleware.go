package cadmin

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/idehen-divine/GinPlate/pkg/web"
)

// RequireControlAdmin gates the control plane: it verifies a control JWT
// (never a tenant token — see Service.Parse), enforces session tracking
// when the service carries a store, reloads the admin for the active
// check, and pins the control database on the request, overriding any
// tenant handle the tenancy middleware may have set.
func RequireControlAdmin(svc *Service, controlDB *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
		if tokenStr == "" {
			web.Fail(c, http.StatusUnauthorized, "Unauthenticated.", nil)
			c.Abort()
			return
		}
		claims, err := svc.Parse(tokenStr)
		if err != nil {
			web.Fail(c, http.StatusUnauthorized, "Invalid token.", nil)
			c.Abort()
			return
		}
		if svc.store != nil {
			if _, ok := svc.store.AccessValid(c.Request.Context(), claims.SessionID); !ok {
				web.Fail(c, http.StatusUnauthorized, "Session expired.", nil)
				c.Abort()
				return
			}
		}
		var a ControlAdmin
		if err := controlDB.Where("id = ?", claims.AdminID.String()).First(&a).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				web.Fail(c, http.StatusUnauthorized, "Invalid token.", nil)
			} else {
				web.Fail(c, http.StatusInternalServerError, "Server Error.", nil)
			}
			c.Abort()
			return
		}
		if !a.IsActive {
			web.Fail(c, http.StatusUnauthorized, "Invalid token.", nil)
			c.Abort()
			return
		}
		c.Set("controlAdmin", &a)
		c.Set("db", controlDB)
		c.Next()
	}
}

// CurrentAdmin returns the authenticated control admin, if any.
func CurrentAdmin(c *gin.Context) *ControlAdmin {
	if v, ok := c.Get("controlAdmin"); ok {
		if a, ok := v.(*ControlAdmin); ok {
			return a
		}
	}
	return nil
}
