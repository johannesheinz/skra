package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migration is a single embedded SQL migration, identified by a monotonic integer version parsed from its filename prefix (e.g. "0001_init.sql" -> 1).
type migration struct {
	version int
	name    string
	sql     string
}

// migrate applies all embedded migrations that have not yet been recorded in the schema_migrations table. Each migration runs in its own transaction so a failure leaves the database at the last fully-applied version.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
        version    INTEGER PRIMARY KEY,
        name       TEXT NOT NULL,
        applied_at TEXT NOT NULL DEFAULT (datetime('now'))
    )`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := appliedVersions(db)
	if err != nil {
		return err
	}

	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		if err := applyMigration(db, m); err != nil {
			return fmt.Errorf("apply migration %04d_%s: %w", m.version, m.name, err)
		}
	}
	return nil
}

// hasPendingMigrations reports whether an existing database has embedded migrations not yet recorded in schema_migrations. If schema_migrations does not exist yet, there is nothing to snapshot (the runner will create it), so it returns false.
func hasPendingMigrations(db *sql.DB) (bool, error) {
	var exists int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&exists); err != nil {
		return false, fmt.Errorf("check schema_migrations: %w", err)
	}
	if exists == 0 {
		return false, nil
	}
	applied, err := appliedVersions(db)
	if err != nil {
		return false, err
	}
	migrations, err := loadMigrations()
	if err != nil {
		return false, err
	}
	for _, m := range migrations {
		if !applied[m.version] {
			return true, nil
		}
	}
	return false, nil
}

func appliedVersions(db *sql.DB) (map[int]bool, error) {
	rows, err := db.Query("SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("query applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}
	return applied, nil
}

func applyMigration(db *sql.DB, m migration) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(m.sql); err != nil {
		return err
	}
	if _, err := tx.Exec(
		"INSERT INTO schema_migrations (version, name) VALUES (?, ?)",
		m.version, m.name,
	); err != nil {
		return fmt.Errorf("record version: %w", err)
	}
	return tx.Commit()
}

// loadMigrations reads and parses every embedded migration, sorted ascending by version. It rejects malformed filenames and duplicate versions rather than guessing intent.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}

	var migrations []migration
	seen := make(map[int]string)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, name, err := parseMigrationName(e.Name())
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("duplicate migration version %d: %q and %q", version, prev, e.Name())
		}
		seen[version] = e.Name()

		content, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", e.Name(), err)
		}
		migrations = append(migrations, migration{version: version, name: name, sql: string(content)})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})
	return migrations, nil
}

// parseMigrationName extracts the version and name from "NNNN_description.sql".
func parseMigrationName(filename string) (int, string, error) {
	base := strings.TrimSuffix(filename, ".sql")
	prefix, name, found := strings.Cut(base, "_")
	if !found {
		return 0, "", fmt.Errorf("malformed migration filename %q: expected NNNN_name.sql", filename)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, "", fmt.Errorf("malformed migration version in %q: %w", filename, err)
	}
	return version, name, nil
}
