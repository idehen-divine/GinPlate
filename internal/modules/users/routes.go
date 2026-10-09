package users

import (
	"github.com/gin-gonic/gin"
	"github.com/idehen-divine/GinPlate/internal/middleware"
	"github.com/idehen-divine/GinPlate/pkg/session"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

func init() {
	web.RegisterModule("users", func(v1 *gin.RouterGroup, d *web.ModuleDeps) {
		// Nil-safe for `route:list`: the repo needs no DB at construction.
		RegisterRoutes(v1, NewHandler(NewService(NewGormRepository())), d.Key, d.Store)
		web.RegisterRouteMeta("GET", "/api/v1/users", "auth+admin")
	})
}

// RegisterRoutes mounts GET /users (JWT-protected, admin-only). RequireAuth
// already rejects deactivated accounts, so no extra middleware is needed.
func RegisterRoutes(r *gin.RouterGroup, h *Handler, key []byte, store session.Store) {
	g := r.Group("/users")
	g.Use(middleware.RequireAuth(key, store), middleware.RequireRole(middleware.RoleAdmin))
	g.GET("", h.List)
}
