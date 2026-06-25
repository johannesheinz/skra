package web

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
)

// TestServerMaintenancePrunes verifies one maintenance pass removes an expired
// session and a stale import upload.
func TestServerMaintenancePrunes(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	user := seedUser(t, d, "u", "pw", models.RoleUser)
	book, err := models.CreateAddressBook(ctx, d, user.ID, "B", "")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}

	if _, err := d.ExecWrite(ctx,
		`INSERT INTO sessions (id, user_id, expires_at) VALUES ('expired', ?, datetime('now','-1 hour'))`, user.ID); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := d.ExecWrite(ctx,
		`INSERT INTO import_uploads (token, book_id, user_id, format, bytes, created_at)
		 VALUES ('stale', ?, ?, 'vcard', x'00', datetime('now','-2 hours'))`, book.ID, user.ID); err != nil {
		t.Fatalf("seed upload: %v", err)
	}

	s := &Server{db: d, sessions: auth.NewSessionStore(d), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.maintain(ctx)

	var sessions, uploads int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM import_uploads`).Scan(&uploads); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 || uploads != 0 {
		t.Errorf("after maintenance: sessions=%d uploads=%d, want 0/0", sessions, uploads)
	}
}
