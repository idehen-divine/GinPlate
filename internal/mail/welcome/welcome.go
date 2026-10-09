// Package welcome is the new-account greeting mailable.
package welcome

import (
	"embed"

	pkgmail "github.com/idehen-divine/GinPlate/pkg/mail"
)

//go:embed *.html
var templateFS embed.FS

type Welcome struct {
	AppName string
	Name    string
	Email   string
	AppURL  string
}

func (w Welcome) Build() (pkgmail.Message, error) {
	html, err := pkgmail.RenderFS(templateFS, "welcome", w)
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
