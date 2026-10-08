package web

import (
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
)

// Recovery replaces gin.Recovery with a panic handler that never exposes
// internals to remote clients. Panics always log server-side with the full
// stack through logf and render a bare 500 through Render; debug mode may
// add the panic value but never the stack trace.
func Recovery(logf func(format string, args ...interface{})) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				stack := debug.Stack()
				// Cap the logged stack so a deep recursion cannot flood logs.
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
