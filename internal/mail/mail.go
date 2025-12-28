// Package appmail holds the application's mailables, mirroring Laravel's
// app/Mail: one directory per email, each with its mailable beside its
// template (password_reset/password_reset.go next to
// password_reset/password_reset.html). Each mailable implements Build()
// from pkg/mail, so handlers send it in one line:
//
//	appmail.Send(ctx, sender, "ada@example.com", passwordreset.PasswordReset{...})
//
// Transport (SMTP/SES/log drivers, MIME, queueing) stays in pkg/mail;
// this package owns content and the send helpers only. Scaffold new
// mailables with `ginplate make:mail OrderShipped`.
package appmail

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	pkgmail "github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/session"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

// Send builds m, addresses it to to (unless Build already set recipients),
// and delivers it immediately through sender.
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

// Queue builds m, addresses it to to (unless Build already set recipients),
// and pushes it as a "mail.send" job for background delivery.
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

// RenderFS executes name+".html" from a mail package's embedded templates
// (each mail directory embeds its own *.html beside its mailable).
func RenderFS(fsys embed.FS, name string, data any) (string, error) {
	tmpl, err := template.ParseFS(fsys, name+".html")
	if err != nil {
		return "", fmt.Errorf("mail: template %q: %w", name, err)
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("mail: template %q: %w", name, err)
	}
	return sb.String(), nil
}

// PreviewRequest is the dev-only payload for POST /mail/preview.
type PreviewRequest struct {
	To      string `json:"to" binding:"required,email"`
	Subject string `json:"subject"`
}

// RegisterPreviewRoutes mounts POST /mail/preview behind admin auth when
// enabled (APP_DEBUG). Disabled builds return 404 so the route never leaks
// into production. newMailable supplies the mailable to preview (usually a
// Welcome); the request's subject overrides the mailable's when set.
func RegisterPreviewRoutes(r *gin.RouterGroup, sender pkgmail.Sender, key []byte, store session.Store, enabled bool, newMailable func(to string) pkgmail.Mailable) {
	g := r.Group("/mail", web.RequireAuth(key, store), web.RequireRole(web.RoleAdmin))
	g.POST("/preview", func(c *gin.Context) {
		if !enabled {
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
