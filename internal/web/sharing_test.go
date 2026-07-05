package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/sharing"
	"github.com/johannesheinz/skra/internal/testutil"
)

func get(router http.Handler, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func postForm(router http.Handler, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestPublicLongShareServing(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Friends", "")
	contact, _ := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Jane Doe"})
	link, _ := models.CreateShareLink(ctx, d, models.NewShareLinkParams{
		Mode: sharing.ModePublicLong, Scope: sharing.ScopeBook, TargetID: book.ID, CreatedBy: owner.ID,
	})
	base := "/s/" + link.Token

	// Anonymous (no session) gets the directory.
	dir := get(router, base)
	if dir.Code != http.StatusOK {
		t.Fatalf("anonymous directory = %d, want 200", dir.Code)
	}
	if !strings.Contains(dir.Body.String(), "Friends") || !strings.Contains(dir.Body.String(), "Jane Doe") {
		t.Error("directory missing book/contact")
	}
	// Contact within the share.
	c := get(router, base+"/c/"+contact.PublicID)
	if c.Code != http.StatusOK || !strings.Contains(c.Body.String(), "Jane Doe") {
		t.Errorf("share contact = %d", c.Code)
	}
	// Export within the share.
	vcf := get(router, base+"/export.vcf")
	if vcf.Code != http.StatusOK || !strings.HasPrefix(vcf.Header().Get("Content-Type"), "text/vcard") {
		t.Errorf("share export.vcf = %d ct=%q", vcf.Code, vcf.Header().Get("Content-Type"))
	}
}

func TestGatedShareFlow(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Secret Book", "")
	_, _ = models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Jane Doe"})
	secretHash, _ := auth.HashPassword("hunter2")
	link, _ := models.CreateShareLink(ctx, d, models.NewShareLinkParams{
		Mode: sharing.ModeGated, Scope: sharing.ScopeBook, TargetID: book.ID, SecretHash: secretHash, CreatedBy: owner.ID,
	})
	base := "/s/" + link.Token

	// Without a passed gate, the entry shows the gate, not the contacts.
	gate := get(router, base)
	if gate.Code != http.StatusOK || !strings.Contains(gate.Body.String(), "Protected share") {
		t.Fatalf("gate page = %d", gate.Code)
	}
	if strings.Contains(gate.Body.String(), "Jane Doe") {
		t.Error("gated contents leaked before passing the gate")
	}
	token := extractCSRF(t, gate.Body.String())
	csrf := cookieByName(gate.Result().Cookies(), auth.CSRFCookieName)

	// Wrong secret → 401, still gated.
	bad := postForm(router, base+"/gate", url.Values{auth.CSRFFormField: {token}, "secret": {"wrong"}}, csrf)
	if bad.Code != http.StatusUnauthorized {
		t.Errorf("wrong secret = %d, want 401", bad.Code)
	}

	// Correct secret → 303 with a gate cookie; then the directory is viewable.
	ok := postForm(router, base+"/gate", url.Values{auth.CSRFFormField: {token}, "secret": {"hunter2"}}, csrf)
	if ok.Code != http.StatusSeeOther {
		t.Fatalf("correct secret = %d, want 303", ok.Code)
	}
	gateCookie := cookieByName(ok.Result().Cookies(), sharing.GateCookieName)
	if gateCookie == nil {
		t.Fatal("no gate cookie set on success")
	}
	viewed := get(router, base, gateCookie)
	if viewed.Code != http.StatusOK || !strings.Contains(viewed.Body.String(), "Jane Doe") {
		t.Errorf("gated directory after passing = %d", viewed.Code)
	}
}

func TestGatedShareLocksAfterTooManyFailures(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	secretHash, _ := auth.HashPassword("hunter2")
	link, _ := models.CreateShareLink(ctx, d, models.NewShareLinkParams{
		Mode: sharing.ModeGated, Scope: sharing.ScopeBook, TargetID: book.ID, SecretHash: secretHash, CreatedBy: owner.ID,
	})

	for i := 0; i < sharing.GateMaxFailures; i++ {
		if err := models.IncrementShareFailure(ctx, d, link.ID); err != nil {
			t.Fatalf("increment failure: %v", err)
		}
	}
	if rec := get(router, "/s/"+link.Token); rec.Code != http.StatusNotFound {
		t.Errorf("locked share = %d, want 404", rec.Code)
	}
}

func TestAuthenticatedShareRequiresLogin(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	link, _ := models.CreateShareLink(ctx, d, models.NewShareLinkParams{
		Mode: sharing.ModeAuthenticated, Scope: sharing.ScopeBook, TargetID: book.ID, CreatedBy: owner.ID,
	})
	base := "/s/" + link.Token

	if rec := get(router, base); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Errorf("anonymous authenticated-share = %d %q, want 303 /login", rec.Code, rec.Header().Get("Location"))
	}
	if rec := get(router, base, sessionCookieFor(t, d, owner.ID)); rec.Code != http.StatusOK {
		t.Errorf("logged-in authenticated-share = %d, want 200", rec.Code)
	}
}

func TestRevokedShareIs404(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	link, _ := models.CreateShareLink(ctx, d, models.NewShareLinkParams{
		Mode: sharing.ModePublicLong, Scope: sharing.ScopeBook, TargetID: book.ID, CreatedBy: owner.ID,
	})
	_ = models.RevokeShareLink(ctx, d, link.ID)
	if rec := get(router, "/s/"+link.Token); rec.Code != http.StatusNotFound {
		t.Errorf("revoked share = %d, want 404", rec.Code)
	}
	if rec := get(router, "/s/does-not-exist"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown share = %d, want 404", rec.Code)
	}
}

func TestShareManagementCreateValidateRevoke(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	viewer := seedUser(t, d, "viewer", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	if _, err := d.ExecWrite(ctx,
		`INSERT INTO address_book_members (address_book_id, user_id, access_level) VALUES (?, ?, ?)`,
		book.ID, viewer.ID, models.AccessViewer); err != nil {
		t.Fatalf("grant viewer: %v", err)
	}
	ownerSession := sessionCookieFor(t, d, owner.ID)
	sharesURL := "/books/" + book.PublicID + "/shares"

	// Manager sees the management page.
	_, token, csrf := authedGet(t, router, ownerSession, sharesURL)

	// gated without secret → 422.
	bad := authedPostForm(router, ownerSession, csrf, sharesURL,
		url.Values{auth.CSRFFormField: {token}, "mode": {sharing.ModeGated}})
	if bad.Code != http.StatusUnprocessableEntity {
		t.Errorf("gated create without secret = %d, want 422", bad.Code)
	}

	// public_long create → 303, then it is listed with an absolute URL.
	okRec := authedPostForm(router, ownerSession, csrf, sharesURL,
		url.Values{auth.CSRFFormField: {token}, "mode": {sharing.ModePublicLong}})
	if okRec.Code != http.StatusSeeOther {
		t.Fatalf("public_long create = %d, want 303", okRec.Code)
	}
	listRec, _, _ := authedGet(t, router, ownerSession, sharesURL)
	if !strings.Contains(listRec.Body.String(), "https://share.example.test/s/") {
		t.Error("created share not listed with absolute URL")
	}

	// Viewer cannot manage shares (write required) → 403.
	if rec, _, _ := authedGet(t, router, sessionCookieFor(t, d, viewer.ID), sharesURL); rec.Code != http.StatusForbidden {
		t.Errorf("viewer shares page = %d, want 403", rec.Code)
	}

	// Revoke the created link.
	links, _ := models.ListShareLinksForTarget(ctx, d, sharing.ScopeBook, book.ID)
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}
	_, rtoken, rcsrf := authedGet(t, router, ownerSession, sharesURL)
	revokeURL := sharesURL + "/" + strconv.FormatInt(links[0].ID, 10) + "/revoke"
	rev := authedPostForm(router, ownerSession, rcsrf, revokeURL, url.Values{auth.CSRFFormField: {rtoken}})
	if rev.Code != http.StatusSeeOther {
		t.Fatalf("revoke = %d, want 303", rev.Code)
	}
	if got, _ := models.GetShareLinkByToken(ctx, d, links[0].Token); !got.Revoked {
		t.Error("link not revoked")
	}
}

func TestSharePhotoServedOnFinalUse(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Friends", "")
	contact, _ := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Jane Doe"})
	if err := models.SetContactPhoto(ctx, d, contact.ID, []byte{0xFF, 0xD8, 0xFF, 0xD9}); err != nil {
		t.Fatalf("set photo: %v", err)
	}
	link, _ := models.CreateShareLink(ctx, d, models.NewShareLinkParams{
		Mode: sharing.ModePublicLong, Scope: sharing.ScopeContact, TargetID: contact.ID, CreatedBy: owner.ID, MaxUses: 1,
	})
	base := "/s/" + link.Token

	// The single permitted view consumes the last use.
	if rec := get(router, base); rec.Code != http.StatusOK {
		t.Fatalf("first view = %d, want 200", rec.Code)
	}
	// Its photo must still load even though the view just exhausted the link.
	if rec := get(router, base+"/photo"); rec.Code != http.StatusOK {
		t.Errorf("photo after final view = %d, want 200", rec.Code)
	}
	// A second page view is refused (uses exhausted).
	if rec := get(router, base); rec.Code != http.StatusNotFound {
		t.Errorf("second view = %d, want 404 (exhausted)", rec.Code)
	}
}

func TestRevokeRejectsAlreadyRevoked(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Friends", "")
	link, _ := models.CreateShareLink(ctx, d, models.NewShareLinkParams{
		Mode: sharing.ModePublicLong, Scope: sharing.ScopeBook, TargetID: book.ID, CreatedBy: owner.ID,
	})
	session := sessionCookieFor(t, d, owner.ID)
	sharesURL := "/books/" + book.PublicID + "/shares"
	_, token, csrf := authedGet(t, router, session, sharesURL)
	revokeURL := sharesURL + "/" + strconv.FormatInt(link.ID, 10) + "/revoke"

	if rec := authedPostForm(router, session, csrf, revokeURL, url.Values{auth.CSRFFormField: {token}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("first revoke = %d, want 303", rec.Code)
	}
	// Soft-deleted: still present for the audit trail.
	if _, err := models.GetShareLinkByToken(ctx, d, link.Token); err != nil {
		t.Errorf("revoked link should still exist: %v", err)
	}
	// Revoking it again is refused.
	if rec := authedPostForm(router, session, csrf, revokeURL, url.Values{auth.CSRFFormField: {token}}); rec.Code != http.StatusNotFound {
		t.Errorf("second revoke = %d, want 404", rec.Code)
	}
}

func TestSharesBackReturnsToOrigin(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Friends", "")
	session := sessionCookieFor(t, d, owner.ID)
	sharesURL := "/books/" + book.PublicID + "/shares"

	// Opened from the overview: back returns to the overview, actions still target the book.
	fromOverview, _, _ := authedGet(t, router, session, sharesURL+"?return=/books")
	if !strings.Contains(fromOverview.Body.String(), `href="/books"`) {
		t.Error("sharing opened from the overview should link back to it")
	}
	if !strings.Contains(fromOverview.Body.String(), `action="`+sharesURL+`"`) {
		t.Error("create action should still target the book's shares endpoint")
	}
	// Default (from the book page): back returns to the book.
	fromBook, _, _ := authedGet(t, router, session, sharesURL)
	if !strings.Contains(fromBook.Body.String(), `href="/books/`+book.PublicID+`"`) {
		t.Error("sharing should default its back link to the book")
	}
}
