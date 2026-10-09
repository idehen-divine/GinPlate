package resettoken

import (
	"strings"
	"testing"
)

// TestResetToken covers mint/hash/verify without any I/O.
func TestResetToken(t *testing.T) {
	t.Run("round-trip", func(t *testing.T) {
		raw, err := Mint()
		if err != nil {
			t.Fatal(err)
		}
		if raw == "" || strings.ContainsAny(raw, "+/=") {
			t.Fatalf("not base64url: %q", raw)
		}
		hash := Hash(raw)
		if len(hash) != 64 {
			t.Fatalf("hash length = %d", len(hash))
		}
		if !Verify(raw, hash) {
			t.Fatal("valid token rejected")
		}
	})

	t.Run("tamper-rejected", func(t *testing.T) {
		raw, err := Mint()
		if err != nil {
			t.Fatal(err)
		}
		hash := Hash(raw)
		if Verify(raw+"x", hash) {
			t.Fatal("tampered token accepted")
		}
		other, err := Mint()
		if err != nil {
			t.Fatal(err)
		}
		if raw == other {
			t.Fatal("identical mints")
		}
		if Verify(other, hash) {
			t.Fatal("cross token accepted")
		}
		if Verify(raw, "not-hex") || Verify(raw, "") {
			t.Fatal("malformed digest accepted")
		}
	})
}
