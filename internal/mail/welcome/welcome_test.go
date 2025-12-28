package welcome

import (
	"strings"
	"testing"
)

func TestWelcomeBuild(t *testing.T) {
	m := Welcome{AppName: "GinPlate", Name: "Ada", Email: "ada@example.com", AppURL: "https://app.com"}
	msg, err := m.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.To) != 1 || msg.To[0] != "ada@example.com" {
		t.Fatalf("to = %v", msg.To)
	}
	if !strings.Contains(msg.Subject, "GinPlate") {
		t.Fatalf("subject = %q", msg.Subject)
	}
	if !strings.Contains(msg.HTML, "Ada") || !strings.Contains(msg.HTML, "https://app.com") {
		t.Fatalf("html missing fields:\n%s", msg.HTML)
	}
	if msg.Text == "" {
		t.Fatal("expected text fallback")
	}
}
