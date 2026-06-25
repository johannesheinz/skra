package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/sharing"
	"github.com/johannesheinz/skra/internal/testutil"
)

func TestContactShareServeRevokeAuthz(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	stranger := seedUser(t, d, "stranger", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	contact, _ := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Jane Doe"})
	link, _ := models.CreateShareLink(ctx, d, models.NewShareLinkParams{
		Mode: sharing.ModePublicLong, Scope: sharing.ScopeContact, TargetID: contact.ID, CreatedBy: owner.ID,
	})
	base := "/s/" + link.Token

	// Public fetch returns the contact.
	if rec := get(router, base); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Jane Doe") {
		t.Fatalf("contact share = %d, missing contact", rec.Code)
	}
	// vCard export within the contact share works.
	if rec := get(router, base+"/export.vcf"); rec.Code != http.StatusOK {
		t.Errorf("contact share export = %d, want 200", rec.Code)
	}

	// Revoked → 404.
	_ = models.RevokeShareLink(ctx, d, link.ID)
	if rec := get(router, base); rec.Code != http.StatusNotFound {
		t.Errorf("revoked contact share = %d, want 404", rec.Code)
	}

	// Managing a contact's shares requires write: stranger gets 404 (not visible).
	sharesURL := "/contacts/" + contact.PublicID + "/shares"
	if rec, _, _ := authedGet(t, router, sessionCookieFor(t, d, stranger.ID), sharesURL); rec.Code != http.StatusNotFound {
		t.Errorf("stranger contact-shares = %d, want 404", rec.Code)
	}
}

func TestContactGatedShareLocksOut(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	contact, _ := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Jane Doe"})
	secretHash, _ := auth.HashPassword("hunter2")
	link, _ := models.CreateShareLink(ctx, d, models.NewShareLinkParams{
		Mode: sharing.ModeGated, Scope: sharing.ScopeContact, TargetID: contact.ID, SecretHash: secretHash, CreatedBy: owner.ID,
	})
	for i := 0; i < sharing.GateMaxFailures; i++ {
		if err := models.IncrementShareFailure(ctx, d, link.ID); err != nil {
			t.Fatalf("increment failure: %v", err)
		}
	}
	if rec := get(router, "/s/"+link.Token); rec.Code != http.StatusNotFound {
		t.Errorf("locked contact share = %d, want 404", rec.Code)
	}
}

func TestBookDeleteAuthorizationAndCascade(t *testing.T) {
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
	contact, _ := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Jane Doe"})
	deleteURL := "/books/" + book.PublicID + "/delete"

	// Stranger cannot even see the book (404 hides it).
	if rec, _, _ := authedGet(t, router, sessionCookieFor(t, d, stranger.ID), "/books/"+book.PublicID); rec.Code != http.StatusNotFound {
		t.Errorf("stranger book view = %d, want 404", rec.Code)
	}
	// Viewer can see it (read) but delete (write) is forbidden.
	viewerSession := sessionCookieFor(t, d, viewer.ID)
	_, vtok, vcsrf := authedGet(t, router, viewerSession, "/books/"+book.PublicID)
	if del := authedPostForm(router, viewerSession, vcsrf, deleteURL, url.Values{auth.CSRFFormField: {vtok}}); del.Code != http.StatusForbidden {
		t.Errorf("viewer delete = %d, want 403", del.Code)
	}

	// Owner deletes → 303, and the book + its contacts are gone (cascade).
	ownerSession := sessionCookieFor(t, d, owner.ID)
	_, tok, csrf := authedGet(t, router, ownerSession, "/books/"+book.PublicID)
	del := authedPostForm(router, ownerSession, csrf, deleteURL, url.Values{auth.CSRFFormField: {tok}})
	if del.Code != http.StatusSeeOther {
		t.Fatalf("owner delete = %d, want 303", del.Code)
	}
	if _, err := models.GetAddressBookByID(ctx, d, book.ID); err == nil {
		t.Error("book still exists after delete")
	}
	if _, err := models.GetContactByID(ctx, d, contact.ID); !errors.Is(err, models.ErrContactNotFound) {
		t.Errorf("contact not cascaded: err=%v", err)
	}
}

func TestAdminUserPasswordReset(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	admin := seedUser(t, d, "admin", "pw", models.RoleAdmin)
	target := seedUser(t, d, "target", "oldpassword", models.RoleUser)
	nonAdmin := seedUser(t, d, "regular", "pw", models.RoleUser)
	resetURL := "/admin/users/" + target.PublicID + "/password"

	// Admin resets the target's password.
	adminSession := sessionCookieFor(t, d, admin.ID)
	_, tok, csrf := authedGet(t, router, adminSession, "/admin/users/"+target.PublicID+"/edit")
	rec := authedPostForm(router, adminSession, csrf, resetURL,
		url.Values{auth.CSRFFormField: {tok}, "password": {"brandnewpassword"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin password reset = %d, want 200", rec.Code)
	}
	reloaded, err := models.GetUserByID(ctx, d, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.VerifyPassword(reloaded.PasswordHash, "brandnewpassword"); err != nil {
		t.Errorf("new password does not verify: %v", err)
	}

	// A non-admin gets 404 (the admin surface is hidden, not 403).
	nonAdminSession := sessionCookieFor(t, d, nonAdmin.ID)
	if rec, _, _ := authedGet(t, router, nonAdminSession, "/admin/users/"+target.PublicID+"/edit"); rec.Code != http.StatusNotFound {
		t.Errorf("non-admin edit page = %d, want 404", rec.Code)
	}
}
