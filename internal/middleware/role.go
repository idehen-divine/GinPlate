package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/idehen-divine/GinPlate/pkg/web"
)

// Role is an account role. Compare against RoleAdmin/RoleMember, never literals.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

// RequireRole rejects callers lacking a listed role (mount after RequireAuth:
// missing claims → 401, wrong role → 403). Combine freely per route.
func RequireRole(roles ...Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		cl := CurrentClaims(c)
		if cl == nil {
			web.Fail(c, http.StatusUnauthorized, "Unauthenticated.", nil)
			c.Abort()
			return
		}
		for _, r := range roles {
			if cl.Role == r {
				c.Next()
				return
			}
		}
		web.Fail(c, http.StatusForbidden, "Forbidden.", nil)
		c.Abort()
	}
}
