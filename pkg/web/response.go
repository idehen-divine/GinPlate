package web

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

// debug exposes error internals to API clients. It defaults to false
// (production-safe) and is enabled by the application when APP_DEBUG is
// true. 4xx payloads always show; 5xx payloads only show when debug.
var debugMode = false

// SetDebug toggles client-facing error detail. Call once at startup from
// the APP_DEBUG config value.
func SetDebug(v bool) { debugMode = v }

// IsDebug reports whether client-facing error detail is enabled.
func IsDebug() bool { return debugMode }

// Success responds with the standard API success shape. All five keys are
// always present (mirroring the Laravel frontend contract): success, code
// (mirrors the HTTP status), message, data, and errors (null on success).
// A nil data becomes an empty list so collections never serialize as null.
func Success(c *gin.Context, status int, message string, data interface{}) {
	if data == nil {
		data = []interface{}{}
	}
	c.JSON(status, gin.H{"success": true, "code": status, "message": message, "data": data, "errors": nil})
}

// Fail responds with the standard API error shape: success false, the code
// mirroring the HTTP status, data always null, and errors carrying the
// payload. The errs payload is included on 4xx responses (user-actionable)
// and on 5xx only when debug is enabled; production 5xx responses carry
// the message alone.
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
