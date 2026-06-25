package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
)

func TestBookExportVCardAndCSV(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()

	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Friends", "")
	jane, _ := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Jane Doe", Org: "Acme", PrimaryEmail: "jane@acme.test"})
	_, _ = models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "=cmd|formula"})
	if err := models.SetContactPhoto(ctx, d, jane.ID, []byte{0xFF, 0xD8, 0xFF, 0xD9}); err != nil {
		t.Fatalf("set photo: %v", err)
	}
	session := sessionCookieFor(t, d, owner.ID)

	// vCard export.
	vcf, _, _ := authedGet(t, router, session, "/books/"+book.PublicID+"/export.vcf")
	if vcf.Code != http.StatusOK {
		t.Fatalf("export.vcf = %d, want 200", vcf.Code)
	}
	if ct := vcf.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/vcard") {
		t.Errorf("vcf Content-Type = %q", ct)
	}
	if cd := vcf.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="Friends.vcf"`) {
		t.Errorf("vcf Content-Disposition = %q", cd)
	}
	vcfBody := vcf.Body.String()
	if n := strings.Count(vcfBody, "BEGIN:VCARD"); n != 2 {
		t.Errorf("vcf has %d cards, want 2", n)
	}
	if !strings.Contains(vcfBody, "PHOTO:data:image/jpeg;base64") {
		t.Error("vcf missing embedded photo for the contact that has one")
	}

	// CSV export with injection sanitization.
	csv, _, _ := authedGet(t, router, session, "/books/"+book.PublicID+"/export.csv")
	if csv.Code != http.StatusOK {
		t.Fatalf("export.csv = %d, want 200", csv.Code)
	}
	if ct := csv.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("csv Content-Type = %q", ct)
	}
	csvBody := csv.Body.String()
	if !strings.Contains(csvBody, "Full Name,Organization,Email,Phone") {
		t.Error("csv missing header")
	}
	if !strings.Contains(csvBody, "'=cmd|formula") {
		t.Errorf("csv did not sanitize formula injection:\n%s", csvBody)
	}
}

func TestContactExportVCard(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	contact, _ := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Jane Doe"})

	rec, _, _ := authedGet(t, router, sessionCookieFor(t, d, owner.ID), "/contacts/"+contact.PublicID+"/export.vcf")
	if rec.Code != http.StatusOK {
		t.Fatalf("contact export.vcf = %d, want 200", rec.Code)
	}
	if n := strings.Count(rec.Body.String(), "BEGIN:VCARD"); n != 1 {
		t.Errorf("contact vcf has %d cards, want 1", n)
	}
}

func TestExportAuthorization(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	viewer := seedUser(t, d, "viewer", "pw", models.RoleUser)
	stranger := seedUser(t, d, "stranger", "pw", models.RoleUser)

	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	if _, err := d.ExecWrite(ctx,
		`INSERT INTO address_book_members (address_book_id, user_id, access_level) VALUES (?, ?, ?)`,
		book.ID, viewer.ID, models.AccessViewer); err != nil {
		t.Fatalf("grant viewer: %v", err)
	}

	// Viewer may export (read includes download/export).
	if rec, _, _ := authedGet(t, router, sessionCookieFor(t, d, viewer.ID), "/books/"+book.PublicID+"/export.vcf"); rec.Code != http.StatusOK {
		t.Errorf("viewer export = %d, want 200", rec.Code)
	}
	// Stranger cannot see the book → 404.
	if rec, _, _ := authedGet(t, router, sessionCookieFor(t, d, stranger.ID), "/books/"+book.PublicID+"/export.csv"); rec.Code != http.StatusNotFound {
		t.Errorf("stranger export = %d, want 404", rec.Code)
	}
}
