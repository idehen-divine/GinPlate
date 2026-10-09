// Package appmail holds the application's mailables: one directory per
// email, each with its mailable beside its template. Transport stays in
// pkg/mail; this package owns content, send helpers, and debug preview.
package appmail

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/idehen-divine/GinPlate/internal/mail/password_reset"
	"github.com/idehen-divine/GinPlate/internal/mail/welcome"
	"github.com/idehen-divine/GinPlate/internal/middleware"
	pkgmail "github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/session"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

func init() {
	// Dev-only mail preview for every mailable below: admin JWT required,
	// 404s outside debug (the handler itself enforces the flag, so the
	// route is always registered and `route:list` stays in sync with
	// `serve`). Adding a mailable = adding one map entry here.
	web.RegisterModule("mail-preview", func(v1 *gin.RouterGroup, d *web.ModuleDeps) {
		mailables := map[string]func(to string) pkgmail.Mailable{
			"welcome": func(to string) pkgmail.Mailable {
				return welcome.Welcome{AppName: d.AppName, Name: "Preview", Email: to, AppURL: d.AppURL}
			},
			"password-reset": func(to string) pkgmail.Mailable {
				return passwordreset.PasswordReset{AppURL: d.AppURL, Name: "Preview", Email: to, Token: "preview", ExpiresMinutes: 60}
			},
		}
		RegisterPreviewRoutes(v1, d.Sender, d.Key, d.Store, d.Debug, mailables)
		web.RegisterRouteMeta("POST", "/api/v1/mail/preview/:name", "auth+admin")
	})
}

// Send delivers m immediately, addressing it to to unless already addressed.
func Send(ctx context.Context, sender pkgmail.Sender, to string, m pkgmail.Mailable) error {
	msg, err := m.Build()
	if err != nil {
		return err
	}
	if len(msg.To) == 0 {
		msg.To = []string{to}
	}
	return sender.Send(ctx, msg)
}

// Queue pushes m as a "mail.send" job for background delivery.
func Queue(ctx context.Context, mailer pkgmail.Mailer, to string, m pkgmail.Mailable) (string, error) {
	msg, err := m.Build()
	if err != nil {
		return "", err
	}
	if len(msg.To) == 0 {
		msg.To = []string{to}
	}
	return mailer.Queue(ctx, msg)
}

type PreviewRequest struct {
	To      string `json:"to" binding:"required,email"`
	Subject string `json:"subject"`
}

// RegisterPreviewRoutes mounts POST /mail/preview/:name behind admin auth
// (debug only; 404 otherwise). Unknown names 404.
func RegisterPreviewRoutes(r *gin.RouterGroup, sender pkgmail.Sender, key []byte, store session.Store, enabled bool, mailables map[string]func(to string) pkgmail.Mailable) {
	g := r.Group("/mail", middleware.RequireAuth(key, store), middleware.RequireRole(middleware.RoleAdmin))
	g.POST("/preview/:name", func(c *gin.Context) {
		if !enabled {
			web.Render(c, web.NotFound("Not found."))
			return
		}
		newMailable, ok := mailables[c.Param("name")]
		if !ok {
			web.Render(c, web.NotFound("Not found."))
			return
		}
		var req PreviewRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			web.ValidationErrors(c, err)
			return
		}
		msg, err := newMailable(req.To).Build()
		if err != nil {
			web.Render(c, err)
			return
		}
		if req.Subject != "" {
			msg.Subject = req.Subject
		}
		if err := sender.Send(c.Request.Context(), msg); err != nil {
			web.Render(c, err)
			return
		}
		web.Success(c, http.StatusOK, "Preview sent.", gin.H{"to": req.To})
	})
}
