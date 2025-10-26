package web

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// AppError is a classified domain error: the single type every handler
// funnels through Render. Status and Message are always safe for clients;
// Fields carries user-actionable detail (e.g. per-field validation);
// Err is the internal cause, exposed only under the debug rule.
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

// Internal classifies an unexpected failure (500) carrying its cause for
// server-side logs. Clients see "Server Error", plus detail in debug mode.
func Internal(err error) *AppError {
	return &AppError{Status: http.StatusInternalServerError, Message: "Server Error", Err: err}
}

// Render writes any error as an API error response and is a no-op on nil,
// so handlers end with `return web.Render(c, err)` patterns. Mapping:
//   - gorm.ErrRecordNotFound (any wrapping) -> 404 "Not found."
//   - *AppError -> its status/message; Fields always shown, Err detail only
//     under the debug rule enforced by Fail.
//   - anything else -> 500 "Server Error", detail debug-gated by Fail.
func Render(c *gin.Context, err error) {
	if err == nil {
		return
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
