package models_test

import (
	"context"
	"testing"

	"github.com/johannesheinz/skra/internal/db"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
)

func seedBook(t *testing.T, d *db.DB, ownerID int64, publicID string) int64 {
	t.Helper()
	res, err := d.ExecWrite(context.Background(),
		`INSERT INTO address_books (public_id, name, owner_id) VALUES (?, ?, ?)`,
		publicID, "Book "+publicID, ownerID)
	if err != nil {
		t.Fatalf("seed book: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("book id: %v", err)
	}
	return id
}

func TestGetGrant(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()

	owner, err := models.CreateUser(ctx, d, "owner", "owner@example.com", "hash", models.RoleUser)
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	member, err := models.CreateUser(ctx, d, "member", "member@example.com", "hash", models.RoleUser)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	bookID := seedBook(t, d, owner.ID, "book1")

	if _, err := d.ExecWrite(ctx,
		`INSERT INTO address_book_members (address_book_id, user_id, access_level) VALUES (?, ?, ?)`,
		bookID, member.ID, models.AccessViewer); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	level, found, err := models.GetGrant(ctx, d, member.ID, bookID)
	if err != nil {
		t.Fatalf("GetGrant: %v", err)
	}
	if !found || level != models.AccessViewer {
		t.Errorf("GetGrant = (%q, %v), want (%q, true)", level, found, models.AccessViewer)
	}

	// A user with no grant on the book.
	_, found, err = models.GetGrant(ctx, d, owner.ID, bookID)
	if err != nil {
		t.Fatalf("GetGrant (no grant): %v", err)
	}
	if found {
		t.Error("owner has no explicit grant row; found should be false")
	}
}
