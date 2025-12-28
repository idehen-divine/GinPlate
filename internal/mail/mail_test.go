package appmail

import (
	"context"
	"testing"

	"github.com/idehen-divine/GinPlate/pkg/config"
	pkgmail "github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/queue"
)

// recordSender captures the message it is given, for helper tests.
type recordSender struct {
	got *pkgmail.Message
}

func (r recordSender) Send(_ context.Context, msg pkgmail.Message) error {
	*r.got = msg
	return nil
}

// bareMailable builds a message with no recipients, so tests prove the
// helpers fill To from their argument.
type bareMailable struct{}

func (bareMailable) Build() (pkgmail.Message, error) {
	return pkgmail.Message{Subject: "Hi", Text: "hello"}, nil
}

func TestHelpers(t *testing.T) {
	t.Run("send-addresses-and-delivers", func(t *testing.T) {
		var got pkgmail.Message
		if err := Send(context.Background(), recordSender{&got}, "q@example.com", bareMailable{}); err != nil {
			t.Fatal(err)
		}
		if len(got.To) != 1 || got.To[0] != "q@example.com" {
			t.Fatalf("helper did not address the message: %+v", got)
		}
	})

	t.Run("send-keeps-mailable-recipients", func(t *testing.T) {
		var got pkgmail.Message
		m := bareMailableWithTo{to: "keep@example.com"}
		if err := Send(context.Background(), recordSender{&got}, "other@example.com", m); err != nil {
			t.Fatal(err)
		}
		if len(got.To) != 1 || got.To[0] != "keep@example.com" {
			t.Fatalf("helper overwrote mailable recipients: %+v", got)
		}
	})

	t.Run("queue-addresses-and-queues", func(t *testing.T) {
		var got pkgmail.Message
		reg := queue.NewRegistry()
		pkgmail.Register(reg, recordSender{&got})
		m, err := pkgmail.Open(
			config.Mail{Mailer: "log", From: config.MailFrom{Address: "h@example.com"}},
			config.S3{},
			queue.NewSync(reg),
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Queue(context.Background(), m, "q@example.com", bareMailable{}); err != nil {
			t.Fatal(err)
		}
		if len(got.To) != 1 || got.To[0] != "q@example.com" {
			t.Fatalf("queued job did not carry the address: %+v", got)
		}
	})
}

// bareMailableWithTo builds a message that already sets recipients.
type bareMailableWithTo struct{ to string }

func (m bareMailableWithTo) Build() (pkgmail.Message, error) {
	return pkgmail.Message{To: []string{m.to}, Subject: "Hi", Text: "hello"}, nil
}
