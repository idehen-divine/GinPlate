package users

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

type Handler struct{ svc *Service }

// NewHandler wires a users Service to its HTTP handlers.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// List serves the paged user listing.
// @Summary List users (paged example: ?limit=&offset=&sort=&order=&search=)
// @Tags Users
// @Security Bearer
// @Success 200 {object} map[string]interface{}
// @Router /users [get]
func (h *Handler) List(c *gin.Context) {
	f := web.BindFilter(c)
	res, err := h.svc.List(c.Request.Context(), web.MustDB(c), f)
	if err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusOK, "Users.", UserCollection(res, web.CurrentClaims(c)))
}
