// Package sharing holds the primitives for public share links: mode/scope constants, per-mode token generation, and the signed gate cookie.
// Validity of a stored link (revoked/expired/uses-exhausted) lives with the model that owns the row; this package stays free of database concerns.
package sharing

import (
	"fmt"

	"github.com/johannesheinz/skra/internal/ids"
)

// Share modes.
const (
	ModeAuthenticated = "authenticated" // any logged-in user with the link
	ModePublicLong    = "public_long"   // anyone; protected only by the long token
	ModeGated         = "gated_short"   // anyone who passes the secret gate
)

// Share scopes.
const (
	ScopeBook    = "book"
	ScopeContact = "contact"
)

// GateMaxFailures is how many wrong secret attempts lock a gated link until a manager revokes/recreates it.
// Proxy rate limiting is too coarse for slow per-link guessing, so this is enforced in the app.
const GateMaxFailures = 10

const (
	publicLongTokenBytes = 20 // 160-bit, per spec requirement for public_long
	shortTokenBytes      = 9  // ~72-bit slug; security rests on the session/secret
)

// ValidMode reports whether m is a known share mode.
func ValidMode(m string) bool {
	return m == ModeAuthenticated || m == ModePublicLong || m == ModeGated
}

// ValidScope reports whether s is a known share scope.
func ValidScope(s string) bool {
	return s == ScopeBook || s == ScopeContact
}

// NewToken generates the path token for a share of the given mode. public_long gets a high-entropy (>=160-bit) token because the URL is the only secret; other modes use a short slug.
func NewToken(mode string) (string, error) {
	switch mode {
	case ModePublicLong:
		return ids.Random(publicLongTokenBytes)
	case ModeAuthenticated, ModeGated:
		return ids.Random(shortTokenBytes)
	default:
		return "", fmt.Errorf("sharing: unknown mode %q", mode)
	}
}
