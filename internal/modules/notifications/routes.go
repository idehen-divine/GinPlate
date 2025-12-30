package notifications

import (
	"github.com/gin-gonic/gin"
	"github.com/idehen-divine/GinPlate/pkg/session"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

// RegisterRoutes mounts the caller's inbox (JWT-protected, any role):
// list, mark one read, mark all read. key is the raw HMAC signing key.
func RegisterRoutes(r *gin.RouterGroup, h *Handler, key []byte, store session.Store) {
	g := r.Group("/notifications", web.RequireAuth(key, store))
	g.GET("", h.List)
	g.POST("/:id/read", h.Read)
	g.POST("/read-all", h.ReadAll)
}
