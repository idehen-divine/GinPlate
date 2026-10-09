package exceptions

import (
	"errors"

	"github.com/idehen-divine/GinPlate/pkg/web"
)

// TimeoutError reports a downstream timeout. Return it instead of a bare
// 500 when a dependency deadline is exceeded.
type TimeoutError struct {
	Message string
	Cause   error
}

func (e *TimeoutError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *TimeoutError) Unwrap() error { return e.Cause }

func init() {
	web.RegisterErrorMapper("gateway-timeout", func(err error) (*web.AppError, bool) {
		var target *TimeoutError
		if !errors.As(err, &target) {
			return nil, false
		}
		msg := target.Message
		if msg == "" {
			msg = "Gateway timeout."
		}
		ae := web.New(504, msg)
		ae.Err = target.Cause
		return ae, true
	})
}
