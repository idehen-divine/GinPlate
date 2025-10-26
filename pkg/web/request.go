package web

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ParseUUID reads a UUID path parameter (e.g. /users/:id), rendering 400
// for anything unparseable. It collapses the three-line parse-check-render
// block repeated in every detail handler.
func ParseUUID(c *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		Render(c, BadRequest("Invalid "+param+"."))
		return uuid.Nil, false
	}
	return id, true
}
