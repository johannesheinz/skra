package sharing

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestNewTokenEntropyByMode(t *testing.T) {
	long, err := NewToken(ModePublicLong)
	if err != nil {
		t.Fatalf("NewToken public_long: %v", err)
	}
	if b, _ := base64.RawURLEncoding.DecodeString(long); len(b) < 20 {
		t.Errorf("public_long token = %d bytes, want >= 20 (160-bit)", len(b))
	}

	short, err := NewToken(ModeGated)
	if err != nil {
		t.Fatalf("NewToken gated: %v", err)
	}
	if short == "" {
		t.Error("gated token empty")
	}

	if _, err := NewToken("bogus"); err == nil {
		t.Error("expected error for unknown mode")
	}
}

func TestGateSignerRoundTrip(t *testing.T) {
	g := NewGateSigner("0123456789abcdef0123456789abcdef")
	now := time.Unix(1_000_000, 0)
	value := g.Issue("tok123", now)

	if !g.Valid(value, "tok123", now.Add(time.Minute)) {
		t.Error("freshly issued grant should be valid")
	}
	if g.Valid(value, "different-token", now.Add(time.Minute)) {
		t.Error("grant must not validate for a different token")
	}
	if g.Valid(value, "tok123", now.Add(GateTTL+time.Second)) {
		t.Error("expired grant should be invalid")
	}
}

func TestGateSignerRejectsTampering(t *testing.T) {
	g := NewGateSigner("0123456789abcdef0123456789abcdef")
	now := time.Unix(1_000_000, 0)
	value := g.Issue("tok", now)

	// Different key must not validate the same value.
	other := NewGateSigner("ffffffffffffffffffffffffffffffff")
	if other.Valid(value, "tok", now) {
		t.Error("grant validated under a different key")
	}
	for _, bad := range []string{"", "a.b", "a.b.c.d", "notbase64.123.mac"} {
		if g.Valid(bad, "tok", now) {
			t.Errorf("malformed value %q validated", bad)
		}
	}
}
