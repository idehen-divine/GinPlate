package users

import (
	"github.com/gin-gonic/gin"
	"github.com/idehen-divine/GinPlate/pkg/session"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

// RegisterRoutes mounts GET /users (JWT-protected, admin-only) as the
// repository-pattern example. key is the raw HMAC signing key.
func RegisterRoutes(r *gin.RouterGroup, h *Handler, key []byte, store session.Store) {
	g := r.Group("/users")
	g.Use(web.RequireAuth(key, store), web.RequireRole(web.RoleAdmin))
	g.GET("", h.List)
}
