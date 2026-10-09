package tenants

import (
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the tenant admin endpoints. The group must already
// carry control-admin authentication; these handlers assume a control-plane
// *gorm.DB on the request (set by cadmin middleware, not the tenant one).
func RegisterRoutes(r *gin.RouterGroup, h *Handler) {
	t := r.Group("/tenants")
	{
		t.POST("", h.Create)
		t.GET("", h.List)
		t.GET("/:slug", h.Get)
		t.PATCH("/:slug/status", h.SetStatus)
		t.DELETE("/:slug", h.Remove)
	}
}
