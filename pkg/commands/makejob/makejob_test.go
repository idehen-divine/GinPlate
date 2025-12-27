package makejob

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestMakeJob is the single entry point for every make:job test: name
// derivation across input styles, stub rendering with and without
// schedules, schedule-flag parsing, and end-to-end file generation.
func TestMakeJob(t *testing.T) {
	t.Run("derive-names", func(t *testing.T) {
		cases := []struct {
			in          string
			name, file  string
			fn, payload string
		}{
			{"ChargeInvoice", "charge-invoice", "charge_invoice.go", "ChargeInvoice", "ChargeInvoicePayload"},
			{"charge-invoice", "charge-invoice", "charge_invoice.go", "ChargeInvoice", "ChargeInvoicePayload"},
			{"charge_invoice", "charge-invoice", "charge_invoice.go", "ChargeInvoice", "ChargeInvoicePayload"},
			{"Billing.Charge", "billing.charge", "billing_charge.go", "BillingCharge", "BillingChargePayload"},
			{"billing.charge", "billing.charge", "billing_charge.go", "BillingCharge", "BillingChargePayload"},
			{"User2FA", "user2-fa", "user2_fa.go", "User2Fa", "User2FaPayload"},
		}
		for _, c := range cases {
			n, err := deriveJobNames(c.in)
			if err != nil {
				t.Fatalf("deriveJobNames(%q): %v", c.in, err)
			}
			if n.Name != c.name || n.File != c.file || n.Func != c.fn || n.Payload != c.payload {
				t.Errorf("deriveJobNames(%q) = %+v, want name=%q file=%q fn=%q payload=%q",
					c.in, n, c.name, c.file, c.fn, c.payload)
			}
		}
	})

	t.Run("derive-names-invalid", func(t *testing.T) {
		for _, in := range []string{"", "9lives", "has space!", "semi;colon", "-dash", "bill..charge", ".leading", "trailing."} {
			if _, err := deriveJobNames(in); err == nil {
				t.Errorf("deriveJobNames(%q) expected error", in)
			}
		}
	})

	t.Run("render-handler-only", func(t *testing.T) {
		n, err := deriveJobNames("Billing.Charge")
		if err != nil {
			t.Fatal(err)
		}
		n.Module = "example.com/demo"
		src, err := renderJob(n)
		if err != nil {
			t.Fatal(err)
		}
		s := string(src)
		for _, want := range []string{
			"package jobs",
			`"example.com/demo/pkg/queue"`,
			`queue.Handle("billing.charge", BillingCharge)`,
			"type BillingChargePayload struct",
			"func BillingCharge(ctx context.Context, job queue.Job) error",
			"json.Unmarshal(job.Payload, &payload)",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("rendered source missing %q:\n%s", want, s)
			}
		}
		if strings.Contains(s, "pkg/scheduler") {
			t.Errorf("handler-only stub should not import scheduler:\n%s", s)
		}
	})

	t.Run("render-with-schedule", func(t *testing.T) {
		n, err := deriveJobNames("Billing.Charge")
		if err != nil {
			t.Fatal(err)
		}
		n.Module = "example.com/demo"
		n.Schedule = "daily@02:00"
		n.SchedCall = ".DailyAt(2, 0)"
		src, err := renderJob(n)
		if err != nil {
			t.Fatal(err)
		}
		s := string(src)
		for _, want := range []string{
			`"example.com/demo/pkg/scheduler"`,
			`scheduler.Schedule(scheduler.New("billing.charge")`,
			`.DailyAt(2, 0)`,
			"scheduler.Data(job.Payload)",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("scheduled stub missing %q:\n%s", want, s)
			}
		}
	})

	t.Run("parse-schedule", func(t *testing.T) {
		cases := map[string]string{
			"every-minute":     ".EveryMinute()",
			"hourly":           ".Hourly()",
			"daily@02:00":      ".DailyAt(2, 0)",
			"daily@6:30":       ".DailyAt(6, 30)",
			"weekly@Mon@06:30": ".Weekly(time.Monday, 6, 30)",
			"cron:0 2 * * *":   `.Cron("0 2 * * *")`,
		}
		for in, want := range cases {
			got, err := parseSchedule(in)
			if err != nil {
				t.Fatalf("parseSchedule(%q): %v", in, err)
			}
			if got != want {
				t.Errorf("parseSchedule(%q) = %q, want %q", in, got, want)
			}
		}
		for _, bad := range []string{"minutely", "daily@25:00", "daily@nope", "weekly@Funday@01:00", "cron:not a cron", "weekly@Mon"} {
			if _, err := parseSchedule(bad); err == nil {
				t.Errorf("parseSchedule(%q) expected error", bad)
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
		sub := filepath.Join("jobs")
		if err := generateJob(cmd, sub, "Billing.Charge", "hourly", false, false); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(sub, "billing_charge.go"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), ".Hourly()") {
			t.Errorf("generated file missing schedule:\n%s", data)
		}
		if err := generateJob(cmd, sub, "Billing.Charge", "", false, false); err == nil {
			t.Error("expected exists error without --force")
		}
	})
}
