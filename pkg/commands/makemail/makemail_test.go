package makemail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMakeMail is the single entry point for every make:mail test: name
// derivation, stub rendering, and file generation.
func TestMakeMail(t *testing.T) {
	t.Run("derive-names", func(t *testing.T) {
		cases := []struct {
			in           string
			struct_, dir string
			pkg, file    string
			template     string
		}{
			{"OrderShipped", "OrderShipped", "order_shipped", "ordershipped", "order_shipped.go", "order_shipped.html"},
			{"Welcome", "Welcome", "welcome", "welcome", "welcome.go", "welcome.html"},
			{"order-shipped", "OrderShipped", "order_shipped", "ordershipped", "order_shipped.go", "order_shipped.html"},
			{"order_shipped", "OrderShipped", "order_shipped", "ordershipped", "order_shipped.go", "order_shipped.html"},
			{"User2FA", "User2Fa", "user2_fa", "user2fa", "user2_fa.go", "user2_fa.html"},
		}
		for _, c := range cases {
			n, err := deriveNames(c.in)
			if err != nil {
				t.Fatalf("deriveNames(%q): %v", c.in, err)
			}
			if n.Struct != c.struct_ || n.Dir != c.dir || n.Pkg != c.pkg || n.File != c.file || n.Template != c.template {
				t.Errorf("deriveNames(%q) = %+v, want struct=%q dir=%q pkg=%q file=%q template=%q",
					c.in, n, c.struct_, c.dir, c.pkg, c.file, c.template)
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

	t.Run("render-mailable", func(t *testing.T) {
		n, err := deriveNames("OrderShipped")
		if err != nil {
			t.Fatal(err)
		}
		n.Module = "example.com/demo"
		src, err := renderMailable(n)
		if err != nil {
			t.Fatal(err)
		}
		s := string(src)
		for _, want := range []string{
			"package ordershipped",
			`"example.com/demo/pkg/mail"`,
			"type OrderShipped struct",
			"func (m OrderShipped) Build() (pkgmail.Message, error)",
			`RenderFS(templateFS, "order_shipped", m)`,
			`"kind": "order_shipped"`,
		} {
			if !strings.Contains(s, want) {
				t.Errorf("rendered source missing %q:\n%s", want, s)
			}
		}
		html, err := renderHTML(n)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(html), "{{.Name}}") {
			t.Errorf("rendered html missing placeholder:\n%s", html)
		}
	})

	t.Run("generate-mail-writes-pair", func(t *testing.T) {
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
		cmd := NewMakeMailCmd()
		cmd.SetArgs([]string{"OrderShipped"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{
			filepath.Join("internal", "mail", "order_shipped", "order_shipped.go"),
			filepath.Join("internal", "mail", "order_shipped", "order_shipped.html"),
		} {
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("expected %s to exist: %v", path, err)
			}
		}
		// Second run refuses without --force.
		cmd2 := NewMakeMailCmd()
		cmd2.SetArgs([]string{"OrderShipped"})
		if err := cmd2.Execute(); err == nil {
			t.Fatal("re-generation without --force: expected error")
		}
	})
}
