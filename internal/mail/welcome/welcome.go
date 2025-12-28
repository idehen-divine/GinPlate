// Package welcome is the new-account greeting mailable: its struct beside
// its template, so the whole email lives in this directory. Copy this
// directory's shape (or run `ginplate make:mail`) for your own mailables.
package welcome

import (
	"embed"

	appmail "github.com/idehen-divine/GinPlate/internal/mail"
	pkgmail "github.com/idehen-divine/GinPlate/pkg/mail"
)

//go:embed *.html
var templateFS embed.FS

// Welcome greets a new account.
type Welcome struct {
	AppName string
	Name    string
	Email   string
	AppURL  string
}

// Build renders welcome.html into a ready-to-send Message.
func (w Welcome) Build() (pkgmail.Message, error) {
	html, err := appmail.RenderFS(templateFS, "welcome", w)
	if err != nil {
		return pkgmail.Message{}, err
	}
	return pkgmail.Message{
		To:      []string{w.Email},
		Subject: "Welcome to " + w.AppName,
		HTML:    html,
		Text:    pkgmail.StripTags(html),
		Tags:    map[string]string{"kind": "welcome"},
	}, nil
}

var _ pkgmail.Mailable = Welcome{}
