// Package welcome is the new-account notification: database inbox row plus
// the Welcome mailable. It shows the reuse pattern — ToMail returns an
// existing mailable from internal/mail instead of rebuilding content —
// and is queued on signup as the end-to-end example.
package welcome

import (
	mailwelcome "github.com/idehen-divine/GinPlate/internal/mail/welcome"
	"github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/notify"
)

// Welcome notifies a fresh account across inbox and email.
type Welcome struct {
	AppName string
	Name    string
	Email   string
	AppURL  string
}

// Type is the stored discriminator.
func (w Welcome) Type() string { return "welcome" }

// Via delivers to the inbox row and the mailbox.
func (w Welcome) Via() []string { return []string{notify.ChannelDatabase, notify.ChannelMail} }

// ToMail reuses the Welcome mailable: one template, two entry points
// (direct mail via appmail, notification fan-out here).
func (w Welcome) ToMail() (mail.Message, error) {
	return mailwelcome.Welcome{
		AppName: w.AppName,
		Name:    w.Name,
		Email:   w.Email,
		AppURL:  w.AppURL,
	}.Build()
}

// ToDatabase is the inbox payload, rendered in the notifications list.
func (w Welcome) ToDatabase() (map[string]any, error) {
	return map[string]any{
		"title": "Welcome to " + w.AppName,
		"body":  "Your account is ready. Sign in to get started.",
		"url":   w.AppURL,
	}, nil
}

var _ notify.Notification = Welcome{}
