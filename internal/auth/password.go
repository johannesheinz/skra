// Package auth provides password hashing, sessions, CSRF protection, and the HTTP middleware that ties them together.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/crypto/argon2"
)

// ErrMismatch is returned by VerifyPassword when the password does not match the stored hash.
// It is deliberately generic to avoid leaking which factor failed.
var ErrMismatch = errors.New("auth: password does not match")

// argon2Params are the cost parameters for a single argon2id hash.
// They are embedded in the PHC string so an old hash can always be verified with the parameters it was created with, while DefaultParams drives new hashes and the rehash policy.
type argon2Params struct {
	memoryKiB   uint32
	iterations  uint32
	parallelism uint8
	saltLen     uint32
	keyLen      uint32
}

// DefaultParams follows the spec's guidance: 64 MB memory, 3 iterations, parallelism tracking available cores.
// Tune for the target hardware.
var DefaultParams = argon2Params{
	memoryKiB:   64 * 1024,
	iterations:  3,
	parallelism: parallelism(),
	saltLen:     16,
	keyLen:      32,
}

func parallelism() uint8 {
	n := runtime.NumCPU()
	if n < 1 {
		return 1
	}
	if n > 255 {
		return 255
	}
	return uint8(n)
}

// HashPassword hashes password with DefaultParams and returns a PHC-encoded string suitable for storing in users.password_hash.
// The salt is generated from a CSPRNG and embedded in the output; there is no separate salt column.
func HashPassword(password string) (string, error) {
	return hashWith(password, DefaultParams)
}

func hashWith(password string, p argon2Params) (string, error) {
	salt := make([]byte, p.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.iterations, p.memoryKiB, p.parallelism, p.keyLen)
	return encode(p, salt, key), nil
}

// VerifyPassword checks password against a PHC-encoded hash in constant time with respect to the derived key.
// It returns ErrMismatch on a wrong password and a different error if the stored hash is malformed.
func VerifyPassword(encoded, password string) error {
	p, salt, want, err := decode(encoded)
	if err != nil {
		return err
	}
	got := argon2.IDKey([]byte(password), salt, p.iterations, p.memoryKiB, p.parallelism, p.keyLen)
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrMismatch
	}
	return nil
}

// NeedsRehash reports whether a stored hash uses weaker parameters than current policy, so a successful login can transparently upgrade it.
func NeedsRehash(encoded string) (bool, error) {
	p, _, _, err := decode(encoded)
	if err != nil {
		return false, err
	}
	weaker := p.memoryKiB < DefaultParams.memoryKiB ||
		p.iterations < DefaultParams.iterations ||
		p.parallelism < DefaultParams.parallelism ||
		p.keyLen < DefaultParams.keyLen
	return weaker, nil
}

func encode(p argon2Params, salt, key []byte) string {
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		p.memoryKiB, p.iterations, p.parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
}

func decode(encoded string) (argon2Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	// "" / argon2id / v=19 / m=..,t=..,p=.. / salt / key
	if len(parts) != 6 || parts[1] != "argon2id" {
		return argon2Params{}, nil, nil, fmt.Errorf("auth: unsupported password hash format")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return argon2Params{}, nil, nil, fmt.Errorf("auth: parse hash version: %w", err)
	}
	if version != argon2.Version {
		return argon2Params{}, nil, nil, fmt.Errorf("auth: incompatible argon2 version %d", version)
	}

	var p argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memoryKiB, &p.iterations, &p.parallelism); err != nil {
		return argon2Params{}, nil, nil, fmt.Errorf("auth: parse hash params: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argon2Params{}, nil, nil, fmt.Errorf("auth: decode salt: %w", err)
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argon2Params{}, nil, nil, fmt.Errorf("auth: decode key: %w", err)
	}
	p.saltLen = uint32(len(salt))
	p.keyLen = uint32(len(key))
	return p, salt, key, nil
}
