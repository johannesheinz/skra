package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
)

func openTestDB(t *testing.T) (*DB, string) {
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
		"import_uploads",
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
	if version != 2 {
		t.Errorf("max migration version = %d, want 2", version)
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
	if count != 2 {
		t.Errorf("schema_migrations row count = %d, want 2", count)
	}
}

func TestConcurrentWritesAreSerialized(t *testing.T) {
	database, _ := openTestDB(t)
	ctx := context.Background()

	if _, err := database.ExecWrite(ctx, "CREATE TABLE counter (n INTEGER NOT NULL)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := database.ExecWrite(ctx, "INSERT INTO counter (n) VALUES (0)"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const goroutines, perGoroutine = 8, 50
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				// A read-modify-write inside one serialized tx; if writes were
				// not serialized this would lose updates or hit lock errors.
				err := database.Write(ctx, func(tx *sql.Tx) error {
					var n int
					if err := tx.QueryRow("SELECT n FROM counter").Scan(&n); err != nil {
						return err
					}
					_, err := tx.Exec("UPDATE counter SET n = ?", n+1)
					return err
				})
				if err != nil {
					t.Errorf("write: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	var n int
	if err := database.QueryRow("SELECT n FROM counter").Scan(&n); err != nil {
		t.Fatalf("final read: %v", err)
	}
	if want := goroutines * perGoroutine; n != want {
		t.Errorf("counter = %d, want %d (lost updates indicate writes were not serialized)", n, want)
	}
}

func TestSnapshot(t *testing.T) {
	database, _ := openTestDB(t)
	ctx := context.Background()

	// Seed a row so we can confirm it survives into the snapshot.
	if _, err := database.ExecWrite(ctx,
		`INSERT INTO users (public_id, username, email, password_hash, role)
		 VALUES ('p', 'alice', 'a@e.com', 'h', 'admin')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out := filepath.Join(t.TempDir(), "backup.db")
	if err := database.Snapshot(ctx, out); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// The snapshot opens as a valid database with the row present.
	snap, err := sql.Open("sqlite", "file:"+out)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer snap.Close()
	var n int
	if err := snap.QueryRow("SELECT COUNT(*) FROM users WHERE username='alice'").Scan(&n); err != nil {
		t.Fatalf("query snapshot: %v", err)
	}
	if n != 1 {
		t.Errorf("snapshot user count = %d, want 1", n)
	}

	// Refuses to overwrite an existing file.
	if err := database.Snapshot(ctx, out); err == nil {
		t.Error("Snapshot should refuse to overwrite an existing file")
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
