// Package passwordreset is the forgot-password mailable.
package passwordreset

import (
	"embed"
	"fmt"
	"strings"

	pkgmail "github.com/idehen-divine/GinPlate/pkg/mail"
)

//go:embed *.html
var templateFS embed.FS

// PasswordReset carries a forgot-password email. The caller persists the
// token row, then sends or queues this mailable.
type PasswordReset struct {
	AppURL         string
	Name           string
	Email          string
	Token          string
	ExpiresMinutes int
}

func (p PasswordReset) Build() (pkgmail.Message, error) {
	if strings.TrimSpace(p.Email) == "" || strings.TrimSpace(p.Token) == "" {
		return pkgmail.Message{}, fmt.Errorf("mail: email and token are required")
	}
	expires := p.ExpiresMinutes
	if expires <= 0 {
		expires = 60
	}
	resetURL := strings.TrimSuffix(strings.TrimSpace(p.AppURL), "/") + "/reset-password?token=" + p.Token
	html, err := pkgmail.RenderFS(templateFS, "password_reset", map[string]any{
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
