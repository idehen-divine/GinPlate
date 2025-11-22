package auth

import (
	"github.com/gin-gonic/gin"
	"github.com/idehen-divine/GinPlate/pkg/session"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

// RegisterRoutes mounts /auth/signup, /login, /refresh, /check (public) and
// /logout, /me (JWT-protected). key is the raw HMAC signing key.
func RegisterRoutes(r *gin.RouterGroup, h *Handler, key []byte, store session.Store) {
	g := r.Group("/auth")
	g.POST("/signup", h.Signup)
	g.POST("/login", h.Login)
	g.POST("/refresh", h.Refresh)
	g.POST("/check", h.Check)
	g.POST("/logout", web.RequireAuth(key, store), h.Logout)
	g.GET("/me", web.RequireAuth(key, store), h.Me)
}
