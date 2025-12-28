// Package passwordreset is the forgot-password mailable: its struct beside
// its template, so the whole email lives in this directory.
package passwordreset

import (
	"embed"
	"fmt"
	"strings"

	appmail "github.com/idehen-divine/GinPlate/internal/mail"
	pkgmail "github.com/idehen-divine/GinPlate/pkg/mail"
)

//go:embed *.html
var templateFS embed.FS

// PasswordReset carries a forgot-password email: Token becomes a link under
// AppURL (e.g. https://app.com/reset-password?token=...). The caller
// persists the token row (see the password_reset_tokens table) then sends
// or queues this mailable:
//
//	appmail.Send(ctx, sender, user.Email, passwordreset.PasswordReset{...})
type PasswordReset struct {
	AppURL         string
	Name           string
	Email          string
	Token          string
	ExpiresMinutes int
}

// Build renders password_reset.html into a ready-to-send Message.
func (p PasswordReset) Build() (pkgmail.Message, error) {
	if strings.TrimSpace(p.Email) == "" || strings.TrimSpace(p.Token) == "" {
		return pkgmail.Message{}, fmt.Errorf("mail: email and token are required")
	}
	expires := p.ExpiresMinutes
	if expires <= 0 {
		expires = 60
	}
	resetURL := strings.TrimSuffix(strings.TrimSpace(p.AppURL), "/") + "/reset-password?token=" + p.Token
	html, err := appmail.RenderFS(templateFS, "password_reset", map[string]any{
		"Name":           p.Name,
		"Email":          p.Email,
		"ResetURL":       resetURL,
		"ExpiresMinutes": expires,
	})
	if err != nil {
		return pkgmail.Message{}, err
	}
	return pkgmail.Message{
		To:      []string{p.Email},
		Subject: "Reset your password",
		HTML:    html,
		Text:    pkgmail.StripTags(html),
		Tags:    map[string]string{"kind": "password-reset"},
	}, nil
}

var _ pkgmail.Mailable = PasswordReset{}
