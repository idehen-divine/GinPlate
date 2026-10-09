package validator

import (
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

func Engine() *validator.Validate {
	if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
		return v
	}
	return validator.New()
}
