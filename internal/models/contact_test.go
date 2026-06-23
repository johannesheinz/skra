package models_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
)

func TestCreateContactHybridWritePath(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	owner, _ := models.CreateUser(ctx, d, "owner", "o@example.com", "h", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")

	c, err := models.CreateContact(ctx, d, book.ID, models.ContactInput{
		FullName: "Jane Doe", Org: "Acme", PrimaryEmail: "jane@acme.test", PrimaryPhone: "+123",
	})
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	if c.PublicID == "" || c.UID == "" || c.ETag == "" {
		t.Error("contact missing generated public_id/uid/etag")
	}

	// vcard_raw must have been written and contain the structured values.
	var vcardRaw string
	if err := d.QueryRowContext(ctx, "SELECT vcard_raw FROM contacts WHERE id = ?", c.ID).Scan(&vcardRaw); err != nil {
		t.Fatalf("read vcard_raw: %v", err)
	}
	for _, want := range []string{"BEGIN:VCARD", "VERSION:4.0", "Jane Doe", "jane@acme.test", "END:VCARD"} {
		if !strings.Contains(vcardRaw, want) {
			t.Errorf("vcard_raw missing %q:\n%s", want, vcardRaw)
		}
	}
}

func TestCreateContactRejectsEmptyName(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	owner, _ := models.CreateUser(ctx, d, "o", "o@example.com", "h", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	if _, err := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "  "}); err == nil {
		t.Error("expected error for empty full name")
	}
}

func TestUpdateContactBumpsETagPreservesUID(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	owner, _ := models.CreateUser(ctx, d, "o", "o@example.com", "h", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	c, _ := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Jane"})

	if err := models.UpdateContact(ctx, d, c, models.ContactInput{FullName: "Jane Smith", Org: "NewCo"}); err != nil {
		t.Fatalf("UpdateContact: %v", err)
	}
	updated, err := models.GetContactByPublicID(ctx, d, c.PublicID)
	if err != nil {
		t.Fatalf("GetContactByPublicID: %v", err)
	}
	if updated.FullName != "Jane Smith" || updated.Org != "NewCo" {
		t.Errorf("update did not persist: %+v", updated)
	}
	if updated.UID != c.UID {
		t.Errorf("uid changed: %q -> %q", c.UID, updated.UID)
	}
	if updated.ETag == c.ETag {
		t.Error("etag was not bumped on update")
	}
}

func TestDeleteContact(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	owner, _ := models.CreateUser(ctx, d, "o", "o@example.com", "h", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	c, _ := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Jane"})

	if err := models.DeleteContact(ctx, d, c.ID); err != nil {
		t.Fatalf("DeleteContact: %v", err)
	}
	if _, err := models.GetContactByPublicID(ctx, d, c.PublicID); !errors.Is(err, models.ErrContactNotFound) {
		t.Errorf("after delete err = %v, want ErrContactNotFound", err)
	}
}

func TestListContactsSearchAndPagination(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	owner, _ := models.CreateUser(ctx, d, "o", "o@example.com", "h", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")

	names := []string{"Alice Anderson", "Bob Brown", "Carol Clark", "Alice Archer"}
	for _, n := range names {
		if _, err := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: n}); err != nil {
			t.Fatalf("seed %q: %v", n, err)
		}
	}

	// Search.
	results, total, err := models.ListContacts(ctx, d, book.ID, "alice", "", false, 10, 0)
	if err != nil {
		t.Fatalf("ListContacts search: %v", err)
	}
	if total != 2 || len(results) != 2 {
		t.Errorf("search 'alice' total=%d len=%d, want 2/2", total, len(results))
	}

	// Pagination: page size 2 over 4 contacts.
	page1, total, err := models.ListContacts(ctx, d, book.ID, "", "", false, 2, 0)
	if err != nil {
		t.Fatalf("ListContacts page1: %v", err)
	}
	if total != 4 || len(page1) != 2 {
		t.Errorf("page1 total=%d len=%d, want 4/2", total, len(page1))
	}
	page2, _, _ := models.ListContacts(ctx, d, book.ID, "", "", false, 2, 2)
	if len(page2) != 2 {
		t.Errorf("page2 len=%d, want 2", len(page2))
	}
	if page1[0].PublicID == page2[0].PublicID {
		t.Error("pages overlap; ordering/offset wrong")
	}
}
