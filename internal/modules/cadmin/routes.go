package cadmin

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterRoutes mounts control auth: login, forgot, and reset stay open;
// me, logout, and the alert inbox require a control admin. The db on these
// requests is the control database (set by the caller via ProvideDB).
func RegisterRoutes(r *gin.RouterGroup, h *Handler, svc *Service, controlDB *gorm.DB, inbox gin.HandlerFunc) {
	auth := r.Group("/auth")
	{
		auth.POST("/login", h.Login)
		auth.POST("/forgot", h.Forgot)
		auth.POST("/reset", h.Reset)
		auth.GET("/me", RequireControlAdmin(svc, controlDB), h.Me)
		auth.POST("/logout", RequireControlAdmin(svc, controlDB), h.Logout)
	}
	if inbox != nil {
		r.GET("/notifications", RequireControlAdmin(svc, controlDB), inbox)
	}
}
