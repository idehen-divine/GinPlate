// Package welcome is the new-account notification (inbox row + Welcome mailable).
package welcome

import (
	mailwelcome "github.com/idehen-divine/GinPlate/internal/mail/welcome"
	"github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/notify"
)

type Welcome struct {
	AppName string
	Name    string
	Email   string
	AppURL  string
}

func (w Welcome) Type() string { return "welcome" }

func (w Welcome) Via() []string { return []string{notify.ChannelDatabase, notify.ChannelMail} }

// ToMail reuses the Welcome mailable.
func (w Welcome) ToMail() (mail.Message, error) {
	return mailwelcome.Welcome{
		AppName: w.AppName,
		Name:    w.Name,
		Email:   w.Email,
		AppURL:  w.AppURL,
	}.Build()
}

func (w Welcome) ToDatabase() (map[string]any, error) {
	return map[string]any{
		"title": "Welcome to " + w.AppName,
		"body":  "Your account is ready. Sign in to get started.",
		"url":   w.AppURL,
	}, nil
}

var _ notify.Notification = Welcome{}
