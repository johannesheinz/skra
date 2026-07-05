package web

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
)

const importVCF = "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Jane Doe\r\nEMAIL:jane@acme.test\r\nUID:uid-jane\r\nEND:VCARD\r\n" +
	"BEGIN:VCARD\r\nVERSION:4.0\r\nFN:Bob Roe\r\nEMAIL:bob@roe.test\r\nEND:VCARD\r\n"

func uploadVCF(t *testing.T, router http.Handler, session, csrf *http.Cookie, url_, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField(auth.CSRFFormField, token)
	fw, _ := mw.CreateFormFile("file", "contacts.vcf")
	_, _ = fw.Write([]byte(body))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, url_, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(session)
	req.AddCookie(csrf)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func uploadNewBookVCF(t *testing.T, router http.Handler, session, csrf *http.Cookie, token, name, body string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField(auth.CSRFFormField, token)
	_ = mw.WriteField("name", name)
	fw, _ := mw.CreateFormFile("file", "contacts.vcf")
	_, _ = fw.Write([]byte(body))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/books/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(session)
	req.AddCookie(csrf)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestBookImportNewCreatesBookAndImports(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	admin := seedUser(t, d, "admin", "pw", models.RoleAdmin)
	session := sessionCookieFor(t, d, admin.ID)

	_, token, csrf := authedGet(t, router, session, "/books")
	rec := uploadNewBookVCF(t, router, session, csrf, token, "Imported Team", importVCF)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Imported 2 contacts") {
		t.Fatalf("create+import = %d, body:\n%s", rec.Code, rec.Body.String())
	}

	// The book was created and holds both contacts.
	books, _ := models.ListAddressBooks(ctx, d, admin)
	var foundID int64
	for i := range books {
		if books[i].Name == "Imported Team" {
			foundID = books[i].ID
		}
	}
	if foundID == 0 {
		t.Fatalf("new book not created; have %+v", books)
	}
	contacts, total, _ := models.ListContacts(ctx, d, foundID, "", "", false, 50, 0)
	if total != 2 || len(contacts) != 2 {
		t.Errorf("imported book total=%d, want 2", total)
	}
}

func TestBookImportNewAllowedForNonAdmin(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	user := seedUser(t, d, "alice", "pw", models.RoleUser)
	session := sessionCookieFor(t, d, user.ID)

	// Creating (and importing into) a book is open to any user, matching the
	// open "New address book" action, so the overview links to the import page...
	page, token, csrf := authedGet(t, router, session, "/books")
	if !strings.Contains(page.Body.String(), `href="/books/import"`) {
		t.Error("non-admin should see the import action on the overview")
	}
	// ...the import page renders for them...
	if form := get(router, "/books/import", session); form.Code != http.StatusOK {
		t.Errorf("import page for non-admin = %d, want 200", form.Code)
	}
	// ...and the import succeeds, creating a book they own.
	rec := uploadNewBookVCF(t, router, session, csrf, token, "Alice's Import", importVCF)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Imported 2 contacts") {
		t.Fatalf("non-admin create+import = %d, body:\n%s", rec.Code, rec.Body.String())
	}
	books, _ := models.ListAddressBooks(ctx, d, user)
	found := false
	for i := range books {
		if books[i].Name == "Alice's Import" {
			found = true
		}
	}
	if !found {
		t.Errorf("imported book not created for non-admin; have %+v", books)
	}
}

func TestImportVCardFlow(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	session := sessionCookieFor(t, d, owner.ID)
	importURL := "/books/" + book.PublicID + "/import"

	// Upload → preview shows 2 new.
	_, token, csrf := authedGet(t, router, session, importURL)
	preview := uploadVCF(t, router, session, csrf, importURL, token, importVCF)
	if preview.Code != http.StatusOK {
		t.Fatalf("upload preview = %d, want 200", preview.Code)
	}
	body := preview.Body.String()
	if !strings.Contains(body, "2 contacts parsed") || !strings.Contains(body, "Jane Doe") {
		t.Fatalf("preview missing expected content:\n%s", body)
	}
	stageToken := extractHidden(t, body, "token")

	// Commit (skip duplicates) → 2 inserted.
	commitCSRF := cookieByName(preview.Result().Cookies(), auth.CSRFCookieName)
	commitToken := extractCSRF(t, body)
	commit := authedPostForm(router, session, commitCSRF, importURL+"/commit", url.Values{
		auth.CSRFFormField: {commitToken}, "token": {stageToken}, "action": {"skip"},
	})
	if commit.Code != http.StatusOK || !strings.Contains(commit.Body.String(), "Imported 2 contacts") {
		t.Fatalf("commit = %d, body:\n%s", commit.Code, commit.Body.String())
	}

	// Contacts now exist in the book.
	contacts, total, _ := models.ListContacts(ctx, d, book.ID, "", "", false, 50, 0)
	if total != 2 || len(contacts) != 2 {
		t.Errorf("after import total=%d, want 2", total)
	}
}

func TestImportSkipsDuplicates(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	// Pre-existing contact with the same UID as Jane in the file.
	if _, err := d.ExecWrite(ctx,
		`INSERT INTO contacts (public_id, address_book_id, full_name, vcard_raw, uid, etag)
		 VALUES ('pid', ?, 'Existing Jane', 'BEGIN:VCARD', 'uid-jane', 'e')`, book.ID); err != nil {
		t.Fatalf("seed: %v", err)
	}
	session := sessionCookieFor(t, d, owner.ID)
	importURL := "/books/" + book.PublicID + "/import"

	_, token, csrf := authedGet(t, router, session, importURL)
	preview := uploadVCF(t, router, session, csrf, importURL, token, importVCF)
	body := preview.Body.String()
	if !strings.Contains(body, "1 new") || !strings.Contains(body, "1 duplicate") {
		t.Errorf("preview should show 1 new / 1 duplicate:\n%s", body)
	}
	stageToken := extractHidden(t, body, "token")
	commitToken := extractCSRF(t, body)
	commitCSRF := cookieByName(preview.Result().Cookies(), auth.CSRFCookieName)
	commit := authedPostForm(router, session, commitCSRF, importURL+"/commit", url.Values{
		auth.CSRFFormField: {commitToken}, "token": {stageToken}, "action": {"skip"},
	})
	if !strings.Contains(commit.Body.String(), "Imported 1 contact") {
		t.Errorf("expected 1 imported, got:\n%s", commit.Body.String())
	}
}

func TestImportRequiresManager(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	viewer := seedUser(t, d, "viewer", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	if _, err := d.ExecWrite(ctx,
		`INSERT INTO address_book_members (address_book_id, user_id, access_level) VALUES (?, ?, ?)`,
		book.ID, viewer.ID, models.AccessViewer); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if rec, _, _ := authedGet(t, router, sessionCookieFor(t, d, viewer.ID), "/books/"+book.PublicID+"/import"); rec.Code != http.StatusForbidden {
		t.Errorf("viewer import page = %d, want 403", rec.Code)
	}
}

// extractHidden pulls a hidden input's value by name from rendered HTML.
func extractHidden(t *testing.T, body, name string) string {
	t.Helper()
	marker := `name="` + name + `" value="`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("hidden field %q not found", name)
	}
	rest := body[i+len(marker):]
	end := strings.IndexByte(rest, '"')
	return rest[:end]
}

func TestImportRejectsUnknownAction(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	session := sessionCookieFor(t, d, owner.ID)
	importURL := "/books/" + book.PublicID + "/import"

	_, token, csrf := authedGet(t, router, session, importURL)
	preview := uploadVCF(t, router, session, csrf, importURL, token, importVCF)
	body := preview.Body.String()
	stageToken := extractHidden(t, body, "token")
	commitCSRF := cookieByName(preview.Result().Cookies(), auth.CSRFCookieName)
	commitToken := extractCSRF(t, body)

	// An unknown action is rejected (no silent default to skip).
	rec := authedPostForm(router, session, commitCSRF, importURL+"/commit", url.Values{
		auth.CSRFFormField: {commitToken}, "token": {stageToken}, "action": {"bogus"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown action = %d, want 400", rec.Code)
	}
	// Nothing was imported.
	if _, total, _ := models.ListContacts(ctx, d, book.ID, "", "", false, 50, 0); total != 0 {
		t.Errorf("contacts imported despite invalid action: %d", total)
	}
}
