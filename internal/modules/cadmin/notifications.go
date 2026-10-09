package cadmin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/idehen-divine/GinPlate/internal/modules/notifications"
	"github.com/idehen-divine/GinPlate/pkg/notify"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

// Inbox lists the control-admin alert inbox for the caller: operational
// events (tenant created/suspended, moves requested/completed/failed)
// addressed to the shared control audience. It reuses the tenant inbox
// service against the control database — the audience scoping is identical,
// only the notifiable differs.
func Inbox(notifSvc *notifications.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if CurrentAdmin(c) == nil {
			web.Render(c, web.Unauthorized("Unauthenticated."))
			return
		}
		f := web.BindFilter(c)
		res, err := notifSvc.List(
			c.Request.Context(),
			web.MustDB(c),
			notify.Notifiable{Type: "control_admin", ID: "broadcast"},
			f,
		)
		if err != nil {
			web.Render(c, err)
			return
		}
		web.Success(c, http.StatusOK, "Notifications.", res)
	}
}
