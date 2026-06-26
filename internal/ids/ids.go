// Package ids generates the random, unguessable identifiers Skra exposes externally (public_id), plus opaque session ids and share tokens.
// All values come from crypto/rand and are URL-safe base64 without padding.
package ids

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// Byte lengths for the different identifier classes. public_id is >=128-bit per the spec; session ids and long share tokens use more entropy.
const (
	publicIDBytes  = 16 // 128-bit
	sessionIDBytes = 32 // 256-bit
	csrfTokenBytes = 32 // 256-bit
)

// NewPublicID returns a 128-bit URL-safe identifier for externally referenced rows (users, address books, contacts, share links).
func NewPublicID() (string, error) {
	return randomString(publicIDBytes)
}

// NewSessionID returns a 256-bit opaque session identifier.
func NewSessionID() (string, error) {
	return randomString(sessionIDBytes)
}

// NewCSRFToken returns a 256-bit token for the double-submit CSRF cookie.
func NewCSRFToken() (string, error) {
	return randomString(csrfTokenBytes)
}

// Random returns a URL-safe base64 string carrying n random bytes.
// It is the building block for tokens whose entropy is chosen at the call site (e.g. the >=160-bit public share tokens).
func Random(n int) (string, error) {
	return randomString(n)
}

func randomString(n int) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("ids: byte length must be positive, got %d", n)
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("ids: read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
