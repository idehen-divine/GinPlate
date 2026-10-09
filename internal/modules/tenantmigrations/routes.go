package tenantmigrations

import (
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the move endpoints on a /tenants group. The group
// must already carry control-admin authentication.
func RegisterRoutes(t *gin.RouterGroup, h *Handler) {
	t.GET("/:slug/migration", h.MigrationStatus)
	t.POST("/:slug/migrate", h.Migrate)
	t.POST("/migrations/:id/rollback", h.Rollback)
}
