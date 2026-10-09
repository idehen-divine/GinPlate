package tenantdomains

import (
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the custom-domain endpoints on a /tenants group.
// The group must already carry control-admin authentication.
func RegisterRoutes(t *gin.RouterGroup, h *Handler) {
	t.POST("/:slug/domains", h.AddDomain)
	t.GET("/:slug/domains", h.ListDomains)
	t.PATCH("/domains/:id/verify", h.VerifyDomain)
	t.DELETE("/domains/:id", h.RemoveDomain)
}
