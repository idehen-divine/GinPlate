package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/idehen-divine/GinPlate/pkg/config"
)

// smtpMailer encryption: "" (STARTTLS when advertised), "tls" (required),
// or "ssl" (implicit TLS).
type smtpMailer struct {
	host       string
	port       int
	username   string
	password   string
	encryption string
	timeout    time.Duration
	fromAddr   string
	fromName   string
}

func NewSMTP(cfg config.Mail) (Sender, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, fmt.Errorf("mail: smtp needs MAIL_HOST")
	}
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &smtpMailer{
		host:       cfg.Host,
		port:       cfg.Port,
		username:   cfg.Username,
		password:   cfg.Password,
		encryption: strings.ToLower(strings.TrimSpace(cfg.Encryption)),
		timeout:    timeout,
		fromAddr:   cfg.From.Address,
		fromName:   cfg.From.Name,
	}, nil
}

func (m *smtpMailer) Send(ctx context.Context, msg Message) error {
	raw, err := buildRaw(m.fromAddr, m.fromName, msg)
	if err != nil {
		return err
	}
	from := m.fromAddr
	if msg.FromAddr != "" {
		from = msg.FromAddr
	}
	to := envelopeRecipients(msg)
	if len(to) == 0 {
		return fmt.Errorf("mail: no recipients")
	}
	addr := fmt.Sprintf("%s:%d", m.host, m.port)
	dialer := &net.Dialer{Timeout: m.timeout}

	var client *smtp.Client
	if m.encryption == "ssl" {
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: m.host}) //nolint:gosec // STARTTLS-equivalent for smtps; host-verified.
		if err != nil {
			return fmt.Errorf("mail: smtp dial: %w", err)
		}
		client, err = smtp.NewClient(conn, m.host)
		if err != nil {
			return fmt.Errorf("mail: smtp client: %w", err)
		}
	} else {
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("mail: smtp dial: %w", err)
		}
		client, err = smtp.NewClient(conn, m.host)
		if err != nil {
			return fmt.Errorf("mail: smtp client: %w", err)
		}
	}
	defer func() { _ = client.Quit() }()

	if err := client.Hello("localhost"); err != nil {
		return fmt.Errorf("mail: smtp hello: %w", err)
	}
	// Fail closed on TLS: an advertised-but-failing STARTTLS aborts delivery
	// (a downgrade attacker must not silently win), and authenticated SMTP
	// without TLS is refused. Plaintext stays available only for
	// unauthenticated local development servers.
	if ok, _ := client.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{ServerName: m.host} //nolint:gosec // STARTTLS with host verification.
		if err := client.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("mail: smtp starttls: %w", err)
		}
	} else if m.username != "" || m.encryption == "tls" {
		return fmt.Errorf("mail: refusing SMTP without TLS")
	}
	if m.username != "" {
		auth := smtp.PlainAuth("", m.username, m.password, m.host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("mail: smtp auth: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("mail: smtp mail from: %w", err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(strings.TrimSpace(rcpt)); err != nil {
			return fmt.Errorf("mail: smtp rcpt %q: %w", rcpt, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("mail: smtp data: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		_ = w.Close()
		return fmt.Errorf("mail: smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: smtp send: %w", err)
	}
	return nil
}

var _ Sender = (*smtpMailer)(nil)
