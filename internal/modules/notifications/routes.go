package notifications

import (
	"github.com/gin-gonic/gin"
	"github.com/idehen-divine/GinPlate/internal/middleware"
	"github.com/idehen-divine/GinPlate/pkg/notify"
	"github.com/idehen-divine/GinPlate/pkg/session"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

func init() {
	web.RegisterModule("notifications", func(v1 *gin.RouterGroup, d *web.ModuleDeps) {
		var store notify.Store
		if d.DB != nil {
			store = notify.NewStore(d.DB)
		}
		RegisterRoutes(v1, NewHandler(NewService(store)), d.Key, d.Store)
		web.RegisterRouteMeta("GET", "/api/v1/notifications", "auth")
		web.RegisterRouteMeta("POST", "/api/v1/notifications/:id/read", "auth")
		web.RegisterRouteMeta("POST", "/api/v1/notifications/read-all", "auth")
	})
}

// RegisterRoutes mounts the caller's inbox (JWT-protected, any role).
func RegisterRoutes(r *gin.RouterGroup, h *Handler, key []byte, store session.Store) {
	g := r.Group("/notifications", middleware.RequireAuth(key, store))
	g.GET("", h.List)
	g.POST("/:id/read", h.Read)
	g.POST("/read-all", h.ReadAll)
}
