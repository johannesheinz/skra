// Package testutil provides shared helpers for tests across packages.
package testutil

import (
	"path/filepath"
	"testing"

	"github.com/johannesheinz/skra/internal/db"
)

// NewDB opens a fresh, migrated SQLite database in a temporary directory and
// registers cleanup. It fails the test on any error.
func NewDB(t *testing.T) *db.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("testutil: open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}
