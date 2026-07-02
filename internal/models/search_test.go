package models_test

import (
	"context"
	"testing"

	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
)

func names(results []models.SearchResult) map[string]string {
	m := make(map[string]string, len(results))
	for _, r := range results {
		m[r.FullName] = r.BookName
	}
	return m
}

func TestSearchContactsForUserScopingAndNullOrg(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()

	owner, _ := models.CreateUser(ctx, d, "owner", "owner@example.com", "h", models.RoleUser)
	other, _ := models.CreateUser(ctx, d, "other", "other@example.com", "h", models.RoleUser)
	member, _ := models.CreateUser(ctx, d, "member", "member@example.com", "h", models.RoleUser)
	admin, _ := models.CreateUser(ctx, d, "admin", "admin@example.com", "h", models.RoleAdmin)

	ownerBook, _ := models.CreateAddressBook(ctx, d, owner.ID, "Owner Book", "")
	otherBook, _ := models.CreateAddressBook(ctx, d, other.ID, "Other Book", "")

	// A contact with an org and one without (no org exercises the NULL->"" coalesce).
	if _, err := models.CreateContact(ctx, d, ownerBook.ID, models.ContactInput{FullName: "Ada Lovelace", Org: "Acme"}); err != nil {
		t.Fatal(err)
	}
	if _, err := models.CreateContact(ctx, d, ownerBook.ID, models.ContactInput{FullName: "Ada Noorg"}); err != nil {
		t.Fatal(err)
	}
	if _, err := models.CreateContact(ctx, d, otherBook.ID, models.ContactInput{FullName: "Ada Turing", Org: "Hooli"}); err != nil {
		t.Fatal(err)
	}

	// member gets viewer access to the owner's book.
	if _, err := d.ExecWrite(ctx,
		`INSERT INTO address_book_members (address_book_id, user_id, access_level) VALUES (?, ?, ?)`,
		ownerBook.ID, member.ID, models.AccessViewer); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	// owner sees only their own book's hits (including the NULL-org contact), never the other user's.
	got, err := models.SearchContactsForUser(ctx, d, owner, "Ada", 50)
	if err != nil {
		t.Fatalf("owner search: %v", err)
	}
	n := names(got)
	if len(n) != 2 {
		t.Fatalf("owner results = %v, want 2 hits in Owner Book", n)
	}
	if n["Ada Lovelace"] != "Owner Book" {
		t.Errorf("Ada Lovelace book = %q, want Owner Book", n["Ada Lovelace"])
	}
	if _, ok := n["Ada Noorg"]; !ok {
		t.Error("owner should see the NULL-org contact Ada Noorg")
	}
	if _, leaked := n["Ada Turing"]; leaked {
		t.Error("owner must not see the other user's book")
	}

	// other sees only their book.
	got, _ = models.SearchContactsForUser(ctx, d, other, "Ada", 50)
	if n := names(got); len(n) != 1 || n["Ada Turing"] != "Other Book" {
		t.Errorf("other results = %v, want only Ada Turing", n)
	}

	// member sees the granted book.
	got, _ = models.SearchContactsForUser(ctx, d, member, "Lovelace", 50)
	if n := names(got); len(n) != 1 || n["Ada Lovelace"] != "Owner Book" {
		t.Errorf("member results = %v, want Ada Lovelace via grant", n)
	}

	// admin sees every book.
	got, _ = models.SearchContactsForUser(ctx, d, admin, "Ada", 50)
	if n := names(got); len(n) != 3 {
		t.Errorf("admin results = %v, want all 3 across books", n)
	}

	// empty query touches nothing.
	if got, err := models.SearchContactsForUser(ctx, d, admin, "   ", 50); err != nil || got != nil {
		t.Errorf("empty query = (%v, %v), want (nil, nil)", got, err)
	}
}
