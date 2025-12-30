package makenotification

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMakeNotification is the single entry point for every
// make:notification test: name derivation, stub rendering, and file
// generation.
func TestMakeNotification(t *testing.T) {
	t.Run("derive-names", func(t *testing.T) {
		cases := []struct {
			in                string
			struct_, dir, pkg string
			file, typ         string
		}{
			{"OrderShipped", "OrderShipped", "order_shipped", "ordershipped", "order_shipped.go", "order-shipped"},
			{"Welcome", "Welcome", "welcome", "welcome", "welcome.go", "welcome"},
			{"order-shipped", "OrderShipped", "order_shipped", "ordershipped", "order_shipped.go", "order-shipped"},
			{"order_shipped", "OrderShipped", "order_shipped", "ordershipped", "order_shipped.go", "order-shipped"},
			{"User2FA", "User2Fa", "user2_fa", "user2fa", "user2_fa.go", "user2-fa"},
		}
		for _, c := range cases {
			n, err := deriveNames(c.in)
			if err != nil {
				t.Fatalf("deriveNames(%q): %v", c.in, err)
			}
			if n.Struct != c.struct_ || n.Dir != c.dir || n.Pkg != c.pkg || n.File != c.file || n.Type != c.typ {
				t.Errorf("deriveNames(%q) = %+v, want struct=%q dir=%q pkg=%q file=%q type=%q",
					c.in, n, c.struct_, c.dir, c.pkg, c.file, c.typ)
			}
		}
	})

	t.Run("derive-names-invalid", func(t *testing.T) {
		for _, in := range []string{"", "9lives", "has space!", "semi;colon", "-dash"} {
			if _, err := deriveNames(in); err == nil {
				t.Errorf("deriveNames(%q) expected error", in)
			}
		}
	})

	t.Run("render-notification", func(t *testing.T) {
		n, err := deriveNames("OrderShipped")
		if err != nil {
			t.Fatal(err)
		}
		n.Module = "example.com/demo"
		src, err := renderNotification(n)
		if err != nil {
			t.Fatal(err)
		}
		s := string(src)
		for _, want := range []string{
			"package ordershipped",
			`"example.com/demo/pkg/mail"`,
			`"example.com/demo/pkg/notify"`,
			"type OrderShipped struct",
			`func (m OrderShipped) Type() string { return "order-shipped" }`,
			"notify.ChannelDatabase, notify.ChannelMail",
			"func (m OrderShipped) ToMail() (mail.Message, error)",
			"func (m OrderShipped) ToDatabase() (map[string]any, error)",
			"var _ notify.Notification = OrderShipped{}",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("rendered source missing %q:\n%s", want, s)
			}
		}
	})

	t.Run("generate-notification-writes-file", func(t *testing.T) {
		root := t.TempDir()
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(root); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chdir(cwd) })
		if err := os.WriteFile("go.mod", []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := NewMakeNotificationCmd()
		cmd.SetArgs([]string{"OrderShipped"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join("internal", "notifications", "order_shipped", "order_shipped.go")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
		// Second run refuses without --force.
		cmd2 := NewMakeNotificationCmd()
		cmd2.SetArgs([]string{"OrderShipped"})
		if err := cmd2.Execute(); err == nil {
			t.Fatal("re-generation without --force: expected error")
		}
	})
}
