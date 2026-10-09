package mail

import (
	"context"
	"log/slog"
)

type logMailer struct {
	fromAddr string
	fromName string
}

func NewLog(fromAddr, fromName string) Sender {
	return &logMailer{fromAddr: fromAddr, fromName: fromName}
}

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
