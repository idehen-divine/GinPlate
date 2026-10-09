package mail

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/idehen-divine/GinPlate/pkg/queue"
)

func validMessage() Message {
	return Message{
		To:      []string{"ada@example.com"},
		Subject: "Hello",
		Text:    "hi",
	}
}

// SenderFunc adapts a func to the Sender interface for tests.
type SenderFunc func(ctx context.Context, msg Message) error

func (f SenderFunc) Send(ctx context.Context, msg Message) error { return f(ctx, msg) }

// fakeSMTP is a minimal SMTP server for tests: greeting, EHLO, MAIL/RCPT/
// DATA/QUIT. It captures the envelope and raw body. With starttlsFail it
// advertises STARTTLS and then fails the negotiation, proving clients fail
// closed instead of downgrading to plaintext.
type fakeSMTP struct {
	t            *testing.T
	listener     net.Listener
	from         string
	rcpts        []string
	data         string
	done         chan struct{}
	starttlsFail bool
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeSMTP{t: t, listener: ln, done: make(chan struct{})}
	go s.serve()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *fakeSMTP) addr() (string, int) {
	a := s.listener.Addr().(*net.TCPAddr)
	return "127.0.0.1", a.Port
}

func (s *fakeSMTP) serve() {
	defer close(s.done)
	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	write := func(format string, args ...any) {
		fmt.Fprintf(conn, format+"\r\n", args...)
	}
	readLine := func() string {
		line, err := r.ReadString('\n')
		if err != nil {
			s.t.Errorf("smtp read: %v", err)
			return ""
		}
		return strings.TrimRight(line, "\r\n")
	}
	write("220 fake ready")
	for {
		line := readLine()
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO") || strings.HasPrefix(upper, "HELO"):
			if s.starttlsFail {
				write("250-fake")
				write("250-STARTTLS")
				write("250 HELP")
			} else {
				write("250-fake")
				write("250 HELP")
			}
		case strings.HasPrefix(upper, "STARTTLS"):
			if s.starttlsFail {
				write("454 TLS not available")
			} else {
				write("502 unimplemented")
			}
		case strings.HasPrefix(upper, "MAIL FROM:"):
			s.from = line[len("MAIL FROM:"):]
			write("250 ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			s.rcpts = append(s.rcpts, line[len("RCPT TO:"):])
			write("250 ok")
		case strings.HasPrefix(upper, "DATA"):
			write("354 end with .")
			var sb strings.Builder
			for {
				l := readLine()
				if l == "." {
					break
				}
				sb.WriteString(l + "\n")
			}
			s.data = sb.String()
			write("250 ok")
		case strings.HasPrefix(upper, "RSET"):
			write("250 ok")
		case strings.HasPrefix(upper, "QUIT"):
			write("221 bye")
			return
		default:
			write("502 unimplemented")
		}
	}
}

// TestMail is the single entry point for every pkg/mail test: validation,
// raw MIME building, log-driver send and inline queueing, factory
// rejection, queued-job round trip, tag stripping, and SMTP delivery
// against a fake server.
func TestMail(t *testing.T) {
	t.Run("validate", func(t *testing.T) {
		if err := Validate(validMessage()); err != nil {
			t.Fatalf("valid: %v", err)
		}
		for name, mutate := range map[string]func(*Message){
			"no-to":      func(m *Message) { m.To = nil },
			"bad-to":     func(m *Message) { m.To = []string{"nope"} },
			"no-subject": func(m *Message) { m.Subject = "" },
			"no-body":    func(m *Message) { m.Text, m.HTML = "", "" },
		} {
			m := validMessage()
			mutate(&m)
			if err := Validate(m); err == nil {
				t.Fatalf("%s: expected error", name)
			}
		}
		m := validMessage()
		m.Files = []Attachment{{Filename: "a.txt", Data: []byte("x"), Inline: true}}
		if err := Validate(m); err == nil {
			t.Fatal("inline without content ID: expected error")
		}
	})

	t.Run("build-raw-text-and-html", func(t *testing.T) {
		msg := Message{
			To:      []string{"a@example.com", "b@example.com"},
			Cc:      []string{"c@example.com"},
			Bcc:     []string{"hidden@example.com"},
			Subject: "Hi",
			Text:    "hello",
			HTML:    "<p>hello</p>",
			Tags:    map[string]string{"kind": "test"},
		}
		raw, err := buildRaw("from@example.com", "GinPlate", msg)
		if err != nil {
			t.Fatal(err)
		}
		s := string(raw)
		for _, want := range []string{"multipart/alternative", "hello", "X-Tag-kind: test", "Cc: c@example.com"} {
			if !strings.Contains(s, want) {
				t.Fatalf("raw missing %q:\n%s", want, s)
			}
		}
		if strings.Contains(s, "hidden@example.com") {
			t.Fatal("Bcc must stay in the envelope, not the headers")
		}
	})

	t.Run("build-raw-attachment", func(t *testing.T) {
		msg := validMessage()
		msg.Files = []Attachment{{
			Filename:    "note.txt",
			ContentType: "text/plain",
			Data:        []byte("attachment-body"),
		}}
		raw, err := buildRaw("from@example.com", "", msg)
		if err != nil {
			t.Fatal(err)
		}
		s := string(raw)
		if !strings.Contains(s, `filename="note.txt"`) {
			t.Fatalf("attachment part missing:\n%s", s)
		}
		if !strings.Contains(s, "YXR0YWNobWVudC1ib2R5") {
			t.Fatalf("attachment body not base64-encoded:\n%s", s)
		}
	})

	t.Run("log-send-and-queue-inline", func(t *testing.T) {
		m, err := Open(config.Mail{Mailer: "log", From: config.MailFrom{Address: "h@example.com"}}, config.S3{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.Send(context.Background(), validMessage()); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Queue(context.Background(), validMessage()); err != nil {
			t.Fatal(err)
		}
		if err := m.Send(context.Background(), Message{}); err == nil {
			t.Fatal("invalid message: expected error")
		}
	})

	t.Run("open-rejects-unknown-driver", func(t *testing.T) {
		if _, err := Open(config.Mail{Mailer: "pigeon"}, config.S3{}, nil); err == nil {
			t.Fatal("expected error for unknown mailer")
		}
	})

	t.Run("queued-job-round-trip", func(t *testing.T) {
		var got Message
		sender := SenderFunc(func(_ context.Context, m Message) error { got = m; return nil })
		reg := queue.NewRegistry()
		Register(reg, sender)
		m, err := Open(config.Mail{Mailer: "log"}, config.S3{}, queue.NewSync(reg))
		if err != nil {
			t.Fatal(err)
		}
		want := Message{
			To:      []string{"q@example.com"},
			Subject: "Queued",
			Text:    "via job",
			Files:   []Attachment{{Filename: "f.bin", Data: []byte{1, 2, 3}}},
		}
		if _, err := m.Queue(context.Background(), want); err != nil {
			t.Fatal(err)
		}
		if got.Subject != want.Subject || len(got.Files) != 1 || len(got.Files[0].Data) != 3 {
			t.Fatalf("job payload mismatch: %+v", got)
		}
	})

	t.Run("strip-tags", func(t *testing.T) {
		if got := StripTags("<p>hi <b>there</b></p>"); got != "hi there" {
			t.Fatalf("StripTags = %q", got)
		}
		if got := StripTags("plain"); got != "plain" {
			t.Fatalf("StripTags = %q", got)
		}
	})

	t.Run("smtp/send-envelope-and-body", func(t *testing.T) {
		srv := newFakeSMTP(t)
		host, port := srv.addr()
		sender, err := NewSMTP(config.Mail{
			Host:       host,
			Port:       port,
			From:       config.MailFrom{Address: "from@example.com", Name: "GinPlate"},
			TimeoutSec: 5,
		})
		if err != nil {
			t.Fatal(err)
		}
		msg := Message{
			To:      []string{"a@example.com"},
			Cc:      []string{"c@example.com"},
			Bcc:     []string{"hidden@example.com"},
			Subject: "Hello",
			Text:    "hi there",
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := sender.Send(ctx, msg); err != nil {
			t.Fatal(err)
		}
		select {
		case <-srv.done:
		case <-time.After(5 * time.Second):
			t.Fatal("server did not finish")
		}
		if len(srv.rcpts) != 3 {
			t.Fatalf("envelope rcpts = %v, want To+Cc+Bcc", srv.rcpts)
		}
		// Bcc travels in the envelope (asserted above), never in the headers.
		if strings.Contains(srv.data, "hidden@example.com") {
			t.Fatalf("Bcc leaked into headers:\n%s", srv.data)
		}
		if !strings.Contains(srv.data, "hi there") {
			t.Fatalf("body missing text part:\n%s", srv.data)
		}
	})

	t.Run("smtp/starttls-failure-aborts", func(t *testing.T) {
		srv := newFakeSMTP(t)
		srv.starttlsFail = true
		host, port := srv.addr()
		sender, err := NewSMTP(config.Mail{
			Host:       host,
			Port:       port,
			From:       config.MailFrom{Address: "from@example.com"},
			TimeoutSec: 5,
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = sender.Send(ctx, validMessage())
		if err == nil || !strings.Contains(err.Error(), "starttls") {
			t.Fatalf("STARTTLS failure must abort: got %v", err)
		}
		if srv.from != "" || len(srv.rcpts) != 0 {
			t.Fatalf("nothing must be transmitted after failed STARTTLS: from=%q rcpts=%v", srv.from, srv.rcpts)
		}
	})

	t.Run("smtp/refuses-plaintext-auth", func(t *testing.T) {
		srv := newFakeSMTP(t)
		host, port := srv.addr()
		sender, err := NewSMTP(config.Mail{
			Host:       host,
			Port:       port,
			Username:   "user",
			Password:   "secret",
			From:       config.MailFrom{Address: "from@example.com"},
			TimeoutSec: 5,
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = sender.Send(ctx, validMessage())
		if err == nil || !strings.Contains(err.Error(), "without TLS") {
			t.Fatalf("plaintext auth must be refused: got %v", err)
		}
		if srv.from != "" {
			t.Fatalf("credentials must not be transmitted: from=%q", srv.from)
		}
	})
}
