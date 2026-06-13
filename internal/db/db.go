// Package db opens the single SQLite datastore, applies the connection pragmas
// the spec mandates, and runs embedded migrations. SQLite is the only datastore
// for Skra; photos live as BLOBs in it too.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"

	_ "modernc.org/sqlite"
)

// connectionPragmas are applied to every connection in the pool via the DSN.
// journal_mode is persisted in the database header; the others are per-connection
// and must be re-applied on each connect, which the DSN guarantees.
//
// auto_vacuum is deliberately NOT here: it is a persistent header setting that
// cannot be changed once the file is in WAL mode, so a per-connection pragma
// would silently fail to persist. It is set explicitly on a fresh database in
// initAutoVacuum before any schema exists.
var connectionPragmas = []string{
	"journal_mode(WAL)",   // concurrent reads with a single writer
	"busy_timeout(5000)",  // wait rather than error on brief locks
	"foreign_keys(ON)",    // enforce FKs (off by default in SQLite)
	"synchronous(NORMAL)", // safe and fast under WAL
}

// Open opens (creating if absent) the SQLite database at path, applies pragmas,
// sets INCREMENTAL auto_vacuum on a brand-new database, and runs all pending
// migrations. The returned *sql.DB is ready for use; the caller owns closing it.
func Open(path string) (*sql.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("db path must not be empty")
	}

	fresh, err := isFreshDatabase(path)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if fresh {
		if err := initAutoVacuum(db); err != nil {
			db.Close()
			return nil, err
		}
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return db, nil
}

// initAutoVacuum sets INCREMENTAL auto_vacuum and forces it into the database
// header with a VACUUM. Both statements must run on the same connection, so a
// single connection is pinned for the operation. VACUUM persists the mode even
// though the DSN has already switched the file to WAL.
func initAutoVacuum(db *sql.DB) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("pin connection for auto_vacuum: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "PRAGMA auto_vacuum = INCREMENTAL"); err != nil {
		return fmt.Errorf("set auto_vacuum: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("vacuum to persist auto_vacuum: %w", err)
	}
	return nil
}

// isFreshDatabase reports whether path refers to a not-yet-initialized database
// (absent or zero-length file), which is when auto_vacuum is established.
func isFreshDatabase(path string) (bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat db path: %w", err)
	}
	return info.Size() == 0, nil
}

// dsn builds a modernc.org/sqlite DSN that applies connectionPragmas on connect.
func dsn(path string) string {
	q := url.Values{}
	for _, p := range connectionPragmas {
		q.Add("_pragma", p)
	}
	return "file:" + path + "?" + q.Encode()
}
