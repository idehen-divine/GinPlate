package notifications

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/idehen-divine/GinPlate/pkg/notify"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

type Handler struct{ svc *Service }

// NewHandler wires a notifications Service to its HTTP handlers.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// notifiableFor scopes every call to the caller's own inbox.
func notifiableFor(c *gin.Context) (notify.Notifiable, bool) {
	cl := web.CurrentClaims(c)
	if cl == nil {
		return notify.Notifiable{}, false
	}
	return notify.Notifiable{Type: "user", ID: cl.UserID.String()}, true
}

// List serves the caller's paged inbox plus the unread count.
// @Summary List my notifications (paged + unread count)
// @Tags Notifications
// @Security Bearer
// @Success 200 {object} map[string]interface{}
// @Router /notifications [get]
func (h *Handler) List(c *gin.Context) {
	to, ok := notifiableFor(c)
	if !ok {
		web.Render(c, web.Unauthorized("Unauthenticated."))
		return
	}
	res, err := h.svc.List(c.Request.Context(), web.MustDB(c), to, web.BindFilter(c))
	if err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusOK, "Notifications.", res)
}

// Read marks one inbox row read.
// @Summary Mark a notification read
// @Tags Notifications
// @Security Bearer
// @Param id path string true "notification id"
// @Success 200 {object} map[string]interface{}
// @Router /notifications/{id}/read [post]
func (h *Handler) Read(c *gin.Context) {
	to, ok := notifiableFor(c)
	if !ok {
		web.Render(c, web.Unauthorized("Unauthenticated."))
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		web.Render(c, web.BadRequest("Invalid notification id."))
		return
	}
	if err := h.svc.Read(c.Request.Context(), web.MustDB(c), to, id); err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusOK, "Notification marked read.", nil)
}

// ReadAll marks the whole inbox read.
// @Summary Mark all notifications read
// @Tags Notifications
// @Security Bearer
// @Success 200 {object} map[string]interface{}
// @Router /notifications/read-all [post]
func (h *Handler) ReadAll(c *gin.Context) {
	to, ok := notifiableFor(c)
	if !ok {
		web.Render(c, web.Unauthorized("Unauthenticated."))
		return
	}
	if err := h.svc.ReadAll(c.Request.Context(), web.MustDB(c), to); err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusOK, "Notifications marked read.", nil)
}
