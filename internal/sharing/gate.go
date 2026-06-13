package sharing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// GateCookieName is the cookie carrying a passed-gate grant.
const GateCookieName = "skra_share"

// GateTTL is how long a passed gate stays valid without re-entering the secret.
const GateTTL = time.Hour

// GateSigner issues and verifies the short-lived, HMAC-signed cookie that proves
// a visitor passed a gated link's secret. The cookie binds to one token; opening
// a different gated link overwrites it.
type GateSigner struct {
	key []byte
}

// NewGateSigner returns a signer keyed by the configured session key.
func NewGateSigner(key string) GateSigner {
	return GateSigner{key: []byte(key)}
}

// Issue returns the cookie value granting access to token until now+GateTTL.
func (g GateSigner) Issue(token string, now time.Time) string {
	exp := now.Add(GateTTL).Unix()
	return fmt.Sprintf("%s.%d.%s",
		base64.RawURLEncoding.EncodeToString([]byte(token)), exp, g.mac(token, exp))
}

// Valid reports whether value is a current, untampered grant for token.
func (g GateSigner) Valid(value, token string, now time.Time) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	tokenBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || string(tokenBytes) != token {
		return false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || now.Unix() >= exp {
		return false
	}
	return hmac.Equal([]byte(parts[2]), []byte(g.mac(token, exp)))
}

func (g GateSigner) mac(token string, exp int64) string {
	m := hmac.New(sha256.New, g.key)
	fmt.Fprintf(m, "%s|%d", token, exp)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
