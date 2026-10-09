package web

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func ParseUUID(c *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		Render(c, BadRequest("Invalid "+param+"."))
		return uuid.Nil, false
	}
	return id, true
}
