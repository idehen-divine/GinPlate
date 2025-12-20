package key

import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestKey is the single entry point for every key:generate test: in-place
// updates, append-plus-create, and generator output shape.
func TestKey(t *testing.T) {
	t.Run("write-updates-in-place", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".env")
		before := "APP_NAME=GinPlate\nAPP_KEY=\nAPP_PORT=8080\n"
		if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := writeKey(path, "base64:abc"); err != nil {
			t.Fatal(err)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		want := "APP_NAME=GinPlate\nAPP_KEY=base64:abc\nAPP_PORT=8080\n"
		if string(after) != want {
			t.Errorf("got %q, want %q", after, want)
		}
	})

	t.Run("write-appends-and-creates", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".env")
		if err := os.WriteFile(path, []byte("APP_PORT=8080\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := writeKey(path, "base64:abc"); err != nil {
			t.Fatal(err)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(string(after), "\nAPP_KEY=base64:abc\n") {
			t.Errorf("key not appended: %q", after)
		}
		fresh := filepath.Join(dir, "fresh.env")
		if err := writeKey(fresh, "base64:abc"); err != nil {
			t.Fatal(err)
		}
		freshData, err := os.ReadFile(fresh)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(freshData), "APP_KEY=base64:abc\n") {
			t.Errorf("fresh file missing key: %q", freshData)
		}
	})

	t.Run("generated-key-decodes-to-32-bytes", func(t *testing.T) {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			t.Fatal(err)
		}
		key := "base64:" + base64.StdEncoding.EncodeToString(raw)
		s, _ := strings.CutPrefix(key, "base64:")
		decoded, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		if len(decoded) != 32 {
			t.Fatalf("decoded to %d bytes, want 32", len(decoded))
		}
	})
}
