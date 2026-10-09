package exceptions

import (
	"errors"

	"github.com/idehen-divine/GinPlate/pkg/web"
)

// UnavailableError reports a downstream dependency failure. Return it
// instead of a bare 500 when a backend is unreachable.
type UnavailableError struct {
	Message string
	Cause   error
}

func (e *UnavailableError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *UnavailableError) Unwrap() error { return e.Cause }

func init() {
	web.RegisterErrorMapper("service-unavailable", func(err error) (*web.AppError, bool) {
		var target *UnavailableError
		if !errors.As(err, &target) {
			return nil, false
		}
		msg := target.Message
		if msg == "" {
			msg = "Service temporarily unavailable."
		}
		ae := web.New(503, msg)
		ae.Err = target.Cause
		return ae, true
	})
}
