package ids

import (
	"encoding/base64"
	"testing"
)

func TestGeneratorsProduceDecodableValuesOfExpectedEntropy(t *testing.T) {
	tests := []struct {
		name      string
		gen       func() (string, error)
		wantBytes int
	}{
		{"public id", NewPublicID, publicIDBytes},
		{"session id", NewSessionID, sessionIDBytes},
		{"csrf token", NewCSRFToken, csrfTokenBytes},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := tc.gen()
			if err != nil {
				t.Fatalf("generator error: %v", err)
			}
			decoded, err := base64.RawURLEncoding.DecodeString(s)
			if err != nil {
				t.Fatalf("value %q is not raw-url base64: %v", s, err)
			}
			if len(decoded) != tc.wantBytes {
				t.Errorf("decoded length = %d, want %d", len(decoded), tc.wantBytes)
			}
		})
	}
}

func TestValuesAreUnique(t *testing.T) {
	const n = 1000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		s, err := NewPublicID()
		if err != nil {
			t.Fatalf("NewPublicID error: %v", err)
		}
		if _, dup := seen[s]; dup {
			t.Fatalf("duplicate public id generated: %q", s)
		}
		seen[s] = struct{}{}
	}
}

func TestRandomRejectsNonPositiveLength(t *testing.T) {
	if _, err := Random(0); err == nil {
		t.Error("Random(0) expected error, got nil")
	}
	if _, err := Random(-5); err == nil {
		t.Error("Random(-5) expected error, got nil")
	}
}
