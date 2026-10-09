package web

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

// debugMode gates 5xx error detail (4xx always show). Set from APP_DEBUG.
var debugMode = false

// SetDebug toggles client-facing error detail. Call once at startup.
func SetDebug(v bool) { debugMode = v }

// IsDebug reports whether client-facing error detail is enabled.
func IsDebug() bool { return debugMode }

// Success responds with the standard envelope. Nil data becomes an empty list.
func Success(c *gin.Context, status int, message string, data interface{}) {
	if data == nil {
		data = []interface{}{}
	}
	c.JSON(status, gin.H{"success": true, "code": status, "message": message, "data": data, "errors": nil})
}

// Fail responds with the error envelope. errs shows on 4xx, and on 5xx
// only when debug is enabled.
func Fail(c *gin.Context, status int, message string, errs interface{}) {
	body := gin.H{"success": false, "code": status, "message": message, "data": nil}
	if errs != nil && (status < 500 || debugMode) {
		body["errors"] = errs
	} else {
		body["errors"] = nil
	}
	c.JSON(status, body)
}

// ValidationErrors converts gin binding errors to a 422 payload with one
// human message per field. Unlike raw validator output it never exposes Go
// type or struct names, so it is safe to show in any environment.
func ValidationErrors(c *gin.Context, err error) {
	fields := map[string]string{}
	if ve, ok := err.(validator.ValidationErrors); ok {
		for _, fe := range ve {
			// LowerCamelCase the Go field name to approximate its JSON
			// key (Email -> email) without struct reflection.
			name := fe.Field()
			fields[strings.ToLower(name[:1])+name[1:]] = "failed '" + fe.Tag() + "' validation"
		}
	}
	if len(fields) == 0 {
		fields["body"] = "malformed request payload"
	}
	Render(c, &AppError{Status: http.StatusUnprocessableEntity, Message: "The given data was invalid.", Fields: fields})
}
