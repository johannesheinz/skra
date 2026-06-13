package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "skra.db")
	database, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database, path
}

func TestOpenSetsAutoVacuumIncremental(t *testing.T) {
	_, path := openTestDB(t)

	// Re-open with a bare DSN (no pragmas) so the query reflects the value
	// persisted in the database header, not a per-connection echo.
	bare, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("bare open: %v", err)
	}
	defer bare.Close()

	var autoVacuum int
	if err := bare.QueryRow("PRAGMA auto_vacuum").Scan(&autoVacuum); err != nil {
		t.Fatalf("query auto_vacuum: %v", err)
	}
	// 2 == INCREMENTAL.
	if autoVacuum != 2 {
		t.Errorf("auto_vacuum = %d, want 2 (INCREMENTAL)", autoVacuum)
	}
}

func TestOpenAppliesConnectionPragmas(t *testing.T) {
	database, _ := openTestDB(t)

	var journalMode string
	if err := database.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want %q", journalMode, "wal")
	}

	var foreignKeys int
	if err := database.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d, want 1 (ON)", foreignKeys)
	}

	var busyTimeout int
	if err := database.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busyTimeout)
	}
}

func TestMigrationsCreateSchema(t *testing.T) {
	database, _ := openTestDB(t)

	wantTables := []string{
		"users", "address_books", "contacts", "contact_photos",
		"address_book_members", "share_links", "sessions", "schema_migrations",
	}
	for _, table := range wantTables {
		var name string
		err := database.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not created: %v", table, err)
		}
	}

	var version int
	if err := database.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if version != 1 {
		t.Errorf("max migration version = %d, want 1", version)
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skra.db")

	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open() error: %v", err)
	}
	first.Close()

	// Re-opening an existing database must not re-run migrations or fail.
	second, err := Open(path)
	if err != nil {
		t.Fatalf("second Open() error: %v", err)
	}
	defer second.Close()

	var count int
	if err := second.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != 1 {
		t.Errorf("schema_migrations row count = %d, want 1", count)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	database, _ := openTestDB(t)

	// Inserting a contact referencing a non-existent address book must fail
	// because foreign_keys is ON.
	_, err := database.Exec(
		`INSERT INTO contacts (public_id, address_book_id, full_name, vcard_raw, uid, etag)
         VALUES ('pid', 999, 'Nobody', 'BEGIN:VCARD', 'uid', 'etag')`,
	)
	if err == nil {
		t.Error("expected foreign key violation, got nil")
	}
}

func TestParseMigrationName(t *testing.T) {
	tests := []struct {
		filename    string
		wantVersion int
		wantName    string
		wantErr     bool
	}{
		{filename: "0001_init.sql", wantVersion: 1, wantName: "init"},
		{filename: "0042_add_pepper.sql", wantVersion: 42, wantName: "add_pepper"},
		{filename: "noprefix.sql", wantErr: true},
		{filename: "bad_version.sql", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.filename, func(t *testing.T) {
			version, name, err := parseMigrationName(tc.filename)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseMigrationName(%q) expected error", tc.filename)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMigrationName(%q) error: %v", tc.filename, err)
			}
			if version != tc.wantVersion || name != tc.wantName {
				t.Errorf("parseMigrationName(%q) = (%d, %q), want (%d, %q)",
					tc.filename, version, name, tc.wantVersion, tc.wantName)
			}
		})
	}
}
