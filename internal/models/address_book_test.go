package models_test

import (
	"context"
	"errors"
	"testing"

	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
)

func TestListAddressBooksManageFlag(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	owner, _ := models.CreateUser(ctx, d, "owner", "o@example.com", "h", models.RoleUser)
	viewer, _ := models.CreateUser(ctx, d, "viewer", "v@example.com", "h", models.RoleUser)
	manager, _ := models.CreateUser(ctx, d, "manager", "m@example.com", "h", models.RoleUser)
	admin, _ := models.CreateUser(ctx, d, "admin", "a@example.com", "h", models.RoleAdmin)

	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	grant := func(u models.User, level string) {
		if _, err := d.ExecWrite(ctx,
			`INSERT INTO address_book_members (address_book_id, user_id, access_level) VALUES (?, ?, ?)`,
			book.ID, u.ID, level); err != nil {
			t.Fatalf("grant %s: %v", level, err)
		}
	}
	grant(viewer, models.AccessViewer)
	grant(manager, models.AccessManager)

	manageFor := func(u models.User) bool {
		items, err := models.ListAddressBooks(ctx, d, u)
		if err != nil {
			t.Fatalf("list for %s: %v", u.Username, err)
		}
		for _, it := range items {
			if it.ID == book.ID {
				return it.Manage
			}
		}
		t.Fatalf("%s cannot see the book", u.Username)
		return false
	}

	if !manageFor(owner) {
		t.Error("owner should manage the book")
	}
	if manageFor(viewer) {
		t.Error("viewer should not manage the book")
	}
	if !manageFor(manager) {
		t.Error("manager should manage the book")
	}
	if !manageFor(admin) {
		t.Error("admin should manage every book")
	}
}

func TestAddressBookColorFromID(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	owner, _ := models.CreateUser(ctx, d, "owner", "o@example.com", "h", models.RoleUser)
	for i := 0; i < 3; i++ {
		b, err := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
		if err != nil {
			t.Fatalf("create book: %v", err)
		}
		want := int(b.ID % models.BookColorCount)
		if got := b.Color(); got != want {
			t.Errorf("book id %d: Color() = %d, want %d", b.ID, got, want)
		}
		if b.Color() < 0 || b.Color() >= models.BookColorCount {
			t.Errorf("book id %d: Color() = %d out of range", b.ID, b.Color())
		}
	}
}

func TestCreateAddressBookGrantsOwnerManager(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	owner, err := models.CreateUser(ctx, d, "owner", "owner@example.com", "h", models.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	book, err := models.CreateAddressBook(ctx, d, owner.ID, "Friends", "people I like")
	if err != nil {
		t.Fatalf("CreateAddressBook: %v", err)
	}
	if book.PublicID == "" || book.ID == 0 {
		t.Error("book missing id/public_id")
	}

	level, found, err := models.GetGrant(ctx, d, owner.ID, book.ID)
	if err != nil {
		t.Fatalf("GetGrant: %v", err)
	}
	if !found || level != models.AccessManager {
		t.Errorf("owner grant = (%q, %v), want manager", level, found)
	}
}

func TestCreateAddressBookRejectsEmptyName(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	owner, _ := models.CreateUser(ctx, d, "owner", "o@example.com", "h", models.RoleUser)
	if _, err := models.CreateAddressBook(ctx, d, owner.ID, "   ", ""); err == nil {
		t.Error("expected error for empty name")
	}
}

func TestGetAddressBookByPublicID(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	owner, _ := models.CreateUser(ctx, d, "owner", "o@example.com", "h", models.RoleUser)
	created, _ := models.CreateAddressBook(ctx, d, owner.ID, "Work", "")

	got, err := models.GetAddressBookByPublicID(ctx, d, created.PublicID)
	if err != nil {
		t.Fatalf("GetAddressBookByPublicID: %v", err)
	}
	if got.ID != created.ID || got.Name != "Work" {
		t.Errorf("got %+v, want id %d name Work", got, created.ID)
	}

	if _, err := models.GetAddressBookByPublicID(ctx, d, "missing"); !errors.Is(err, models.ErrAddressBookNotFound) {
		t.Errorf("missing book err = %v, want ErrAddressBookNotFound", err)
	}
}

func TestUpdateAndDeleteAddressBook(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	owner, _ := models.CreateUser(ctx, d, "owner", "o@example.com", "h", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Old", "old desc")

	if err := models.UpdateAddressBook(ctx, d, book.ID, "New", ""); err != nil {
		t.Fatalf("UpdateAddressBook: %v", err)
	}
	got, _ := models.GetAddressBookByPublicID(ctx, d, book.PublicID)
	if got.Name != "New" || got.Description != "" {
		t.Errorf("after update got %+v, want name New empty desc", got)
	}

	if err := models.DeleteAddressBook(ctx, d, book.ID); err != nil {
		t.Fatalf("DeleteAddressBook: %v", err)
	}
	if _, err := models.GetAddressBookByPublicID(ctx, d, book.PublicID); !errors.Is(err, models.ErrAddressBookNotFound) {
		t.Errorf("after delete err = %v, want ErrAddressBookNotFound", err)
	}
}

func TestListAddressBooksScoping(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	admin, _ := models.CreateUser(ctx, d, "admin", "a@example.com", "h", models.RoleAdmin)
	alice, _ := models.CreateUser(ctx, d, "alice", "al@example.com", "h", models.RoleUser)
	bob, _ := models.CreateUser(ctx, d, "bob", "b@example.com", "h", models.RoleUser)

	aliceBook, _ := models.CreateAddressBook(ctx, d, alice.ID, "Alice Book", "")
	_, _ = models.CreateAddressBook(ctx, d, bob.ID, "Bob Book", "")

	// Alice sees only her own book.
	aliceList, err := models.ListAddressBooks(ctx, d, alice)
	if err != nil {
		t.Fatalf("list for alice: %v", err)
	}
	if len(aliceList) != 1 || aliceList[0].ID != aliceBook.ID {
		t.Errorf("alice sees %d books, want only her own", len(aliceList))
	}

	// Admin sees both.
	adminList, err := models.ListAddressBooks(ctx, d, admin)
	if err != nil {
		t.Fatalf("list for admin: %v", err)
	}
	if len(adminList) != 2 {
		t.Errorf("admin sees %d books, want 2", len(adminList))
	}

	// Grant bob viewer on alice's book; bob now sees it.
	if _, err := d.ExecWrite(ctx,
		`INSERT INTO address_book_members (address_book_id, user_id, access_level) VALUES (?, ?, ?)`,
		aliceBook.ID, bob.ID, models.AccessViewer); err != nil {
		t.Fatalf("grant bob: %v", err)
	}
	bobList, _ := models.ListAddressBooks(ctx, d, bob)
	if len(bobList) != 2 {
		t.Errorf("bob sees %d books, want 2 (his own + granted)", len(bobList))
	}
}
