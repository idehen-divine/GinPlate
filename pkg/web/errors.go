package web

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// AppError is a classified domain error: the single type handlers funnel
// through Render. Status/Message are client-safe; Err stays debug-gated.
type AppError struct {
	Status  int
	Message string
	Fields  map[string]string
	Err     error
}

// Error implements error so AppError flows through ordinary err returns.
func (e *AppError) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

// Unwrap exposes the internal cause for errors.Is/As.
func (e *AppError) Unwrap() error { return e.Err }

// New classifies a message at any status.
func New(status int, message string) *AppError {
	return &AppError{Status: status, Message: message}
}

// Wrap classifies an internal cause at a status with a safe message.
func Wrap(status int, message string, err error) *AppError {
	return &AppError{Status: status, Message: message, Err: err}
}

// NotFound classifies a missing resource (404).
func NotFound(message string) *AppError {
	return &AppError{Status: http.StatusNotFound, Message: message}
}

// Unauthorized classifies bad/missing credentials (401).
func Unauthorized(message string) *AppError {
	return &AppError{Status: http.StatusUnauthorized, Message: message}
}

// Forbidden classifies a valid caller lacking permission (403).
func Forbidden(message string) *AppError {
	return &AppError{Status: http.StatusForbidden, Message: message}
}

// Conflict classifies a state clash such as a duplicate (409).
func Conflict(message string) *AppError {
	return &AppError{Status: http.StatusConflict, Message: message}
}

// BadRequest classifies a malformed client request (400).
func BadRequest(message string) *AppError {
	return &AppError{Status: http.StatusBadRequest, Message: message}
}

// Internal classifies an unexpected failure (500). Clients see "Server Error".
func Internal(err error) *AppError {
	return &AppError{Status: http.StatusInternalServerError, Message: "Server Error", Err: err}
}

// ErrorMapper maps a custom error type to a response: (*AppError, true) to
// handle, (nil, false) to pass. First match wins, in registration order.
type ErrorMapper func(err error) (*AppError, bool)

var (
	errorMapperNames []string
	errorMappers     = map[string]ErrorMapper{}
)

// RegisterErrorMapper registers a custom mapping under name (later calls
// replace). Register from an init() in internal/exceptions.
func RegisterErrorMapper(name string, fn ErrorMapper) {
	if _, ok := errorMappers[name]; !ok {
		errorMapperNames = append(errorMapperNames, name)
	}
	errorMappers[name] = fn
}

// registeredErrorMappers returns mappers in registration order.
func registeredErrorMappers() []ErrorMapper {
	out := make([]ErrorMapper, 0, len(errorMapperNames))
	for _, n := range errorMapperNames {
		if fn, ok := errorMappers[n]; ok && fn != nil {
			out = append(out, fn)
		}
	}
	return out
}

// Render writes any error as an API error response (no-op on nil). Order:
// custom mappers, gorm not-found -> 404, *AppError -> its status, else 500.
func Render(c *gin.Context, err error) {
	if err == nil {
		return
	}
	for _, fn := range registeredErrorMappers() {
		if ae, ok := fn(err); ok && ae != nil {
			status := ae.Status
			if status == 0 {
				status = http.StatusInternalServerError
			}
			var detail interface{}
			if ae.Fields != nil {
				detail = ae.Fields
			} else if ae.Err != nil {
				detail = ae.Err.Error()
			}
			Fail(c, status, ae.Message, detail)
			return
		}
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		Fail(c, http.StatusNotFound, "Not found.", nil)
		return
	}
	var ae *AppError
	if errors.As(err, &ae) {
		status := ae.Status
		if status == 0 {
			status = http.StatusInternalServerError
		}
		var detail interface{}
		if ae.Fields != nil {
			detail = ae.Fields
		} else if ae.Err != nil {
			detail = ae.Err.Error()
		}
		Fail(c, status, ae.Message, detail)
		return
	}
	Fail(c, http.StatusInternalServerError, "Server Error", err.Error())
}
