package web

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
)

// Recovery replaces gin.Recovery with a debug-aware panic handler. Panics
// always log server-side through logf and render 500 through Render, so
// panic responses share the envelope and debug rule with domain errors.
// Pass the app logger's Errorf as logf so panics land in the log file,
// not just the terminal.
func Recovery(logf func(format string, args ...interface{})) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				stack := debug.Stack()
				logf("panic: %v\n%s", rec, stack)
				Render(c, &AppError{
					Status:  http.StatusInternalServerError,
					Message: "Server Error",
					Fields:  map[string]string{"exception": fmt.Sprintf("%v", rec), "trace": string(stack)},
				})
			}
		}()
		c.Next()
	}
}
