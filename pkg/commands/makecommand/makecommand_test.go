package makecommand

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestMakeCommand is the single entry point for every make:command test:
// name derivation, stub rendering, and custom-import syncing.
func TestMakeCommand(t *testing.T) {
	t.Run("derive-names", func(t *testing.T) {
		cases := []struct {
			in                     string
			use, file, constructor string
		}{
			{"SendEmails", "send-emails", "send_emails.go", "newSendEmailsCmd"},
			{"SendReport", "send-report", "send_report.go", "newSendReportCmd"},
			{"report", "report", "report.go", "newReportCmd"},
			{"send-report", "send-report", "send_report.go", "newSendReportCmd"},
			{"send_report", "send-report", "send_report.go", "newSendReportCmd"},
			{"User2FA", "user2-fa", "user2_fa.go", "newUser2FaCmd"},
		}
		for _, c := range cases {
			n, err := deriveNames(c.in)
			if err != nil {
				t.Fatalf("deriveNames(%q): %v", c.in, err)
			}
			if n.Use != c.use || n.File != c.file || n.Constructor != c.constructor {
				t.Errorf("deriveNames(%q) = %+v, want use=%q file=%q ctor=%q",
					c.in, n, c.use, c.file, c.constructor)
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

	t.Run("render-command", func(t *testing.T) {
		n, err := deriveNames("SendReport")
		if err != nil {
			t.Fatal(err)
		}
		n.Module = "example.com/demo"
		src, err := renderCommand(n)
		if err != nil {
			t.Fatal(err)
		}
		s := string(src)
		for _, want := range []string{
			"package sendreport",
			`"example.com/demo/internal/commands"`,
			`custom.RegisterCustom("send-report", newSendReportCmd)`,
			"func newSendReportCmd() *cobra.Command",
			`Use:   "send-report"`,
		} {
			if !strings.Contains(s, want) {
				t.Errorf("rendered source missing %q:\n%s", want, s)
			}
		}
	})

	t.Run("sync-custom-imports", func(t *testing.T) {
		root := t.TempDir()
		mkpkg := func(dir string) {
			t.Helper()
			if err := os.MkdirAll(filepath.Join(root, "internal/commands", dir), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(
				filepath.Join(root, "internal/commands", dir, "cmd.go"),
				[]byte("package "+strings.ReplaceAll(dir, "-", "")+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		mkpkg("send-report")
		if err := os.MkdirAll(filepath.Join(root, "internal/commands", "empty-dir"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, "internal/commands", "notes"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "internal/commands", "notes", "todo.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}

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

		var sb strings.Builder
		cmd := &cobra.Command{}
		cmd.SetOut(&sb)
		if err := syncCustomImports(cmd, "pkg/commands", "internal/commands", false); err != nil {
			t.Fatal(err)
		}
		out, err := os.ReadFile(filepath.Join("pkg/commands", "generated.go"))
		if err != nil {
			t.Fatal(err)
		}
		s := string(out)
		if !strings.Contains(s, `"example.com/demo/internal/commands/send-report"`) {
			t.Errorf("generated imports missing send-report:\n%s", s)
		}
		if strings.Contains(s, "empty-dir") || strings.Contains(s, "notes") {
			t.Errorf("generated imports should skip dirs without Go files:\n%s", s)
		}
	})
}
