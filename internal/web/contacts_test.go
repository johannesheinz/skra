package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
)

func TestContactCRUDFlow(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()

	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, err := models.CreateAddressBook(ctx, d, owner.ID, "Friends", "")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	session := sessionCookieFor(t, d, owner.ID)
	bookURL := "/books/" + book.PublicID

	// Book detail starts with no contacts.
	if rec, _, _ := authedGet(t, router, session, bookURL); !strings.Contains(rec.Body.String(), "No contacts yet") {
		t.Fatal("expected empty contacts notice")
	}

	// Create a contact.
	_, token, csrf := authedGet(t, router, session, bookURL+"/contacts/new")
	createRec := authedPostForm(router, session, csrf, bookURL+"/contacts", url.Values{
		auth.CSRFFormField: {token},
		"given_name":       {"Jane"},
		"family_name":      {"Doe"},
		"org":              {"Acme"},
		"email_type":       {"work"},
		"email_value":      {"jane@acme.test"},
	})
	if createRec.Code != http.StatusSeeOther {
		t.Fatalf("create contact = %d, want 303", createRec.Code)
	}
	contactURL := createRec.Header().Get("Location")
	if !strings.HasPrefix(contactURL, "/contacts/") {
		t.Fatalf("create redirect = %q", contactURL)
	}

	// Detail shows the contact and manage controls for the manager.
	showRec, _, _ := authedGet(t, router, session, contactURL)
	body := showRec.Body.String()
	if showRec.Code != http.StatusOK || !strings.Contains(body, "Jane Doe") || !strings.Contains(body, "jane@acme.test") {
		t.Fatalf("contact detail missing data (status %d)", showRec.Code)
	}
	if !strings.Contains(body, "/edit") {
		t.Error("manager should see edit control")
	}

	// It appears in the book listing and is searchable.
	listRec, _, _ := authedGet(t, router, session, bookURL)
	if !strings.Contains(listRec.Body.String(), "Jane Doe") {
		t.Error("contact not listed on book detail")
	}
	searchHit, _, _ := authedGet(t, router, session, bookURL+"?q=acme")
	if !strings.Contains(searchHit.Body.String(), "Jane Doe") {
		t.Error("search by org did not find contact")
	}
	searchMiss, _, _ := authedGet(t, router, session, bookURL+"?q=zzzzz")
	if !strings.Contains(searchMiss.Body.String(), "No contacts match") {
		t.Error("search miss did not show empty notice")
	}

	// Edit.
	publicID := strings.TrimPrefix(contactURL, "/contacts/")
	_, editToken, editCSRF := authedGet(t, router, session, "/contacts/"+publicID+"/edit")
	editRec := authedPostForm(router, session, editCSRF, "/contacts/"+publicID+"/edit", url.Values{
		auth.CSRFFormField: {editToken},
		"given_name":       {"Jane"},
		"family_name":      {"Smith"},
	})
	if editRec.Code != http.StatusSeeOther {
		t.Fatalf("edit contact = %d, want 303", editRec.Code)
	}
	if after, _, _ := authedGet(t, router, session, contactURL); !strings.Contains(after.Body.String(), "Jane Smith") {
		t.Error("edit did not persist")
	}

	// Delete returns to the book.
	_, delToken, delCSRF := authedGet(t, router, session, contactURL)
	delRec := authedPostForm(router, session, delCSRF, "/contacts/"+publicID+"/delete",
		url.Values{auth.CSRFFormField: {delToken}})
	if delRec.Code != http.StatusSeeOther || delRec.Header().Get("Location") != bookURL {
		t.Fatalf("delete contact = %d loc %q, want 303 %q", delRec.Code, delRec.Header().Get("Location"), bookURL)
	}
}

func TestContactAuthorization(t *testing.T) {
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
	contact, _ := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Jane"})
	contactURL := "/contacts/" + contact.PublicID

	// Stranger: not visible → 404.
	if rec, _, _ := authedGet(t, router, sessionCookieFor(t, d, stranger.ID), contactURL); rec.Code != http.StatusNotFound {
		t.Errorf("stranger contact = %d, want 404", rec.Code)
	}

	// Viewer: can read, cannot edit (403), and sees no manage controls.
	viewerSession := sessionCookieFor(t, d, viewer.ID)
	show, _, _ := authedGet(t, router, viewerSession, contactURL)
	if show.Code != http.StatusOK || strings.Contains(show.Body.String(), "/delete") {
		t.Errorf("viewer show = %d or sees manage controls", show.Code)
	}
	if edit, _, _ := authedGet(t, router, viewerSession, contactURL+"/edit"); edit.Code != http.StatusForbidden {
		t.Errorf("viewer edit = %d, want 403", edit.Code)
	}
}
