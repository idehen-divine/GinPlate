package web

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// MustDB returns the request's *gorm.DB (panics when no provider ran).
func MustDB(c *gin.Context) *gorm.DB {
	return c.MustGet("db").(*gorm.DB)
}

func ProvideDB(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("db", db)
		c.Next()
	}
}
