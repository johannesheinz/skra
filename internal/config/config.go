// Package config loads and validates Skra's runtime configuration from the
// environment. Per project policy there are no silent defaults: a missing or
// empty required variable is a startup error.
package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds the validated runtime configuration.
type Config struct {
	// Listen is the internal bind address, e.g. "127.0.0.1:3000".
	Listen string
	// DBPath is the SQLite file location.
	DBPath string
}

// envKeys are the environment variables read at startup.
const (
	envListen = "SKRA_LISTEN"
	envDBPath = "SKRA_DB_PATH"
)

// Load reads configuration from the environment and validates it.
// It returns an error naming every missing required variable rather than
// falling back to a default.
func Load() (Config, error) {
	var missing []string

	listen := strings.TrimSpace(os.Getenv(envListen))
	if listen == "" {
		missing = append(missing, envListen)
	}

	dbPath := strings.TrimSpace(os.Getenv(envDBPath))
	if dbPath == "" {
		missing = append(missing, envDBPath)
	}

	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	return Config{Listen: listen, DBPath: dbPath}, nil
}
