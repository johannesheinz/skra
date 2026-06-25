package models_test

import (
	"context"
	"testing"

	"github.com/johannesheinz/skra/internal/db"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/sharing"
	"github.com/johannesheinz/skra/internal/testutil"
)

func countShareLinks(t *testing.T, d *db.DB, ctx context.Context) int {
	t.Helper()
	var n int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM share_links`).Scan(&n); err != nil {
		t.Fatalf("count share_links: %v", err)
	}
	return n
}

func TestDeleteBookPurgesShareLinks(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	owner, _ := models.CreateUser(ctx, d, "o", "o@e.test", "h", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	contact, _ := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Jane"})

	if _, err := models.CreateShareLink(ctx, d, models.NewShareLinkParams{
		Mode: sharing.ModePublicLong, Scope: sharing.ScopeBook, TargetID: book.ID, CreatedBy: owner.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := models.CreateShareLink(ctx, d, models.NewShareLinkParams{
		Mode: sharing.ModePublicLong, Scope: sharing.ScopeContact, TargetID: contact.ID, CreatedBy: owner.ID}); err != nil {
		t.Fatal(err)
	}
	if got := countShareLinks(t, d, ctx); got != 2 {
		t.Fatalf("share links before delete = %d, want 2", got)
	}

	if err := models.DeleteAddressBook(ctx, d, book.ID); err != nil {
		t.Fatalf("delete book: %v", err)
	}
	if got := countShareLinks(t, d, ctx); got != 0 {
		t.Errorf("orphaned share links after book delete = %d, want 0", got)
	}
}

func TestDeleteContactPurgesShareLinks(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	owner, _ := models.CreateUser(ctx, d, "o", "o@e.test", "h", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	contact, _ := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Jane"})
	if _, err := models.CreateShareLink(ctx, d, models.NewShareLinkParams{
		Mode: sharing.ModePublicLong, Scope: sharing.ScopeContact, TargetID: contact.ID, CreatedBy: owner.ID}); err != nil {
		t.Fatal(err)
	}

	if err := models.DeleteContact(ctx, d, contact.ID); err != nil {
		t.Fatalf("delete contact: %v", err)
	}
	if got := countShareLinks(t, d, ctx); got != 0 {
		t.Errorf("orphaned share links after contact delete = %d, want 0", got)
	}
}
