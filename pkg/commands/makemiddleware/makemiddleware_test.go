package makemiddleware

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestMakeMiddleware is the single entry point for every make:middleware
// test: name derivation, both stub renderings, and end-to-end generation.
func TestMakeMiddleware(t *testing.T) {
	t.Run("derive-names", func(t *testing.T) {
		cases := []struct {
			in            string
			use, file, fn string
		}{
			{"AuditLog", "audit-log", "audit_log.go", "AuditLog"},
			{"audit-log", "audit-log", "audit_log.go", "AuditLog"},
			{"audit_log", "audit-log", "audit_log.go", "AuditLog"},
			{"RequestID", "request-id", "request_id.go", "RequestId"},
			{"User2FA", "user2-fa", "user2_fa.go", "User2Fa"},
		}
		for _, c := range cases {
			n, err := deriveMiddlewareNames(c.in)
			if err != nil {
				t.Fatalf("deriveMiddlewareNames(%q): %v", c.in, err)
			}
			if n.Use != c.use || n.File != c.file || n.Func != c.fn {
				t.Errorf("deriveMiddlewareNames(%q) = %+v, want use=%q file=%q fn=%q",
					c.in, n, c.use, c.file, c.fn)
			}
		}
	})

	t.Run("derive-names-invalid", func(t *testing.T) {
		for _, in := range []string{"", "9lives", "has space!", "semi;colon", "-dash"} {
			if _, err := deriveMiddlewareNames(in); err == nil {
				t.Errorf("deriveMiddlewareNames(%q) expected error", in)
			}
		}
	})

	t.Run("render-per-route", func(t *testing.T) {
		n, err := deriveMiddlewareNames("AuditLog")
		if err != nil {
			t.Fatal(err)
		}
		n.Module = "example.com/demo"
		src, err := renderMiddleware(n)
		if err != nil {
			t.Fatal(err)
		}
		s := string(src)
		for _, want := range []string{
			"package middleware",
			"func AuditLog() gin.HandlerFunc",
			"c.Next()",
			"middleware.AuditLog",
			"auth+audit-log",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("rendered source missing %q:\n%s", want, s)
			}
		}
		if strings.Contains(s, "RegisterGlobalMiddleware") {
			t.Errorf("per-route stub must not self-register:\n%s", s)
		}
	})

	t.Run("render-global", func(t *testing.T) {
		n, err := deriveMiddlewareNames("RequestID")
		if err != nil {
			t.Fatal(err)
		}
		n.Module = "example.com/demo"
		n.Global = true
		src, err := renderMiddleware(n)
		if err != nil {
			t.Fatal(err)
		}
		s := string(src)
		for _, want := range []string{
			"package middleware",
			`func init()`,
			`RegisterGlobalMiddleware("request-id"`,
			"func RequestId() gin.HandlerFunc",
			`"example.com/demo/pkg/web"`,
		} {
			if !strings.Contains(s, want) {
				t.Errorf("rendered source missing %q:\n%s", want, s)
			}
		}
	})

	t.Run("generate-writes-file", func(t *testing.T) {
		dir := t.TempDir()
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chdir(cwd) })
		cmd := &cobra.Command{}
		var sb strings.Builder
		cmd.SetOut(&sb)
		sub := filepath.Join("middleware")
		if err := generateMiddleware(cmd, sub, "AuditLog", false, false, false); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(sub, "audit_log.go"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "func AuditLog() gin.HandlerFunc") {
			t.Errorf("generated file missing constructor:\n%s", data)
		}
		if err := generateMiddleware(cmd, sub, "AuditLog", false, false, false); err == nil {
			t.Error("expected exists error without --force")
		}
		if err := generateMiddleware(cmd, sub, "TraceID", true, false, false); err != nil {
			t.Fatal(err)
		}
		global, err := os.ReadFile(filepath.Join(sub, "trace_id.go"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(global), "RegisterGlobalMiddleware") {
			t.Errorf("global file missing registration:\n%s", global)
		}
	})
}
