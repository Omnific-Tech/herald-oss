package server

import (
	"sync"

	"github.com/Omnific-Tech/herald/internal/model"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

var validatorsOnce sync.Once

// registerValidators teaches gin's binding validator the herald_path tag used
// by PublishRequest.Link.
func registerValidators() {
	validatorsOnce.Do(func() {
		engine, ok := binding.Validator.Engine().(*validator.Validate)
		if !ok {
			return
		}
		_ = engine.RegisterValidation("herald_path", func(field validator.FieldLevel) bool {
			return model.SafeLinkPath(field.Field().String())
		})
	})
}
