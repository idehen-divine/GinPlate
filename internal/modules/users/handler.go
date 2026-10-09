package users

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/idehen-divine/GinPlate/internal/middleware"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

type Handler struct{ service UserService }

func NewHandler(service UserService) *Handler { return &Handler{service: service} }

// @Summary List users (paged example: ?limit=&offset=&sort=&order=&search=)
// @Tags Users
// @Security Bearer
// @Success 200 {object} map[string]interface{}
// @Router /users [get]
func (h *Handler) List(c *gin.Context) {
	filter := web.BindFilter(c)
	result, err := h.service.List(c.Request.Context(), web.MustDB(c), filter)
	if err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusOK, "Users.", UserCollection(result, middleware.CurrentClaims(c)))
}
