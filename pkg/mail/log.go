package mail

import (
	"context"
	"log/slog"
)

// logMailer is the dev/test driver: it validates and logs instead of
// delivering, so a bare .env still boots and tests stay hermetic.
type logMailer struct {
	fromAddr string
	fromName string
}

// NewLog returns the log driver with the configured default sender.
func NewLog(fromAddr, fromName string) Sender {
	return &logMailer{fromAddr: fromAddr, fromName: fromName}
}

// Send validates msg and logs its summary (body excluded at info level).
func (m *logMailer) Send(_ context.Context, msg Message) error {
	if err := Validate(msg); err != nil {
		return err
	}
	from := m.fromAddr
	if msg.FromAddr != "" {
		from = msg.FromAddr
	}
	slog.Info("mail send (log driver)",
		"from", formatAddr(m.fromNameOr(msg), from),
		"to", msg.To,
		"cc", msg.Cc,
		"bcc", msg.Bcc,
		"subject", msg.Subject,
		"attachments", len(msg.Files),
	)
	return nil
}

func (m *logMailer) fromNameOr(msg Message) string {
	if msg.FromName != "" {
		return msg.FromName
	}
	return m.fromName
}

var _ Sender = (*logMailer)(nil)
