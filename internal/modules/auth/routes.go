package auth

import (
	"github.com/gin-gonic/gin"
	"github.com/idehen-divine/GinPlate/internal/middleware"
	"github.com/idehen-divine/GinPlate/pkg/session"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

func init() {
	web.RegisterModule("auth", func(v1 *gin.RouterGroup, d *web.ModuleDeps) {
		ttl := d.AccessTTLMin
		if ttl <= 0 {
			ttl = 60
		}
		service := NewService(d.Key, ttl, d.Store).WithMailer(d.Sender)
		if d.Notifier != nil && d.Queue != nil {
			service.WithNotifications(d.Notifier, d.Queue, d.AppName, d.AppURL)
		}
		RegisterRoutes(v1, NewHandler(service), d.Key, d.Store)
		web.RegisterRouteMeta("POST", "/api/v1/auth/signup", "-")
		web.RegisterRouteMeta("POST", "/api/v1/auth/login", "-")
		web.RegisterRouteMeta("POST", "/api/v1/auth/refresh", "-")
		web.RegisterRouteMeta("POST", "/api/v1/auth/check", "-")
		web.RegisterRouteMeta("POST", "/api/v1/auth/forgot", "-")
		web.RegisterRouteMeta("POST", "/api/v1/auth/reset", "-")
		web.RegisterRouteMeta("POST", "/api/v1/auth/logout", "auth")
		web.RegisterRouteMeta("GET", "/api/v1/auth/me", "auth")
	})
}

// RegisterRoutes mounts signup/login/refresh/check/forgot/reset (public) and logout/me (JWT).
func RegisterRoutes(r *gin.RouterGroup, h *Handler, key []byte, store session.Store) {
	g := r.Group("/auth")
	g.POST("/signup", h.Signup)
	g.POST("/login", h.Login)
	g.POST("/refresh", h.Refresh)
	g.POST("/check", h.Check)
	g.POST("/forgot", h.Forgot)
	g.POST("/reset", h.Reset)
	g.POST("/logout", middleware.RequireAuth(key, store), h.Logout)
	g.GET("/me", middleware.RequireAuth(key, store), h.Me)
}
