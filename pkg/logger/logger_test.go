package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLogger is the single entry point for every logger test: daily file
// naming, day rollover, 7-day pruning, and the stdout fallback.
func TestLogger(t *testing.T) {
	t.Run("daily-filename", func(t *testing.T) {
		dir := t.TempDir()
		now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
		w := &dailyWriter{dir: dir, now: func() time.Time { return now }}
		if _, err := w.Write([]byte("hello\n")); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "2026-10-07.logs"))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "hello\n" {
			t.Fatalf("unexpected content %q", data)
		}
	})

	t.Run("rollover", func(t *testing.T) {
		dir := t.TempDir()
		day := time.Date(2026, 10, 7, 23, 59, 0, 0, time.UTC)
		w := &dailyWriter{dir: dir, now: func() time.Time { return day }}
		if _, err := w.Write([]byte("seven\n")); err != nil {
			t.Fatal(err)
		}
		day = time.Date(2026, 10, 8, 0, 1, 0, 0, time.UTC)
		if _, err := w.Write([]byte("eight\n")); err != nil {
			t.Fatal(err)
		}
		for file, want := range map[string]string{"2026-10-07.logs": "seven\n", "2026-10-08.logs": "eight\n"} {
			data, err := os.ReadFile(filepath.Join(dir, file))
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != want {
				t.Fatalf("%s = %q, want %q", file, data, want)
			}
		}
	})

	t.Run("prune-old-logs", func(t *testing.T) {
		dir := t.TempDir()
		now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
		for d := 10; d >= 1; d-- {
			name := time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC).Format("2006-01-02") + ".logs"
			if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for _, name := range []string{"notes.txt", "app.log", "bad-name.logs"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		pruneOldLogs(dir, now)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, e := range entries {
			got = append(got, e.Name())
		}
		want := []string{
			"2026-10-04.logs", "2026-10-05.logs", "2026-10-06.logs",
			"2026-10-07.logs", "2026-10-08.logs", "2026-10-09.logs",
			"2026-10-10.logs", "app.log", "bad-name.logs", "notes.txt",
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("dir = %v, want %v", got, want)
		}
	})

	t.Run("unwritable-dir-falls-back", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "blocker")
		if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		l := New("development", "info", filepath.Join(blocker, "logs"))
		if l == nil || l.SugaredLogger == nil {
			t.Fatal("expected stdout fallback logger")
		}
		l.Info("fallback works")
	})
}
