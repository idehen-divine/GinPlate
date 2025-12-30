package notify

import (
	"context"
	"fmt"
	"strings"
)

// MailChannel delivers the notification through the mail sender in deps.
// The recipient route comes from Notifiable.Email (set at the call site
// from the user's email); an empty route fails loudly instead of guessing.
type MailChannel struct{}

// Name matches Via entries for mail delivery.
func (MailChannel) Name() string { return ChannelMail }

// Send renders ToMail, addresses it to the notifiable's email, and sends.
func (MailChannel) Send(ctx context.Context, deps Deps, to Notifiable, n Notification) error {
	if deps.Sender == nil {
		return fmt.Errorf("notify: mail channel needs a sender")
	}
	if strings.TrimSpace(to.Email) == "" {
		return fmt.Errorf("notify: mail channel needs Notifiable.Email")
	}
	msg, err := n.ToMail()
	if err != nil {
		return err
	}
	if len(msg.To) == 0 {
		msg.To = []string{to.Email}
	}
	return deps.Sender.Send(ctx, msg)
}

var _ Channel = MailChannel{}
