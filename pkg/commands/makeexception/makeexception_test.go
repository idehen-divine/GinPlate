package makeexception

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestMakeException is the single entry point for every make:exception
// test: name derivation, status validation, stub rendering, and end-to-end
// file generation.
func TestMakeException(t *testing.T) {
	t.Run("derive-names", func(t *testing.T) {
		cases := []struct {
			in            string
			use, file, ty string
		}{
			{"PaymentRequired", "payment-required", "payment_required.go", "PaymentRequiredError"},
			{"payment-required", "payment-required", "payment_required.go", "PaymentRequiredError"},
			{"payment_required", "payment-required", "payment_required.go", "PaymentRequiredError"},
			{"User2FA", "user2-fa", "user2_fa.go", "User2FaError"},
		}
		for _, c := range cases {
			n, err := deriveExceptionNames(c.in)
			if err != nil {
				t.Fatalf("deriveExceptionNames(%q): %v", c.in, err)
			}
			if n.Use != c.use || n.File != c.file || n.Type != c.ty {
				t.Errorf("deriveExceptionNames(%q) = %+v, want use=%q file=%q type=%q",
					c.in, n, c.use, c.file, c.ty)
			}
		}
	})

	t.Run("derive-names-invalid", func(t *testing.T) {
		for _, in := range []string{"", "9lives", "has space!", "semi;colon", "-dash"} {
			if _, err := deriveExceptionNames(in); err == nil {
				t.Errorf("deriveExceptionNames(%q) expected error", in)
			}
		}
	})

	t.Run("render", func(t *testing.T) {
		n, err := deriveExceptionNames("PaymentRequired")
		if err != nil {
			t.Fatal(err)
		}
		n.Module = "example.com/demo"
		n.Status = 402
		src, err := renderException(n)
		if err != nil {
			t.Fatal(err)
		}
		s := string(src)
		for _, want := range []string{
			"package exceptions",
			"type PaymentRequiredError struct",
			`RegisterErrorMapper("payment-required"`,
			`"example.com/demo/pkg/web"`,
			"errors.As(err, &target)",
			"web.New(402, msg)",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("rendered source missing %q:\n%s", want, s)
			}
		}
	})

	t.Run("generate-validates-status", func(t *testing.T) {
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
		sub := filepath.Join("exceptions")
		for _, bad := range []int{199, 399, 600} {
			if err := generateException(cmd, sub, "Nope", bad, false, false); err == nil {
				t.Errorf("status %d: expected error", bad)
			}
		}
		if err := generateException(cmd, sub, "PaymentRequired", 402, false, false); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(sub, "payment_required.go"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "PaymentRequiredError") {
			t.Errorf("generated file missing type:\n%s", data)
		}
		if err := generateException(cmd, sub, "PaymentRequired", 402, false, false); err == nil {
			t.Error("expected exists error without --force")
		}
	})
}
