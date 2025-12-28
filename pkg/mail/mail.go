// Package mail delivers outgoing email behind a small interface so the
// driver can change without touching callers. Three drivers ship:
//
//	log  - writes to the logs, no delivery (dev/test default).
//	smtp - real delivery via MAIL_HOST/PORT/USERNAME/PASSWORD/ENCRYPTION.
//	ses  - AWS SESv2 raw send, reusing the standard AWS_* credential chain.
//
// Messages support To/Cc/Bcc, From/ReplyTo overrides, Text+HTML bodies,
// attachments (regular + inline), custom headers, and tags/metadata.
// Sending is sync via Send; Queue pushes a "mail.send" job onto the
// configured queue backend (sync runs inline, like every other job).
package mail

import (
	"context"
	"fmt"
	"strings"

	"github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/idehen-divine/GinPlate/pkg/queue"
)

// Attachment is one file carried with a Message. Data is held in memory;
// keep attachments small (a few MB) or upload via pkg/storage and link
// instead. Inline attachments need a ContentID referenced from the HTML
// as <img src="cid:...">.
type Attachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	Data        []byte `json:"data,omitempty"`
	Inline      bool   `json:"inline,omitempty"`
	ContentID   string `json:"content_id,omitempty"`
}

// Message is one email. FromAddr/FromName and ReplyTo override the
// configured defaults when set; Tags carry vendor metadata (mapped to
// SES message tags and X-Tag-* headers on SMTP).
type Message struct {
	To       []string          `json:"to"`
	Cc       []string          `json:"cc,omitempty"`
	Bcc      []string          `json:"bcc,omitempty"`
	FromAddr string            `json:"from_addr,omitempty"`
	FromName string            `json:"from_name,omitempty"`
	ReplyTo  string            `json:"reply_to,omitempty"`
	Subject  string            `json:"subject"`
	Text     string            `json:"text,omitempty"`
	HTML     string            `json:"html,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Tags     map[string]string `json:"tags,omitempty"`
	Files    []Attachment      `json:"files,omitempty"`
}

// Mailable builds a Message. Implement it on per-email types (e.g. a
// password-reset mailable in internal/modules/auth) so callers compose
// content without importing SMTP details.
type Mailable interface {
	Build() (Message, error)
}

// Sender delivers one message immediately.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// Mailer delivers immediately (Send) or in the background (Queue pushes
// a "mail.send" job; sync backends run it inline).
type Mailer interface {
	Sender
	Queue(ctx context.Context, msg Message) (string, error)
}

// Validate checks the message shape before any driver touches the network.
func Validate(msg Message) error {
	if len(msg.To) == 0 {
		return fmt.Errorf("mail: at least one To recipient is required")
	}
	for _, addr := range append(append(append([]string{}, msg.To...), msg.Cc...), msg.Bcc...) {
		if !strings.Contains(strings.TrimSpace(addr), "@") {
			return fmt.Errorf("mail: invalid recipient %q", addr)
		}
	}
	if strings.TrimSpace(msg.Subject) == "" {
		return fmt.Errorf("mail: subject is required")
	}
	if strings.TrimSpace(msg.Text) == "" && strings.TrimSpace(msg.HTML) == "" {
		return fmt.Errorf("mail: text or HTML body is required")
	}
	for i, f := range msg.Files {
		if strings.TrimSpace(f.Filename) == "" {
			return fmt.Errorf("mail: attachment %d needs a filename", i)
		}
		if len(f.Data) == 0 {
			return fmt.Errorf("mail: attachment %q is empty", f.Filename)
		}
		if f.Inline && strings.TrimSpace(f.ContentID) == "" {
			return fmt.Errorf("mail: inline attachment %q needs a content ID", f.Filename)
		}
	}
	return nil
}

// mailer wraps a Sender with an optional queue for background delivery.
// A nil queue makes Queue fall back to inline Send.
type mailer struct {
	sender Sender
	q      queue.Queue
}

// Send delivers msg immediately.
func (m *mailer) Send(ctx context.Context, msg Message) error {
	return m.sender.Send(ctx, msg)
}

// Queue pushes msg as a "mail.send" job. With a nil queue (or a sync
// queue whose registry handles the job) delivery happens inline.
func (m *mailer) Queue(ctx context.Context, msg Message) (string, error) {
	if err := Validate(msg); err != nil {
		return "", err
	}
	if m.q == nil {
		if err := m.sender.Send(ctx, msg); err != nil {
			return "", err
		}
		return "inline", nil
	}
	return m.q.Push(ctx, JobName, mustMarshal(msg))
}

// Open returns the configured mail driver with Queue wired to q (nil ok).
// Unknown MAIL_MAILER values fail fast so typos surface at startup.
func Open(mailCfg config.Mail, awsCfg config.S3, q queue.Queue) (Mailer, error) {
	var sender Sender
	var err error
	switch mailCfg.Mailer {
	case "", "log":
		sender = NewLog(mailCfg.From.Address, mailCfg.From.Name)
	case "smtp":
		sender, err = NewSMTP(mailCfg)
		if err != nil {
			return nil, err
		}
	case "ses":
		sender, err = NewSES(mailCfg, awsCfg)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("mail: unsupported mailer %q", mailCfg.Mailer)
	}
	return &mailer{sender: sender, q: q}, nil
}

// OpenSender returns just the sync driver, for workers and tests that
// never queue.
func OpenSender(mailCfg config.Mail, awsCfg config.S3) (Sender, error) {
	m, err := Open(mailCfg, awsCfg, nil)
	if err != nil {
		return nil, err
	}
	return m, nil
}

var (
	_ Sender = (*mailer)(nil)
	_ Mailer = (*mailer)(nil)
)
