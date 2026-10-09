package web

import (
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
)

// Recovery catches panics: logs server-side with stack, renders a bare 500.
func Recovery(logf func(format string, args ...interface{})) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				stack := debug.Stack()
				if len(stack) > 8*1024 {
					stack = stack[:8*1024]
				}
				logf("panic: %v\n%s", rec, stack)
				Render(c, &AppError{
					Status:  http.StatusInternalServerError,
					Message: "Server Error",
				})
			}
		}()
		c.Next()
	}
}
