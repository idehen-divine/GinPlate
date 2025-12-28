package passwordreset

import (
	"strings"
	"testing"
)

// TestPasswordReset is the single entry point for every password-reset
// mailable test: content rendering and required-field validation.
func TestPasswordReset(t *testing.T) {
	t.Run("build", func(t *testing.T) {
		m := PasswordReset{AppURL: "https://app.com/", Name: "Ada", Email: "ada@example.com", Token: "tok123", ExpiresMinutes: 60}
		msg, err := m.Build()
		if err != nil {
			t.Fatal(err)
		}
		if len(msg.To) != 1 || msg.To[0] != "ada@example.com" {
			t.Fatalf("to = %v", msg.To)
		}
		if !strings.Contains(msg.HTML, "tok123") || !strings.Contains(msg.HTML, "https://app.com/reset-password") {
			t.Fatalf("reset link missing:\n%s", msg.HTML)
		}
		if msg.Text == "" || msg.Subject == "" {
			t.Fatal("expected subject and text fallback")
		}
		if msg.Tags["kind"] != "password-reset" {
			t.Fatalf("tags = %v", msg.Tags)
		}
	})

	t.Run("requires-email-and-token", func(t *testing.T) {
		if _, err := (PasswordReset{Email: "a@b.c"}).Build(); err == nil {
			t.Fatal("missing token: expected error")
		}
		if _, err := (PasswordReset{Token: "x"}).Build(); err == nil {
			t.Fatal("missing email: expected error")
		}
	})
}
