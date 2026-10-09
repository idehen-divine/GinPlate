// Package resettoken owns the password-reset token contract shared by
// tenant users and control admins: 32-byte random secrets, SHA-256 hex
// digests for storage, and constant-time verification. The raw token is
// emailed once and never persisted; only the digest touches the database.
package resettoken

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
)

// Mint creates a 32-byte random reset secret encoded as unpadded base64url
// for use in emailed links.
func Mint() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// Hash returns the SHA-256 hex digest to persist for a reset token.
func Hash(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}

// Verify reports whether a presented raw token matches a stored digest,
// in constant time.
func Verify(rawToken, storedHex string) bool {
	sum := sha256.Sum256([]byte(rawToken))
	want, err := hex.DecodeString(storedHex)
	if err != nil || len(want) != sha256.Size {
		return false
	}
	return subtle.ConstantTimeCompare(sum[:], want) == 1
}
