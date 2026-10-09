package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/idehen-divine/GinPlate/pkg/web"
)

func init() {
	web.RegisterGlobalMiddleware("request-id", web.DefaultGlobalMiddlewareOrder, func(router *gin.Engine, _ *web.ModuleDeps) {
		router.Use(RequestID())
	})
}

// RequestID ensures every request/response carries X-Request-ID.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if id == "" {
			id = uuid.NewString()
		}
		c.Set("request_id", id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}
