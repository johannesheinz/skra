package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestHashAndVerifyRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	if err := VerifyPassword(hash, "correct horse battery staple"); err != nil {
		t.Errorf("VerifyPassword on correct password: %v", err)
	}
}

func TestVerifyRejectsWrongPassword(t *testing.T) {
	hash, err := HashPassword("s3cret")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	if err := VerifyPassword(hash, "wrong"); !errors.Is(err, ErrMismatch) {
		t.Errorf("VerifyPassword wrong password = %v, want ErrMismatch", err)
	}
}

func TestHashIsPHCEncodedAndSalted(t *testing.T) {
	hash, err := HashPassword("pw")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$") {
		t.Errorf("hash %q is not a PHC argon2id string", hash)
	}

	// Same password hashed twice must differ because of the random salt.
	other, err := HashPassword("pw")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	if hash == other {
		t.Error("two hashes of the same password are identical; salt is not random")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	for _, bad := range []string{
		"",
		"not-a-hash",
		"$argon2id$v=19$m=65536,t=3,p=4$only-one-segment",
		"$bcrypt$v=19$m=1,t=1,p=1$c2FsdA$a2V5",
	} {
		if err := VerifyPassword(bad, "pw"); err == nil || errors.Is(err, ErrMismatch) {
			t.Errorf("VerifyPassword(%q) = %v, want a format error (not nil, not ErrMismatch)", bad, err)
		}
	}
}

func TestNeedsRehash(t *testing.T) {
	current, err := HashPassword("pw")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	need, err := NeedsRehash(current)
	if err != nil {
		t.Fatalf("NeedsRehash error: %v", err)
	}
	if need {
		t.Error("freshly created hash should not need rehash")
	}

	weak := argon2Params{memoryKiB: 8 * 1024, iterations: 1, parallelism: 1, saltLen: 16, keyLen: 32}
	weakHash, err := hashWith("pw", weak)
	if err != nil {
		t.Fatalf("hashWith error: %v", err)
	}
	need, err = NeedsRehash(weakHash)
	if err != nil {
		t.Fatalf("NeedsRehash error: %v", err)
	}
	if !need {
		t.Error("hash with weaker params should need rehash")
	}
	// A weak hash must still verify, so the upgrade can happen on login.
	if err := VerifyPassword(weakHash, "pw"); err != nil {
		t.Errorf("VerifyPassword on weak hash: %v", err)
	}
}
