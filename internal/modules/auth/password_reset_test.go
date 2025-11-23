package auth

import (
	"context"
	"testing"

	"github.com/idehen-divine/GinPlate/pkg/mail"
)

// stubSender accepts any message, for mailer-injection tests.
type stubSender struct {
	got *mail.Message
}

func (s stubSender) Send(_ context.Context, msg mail.Message) error {
	if s.got != nil {
		*s.got = msg
	}
	return nil
}

func TestSendPasswordResetNeedsMailer(t *testing.T) {
	svc, _ := testService(t)
	if err := svc.SendPasswordReset(context.Background(), "https://app.com", "Ada", "a@b.c", "tok", 60); err == nil {
		t.Fatal("nil mailer: expected error")
	}
}

func TestSendPasswordResetDelivers(t *testing.T) {
	svc, _ := testService(t)
	var got mail.Message
	svc.WithMailer(stubSender{got: &got})
	if err := svc.SendPasswordReset(context.Background(), "https://app.com", "Ada", "a@b.c", "tok", 60); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(got.To) != 1 || got.To[0] != "a@b.c" {
		t.Fatalf("to = %v", got.To)
	}
}
