// Package config loads and validates Skra's runtime configuration from the environment. Per project policy there are no silent defaults: a missing, empty, or malformed required variable is a startup error.
package config

import (
	"fmt"
	"os"
	"strings"
)

// minSessionKeyLen is the minimum accepted length for the cookie-signing key.
const minSessionKeyLen = 32

// Config holds the validated runtime configuration.
type Config struct {
	// Listen is the internal bind address, e.g. "127.0.0.1:3000".
	Listen string
	// DBPath is the SQLite file location.
	DBPath string
	// CookieSecure sets the Secure flag on cookies. It must be derived from the external scheme, not the internal HTTP connection: the app sees plain HTTP behind a TLS-terminating proxy, so naive code would emit non-Secure cookies on an HTTPS site.
	CookieSecure bool
	// ExternalURL is the public origin (e.g. https://contacts.example.com) used to build absolute share links. No trailing slash.
	ExternalURL string
	// SessionKey signs share-gate cookies (HMAC). Keep it secret and stable; rotating it invalidates outstanding gate cookies.
	SessionKey string
}

const (
	envListen       = "SKRA_LISTEN"
	envDBPath       = "SKRA_DB_PATH"
	envCookieSecure = "SKRA_COOKIE_SECURE"
	envExternalURL  = "SKRA_EXTERNAL_URL"
	envSessionKey   = "SKRA_SESSION_KEY"
)

// Load reads configuration from the environment and validates it. It returns an error describing every problem found rather than falling back to a default.
func Load() (Config, error) {
	var problems []string

	listen := strings.TrimSpace(os.Getenv(envListen))
	if listen == "" {
		problems = append(problems, "missing "+envListen)
	}

	dbPath := strings.TrimSpace(os.Getenv(envDBPath))
	if dbPath == "" {
		problems = append(problems, "missing "+envDBPath)
	}

	cookieSecure, err := parseBool(envCookieSecure)
	if err != nil {
		problems = append(problems, err.Error())
	}

	externalURL := strings.TrimRight(strings.TrimSpace(os.Getenv(envExternalURL)), "/")
	switch {
	case externalURL == "":
		problems = append(problems, "missing "+envExternalURL)
	case !strings.HasPrefix(externalURL, "http://") && !strings.HasPrefix(externalURL, "https://"):
		problems = append(problems, envExternalURL+" must start with http:// or https://")
	}

	sessionKey := os.Getenv(envSessionKey)
	switch {
	case strings.TrimSpace(sessionKey) == "":
		problems = append(problems, "missing "+envSessionKey)
	case len(sessionKey) < minSessionKeyLen:
		problems = append(problems, fmt.Sprintf("%s must be at least %d characters", envSessionKey, minSessionKeyLen))
	}

	if len(problems) > 0 {
		return Config{}, fmt.Errorf("invalid configuration: %s", strings.Join(problems, "; "))
	}

	return Config{
		Listen:       listen,
		DBPath:       dbPath,
		CookieSecure: cookieSecure,
		ExternalURL:  externalURL,
		SessionKey:   sessionKey,
	}, nil
}

// parseBool requires the variable to be exactly "true" or "false" — there is no default, and any other value (including empty) is an error.
func parseBool(key string) (bool, error) {
	switch strings.TrimSpace(os.Getenv(key)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "":
		return false, fmt.Errorf("missing %s", key)
	default:
		return false, fmt.Errorf("%s must be \"true\" or \"false\"", key)
	}
}
