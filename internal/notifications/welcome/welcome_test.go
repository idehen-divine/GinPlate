package welcome

import (
	"strings"
	"testing"

	"github.com/idehen-divine/GinPlate/pkg/notify"
)

func TestWelcomeNotification(t *testing.T) {
	n := Welcome{AppName: "GinPlate", Name: "Ada", Email: "ada@example.com", AppURL: "https://app.com"}
	if n.Type() != "welcome" {
		t.Fatalf("type = %q", n.Type())
	}
	via := n.Via()
	if len(via) != 2 || via[0] != notify.ChannelDatabase || via[1] != notify.ChannelMail {
		t.Fatalf("via = %v", via)
	}
	msg, err := n.ToMail()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.HTML, "Ada") {
		t.Fatalf("mail missing name:\n%s", msg.HTML)
	}
	data, err := n.ToDatabase()
	if err != nil {
		t.Fatal(err)
	}
	if data["title"] == "" || data["url"] != "https://app.com" {
		t.Fatalf("database payload = %v", data)
	}
}
