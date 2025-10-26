package validator

import (
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

// Engine returns Gin's underlying validator, or a fresh one when the
// binding engine reports an unexpected type.
func Engine() *validator.Validate {
	if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
		return v
	}
	return validator.New()
}
