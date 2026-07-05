package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/db"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
)

func sessionCookieFor(t *testing.T, d *db.DB, userID int64) *http.Cookie {
	t.Helper()
	id, err := auth.NewSessionStore(d).Create(context.Background(), userID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return &http.Cookie{Name: auth.SessionCookieName, Value: id}
}

// authedGet performs a GET and returns the recorder plus the CSRF token and cookie issued by the page (for a follow-up POST).
func authedGet(t *testing.T, router http.Handler, session *http.Cookie, path string) (*httptest.ResponseRecorder, string, *http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	csrf := cookieByName(rec.Result().Cookies(), auth.CSRFCookieName)
	var token string
	if csrf != nil {
		token = extractCSRF(t, rec.Body.String())
	}
	return rec, token, csrf
}

func authedPostForm(router http.Handler, session, csrf *http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	if csrf != nil {
		req.AddCookie(csrf)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestBookCRUDFlow(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	session := sessionCookieFor(t, d, owner.ID)

	// Empty list.
	listRec, _, _ := authedGet(t, router, session, "/books")
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), "No address books yet") {
		t.Fatalf("empty /books = %d, body missing empty notice", listRec.Code)
	}

	// New form → create.
	_, token, csrf := authedGet(t, router, session, "/books/new")
	createRec := authedPostForm(router, session, csrf, "/books",
		url.Values{auth.CSRFFormField: {token}, "name": {"Friends"}, "description": {"people"}})
	if createRec.Code != http.StatusSeeOther {
		t.Fatalf("create book = %d, want 303", createRec.Code)
	}
	loc := createRec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/books/") {
		t.Fatalf("create redirect = %q, want /books/{id}", loc)
	}

	// Detail shows the book and (owner is manager) the manage controls.
	showRec, _, _ := authedGet(t, router, session, loc)
	if showRec.Code != http.StatusOK {
		t.Fatalf("show book = %d, want 200", showRec.Code)
	}
	body := showRec.Body.String()
	if !strings.Contains(body, "Friends") || !strings.Contains(body, "/delete") {
		t.Error("detail missing name or manage controls for owner")
	}

	// Edit.
	publicID := strings.TrimPrefix(loc, "/books/")
	_, editToken, editCSRF := authedGet(t, router, session, "/books/"+publicID+"/edit")
	editRec := authedPostForm(router, session, editCSRF, "/books/"+publicID+"/edit",
		url.Values{auth.CSRFFormField: {editToken}, "name": {"Close Friends"}, "description": {""}})
	if editRec.Code != http.StatusSeeOther {
		t.Fatalf("edit book = %d, want 303", editRec.Code)
	}
	afterEdit, _, _ := authedGet(t, router, session, loc)
	if !strings.Contains(afterEdit.Body.String(), "Close Friends") {
		t.Error("edit did not persist new name")
	}

	// Create rejects a blank name (422, re-render).
	_, blankToken, blankCSRF := authedGet(t, router, session, "/books/new")
	blankRec := authedPostForm(router, session, blankCSRF, "/books",
		url.Values{auth.CSRFFormField: {blankToken}, "name": {"  "}})
	if blankRec.Code != http.StatusUnprocessableEntity {
		t.Errorf("blank-name create = %d, want 422", blankRec.Code)
	}
}

func TestBookAuthorization(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()

	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	stranger := seedUser(t, d, "stranger", "pw", models.RoleUser)
	viewer := seedUser(t, d, "viewer", "pw", models.RoleUser)

	book, err := models.CreateAddressBook(ctx, d, owner.ID, "Private", "")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	if _, err := d.ExecWrite(ctx,
		`INSERT INTO address_book_members (address_book_id, user_id, access_level) VALUES (?, ?, ?)`,
		book.ID, viewer.ID, models.AccessViewer); err != nil {
		t.Fatalf("grant viewer: %v", err)
	}

	// Stranger cannot see the book at all → 404.
	strangerRec, _, _ := authedGet(t, router, sessionCookieFor(t, d, stranger.ID), "/books/"+book.PublicID)
	if strangerRec.Code != http.StatusNotFound {
		t.Errorf("stranger show = %d, want 404", strangerRec.Code)
	}

	// Viewer can read but the edit route is forbidden (visible → 403, not 404).
	viewerSession := sessionCookieFor(t, d, viewer.ID)
	viewerShow, _, _ := authedGet(t, router, viewerSession, "/books/"+book.PublicID)
	if viewerShow.Code != http.StatusOK {
		t.Errorf("viewer show = %d, want 200", viewerShow.Code)
	}
	if strings.Contains(viewerShow.Body.String(), "/delete") {
		t.Error("viewer should not see manage controls")
	}
	viewerEdit, _, _ := authedGet(t, router, viewerSession, "/books/"+book.PublicID+"/edit")
	if viewerEdit.Code != http.StatusForbidden {
		t.Errorf("viewer edit = %d, want 403", viewerEdit.Code)
	}
}

func TestGlobalSearchFoldedIntoDashboard(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	user := seedUser(t, d, "u", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, user.ID, "Book", "")
	if _, err := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Zoe Zulu"}); err != nil {
		t.Fatal(err)
	}
	session := sessionCookieFor(t, d, user.ID)

	// Full-page dashboard search.
	full := get(router, "/?q=Zoe", session)
	if full.Code != http.StatusOK || !strings.Contains(full.Body.String(), "Zoe Zulu") {
		t.Fatalf("dashboard search = %d, missing result", full.Code)
	}
	if !strings.Contains(strings.ToLower(full.Body.String()), "<!doctype") {
		t.Error("full-page search should render the whole page")
	}

	// htmx request returns only the results fragment.
	req := httptest.NewRequest(http.MethodGet, "/?q=Zoe", nil)
	req.AddCookie(session)
	req.Header.Set("HX-Request", "true")
	frag := httptest.NewRecorder()
	router.ServeHTTP(frag, req)
	if frag.Code != http.StatusOK || !strings.Contains(frag.Body.String(), "Zoe Zulu") {
		t.Fatalf("htmx search = %d, missing result", frag.Code)
	}
	if strings.Contains(strings.ToLower(frag.Body.String()), "<!doctype") {
		t.Error("htmx search should return the fragment only, not the whole page")
	}

	// The standalone /search page is gone.
	if rec := get(router, "/search?q=Zoe", session); rec.Code != http.StatusNotFound {
		t.Errorf("/search = %d, want 404", rec.Code)
	}
}

func TestBookContactSearchLiveFragment(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Clients", "")
	if _, err := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Zoe Zulu"}); err != nil {
		t.Fatal(err)
	}
	session := sessionCookieFor(t, d, owner.ID)
	base := "/books/" + book.PublicID

	// Full page has the whole document and the book title.
	full := get(router, base, session)
	if full.Code != http.StatusOK || !strings.Contains(strings.ToLower(full.Body.String()), "<!doctype") {
		t.Fatalf("book page = %d, not a full page", full.Code)
	}

	// htmx search returns just the results region (the count heading, no full page).
	req := httptest.NewRequest(http.MethodGet, base+"?q=Zoe", nil)
	req.AddCookie(session)
	req.Header.Set("HX-Request", "true")
	frag := httptest.NewRecorder()
	router.ServeHTTP(frag, req)
	if frag.Code != http.StatusOK || !strings.Contains(frag.Body.String(), "Zoe Zulu") {
		t.Fatalf("book htmx search = %d, missing result", frag.Code)
	}
	if strings.Contains(strings.ToLower(frag.Body.String()), "<!doctype") {
		t.Error("book htmx search should return the results fragment only")
	}
}

func TestBookEditCancelReturnsToBook(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Clients", "")
	session := sessionCookieFor(t, d, owner.ID)

	rec, _, _ := authedGet(t, router, session, "/books/"+book.PublicID+"/edit")
	if rec.Code != http.StatusOK {
		t.Fatalf("edit page = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `href="/books/`+book.PublicID+`"`) {
		t.Error("edit cancel should return to the book, not the overview")
	}
}

func TestBookEditReturnsToOrigin(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Clients", "")
	session := sessionCookieFor(t, d, owner.ID)

	// The overview links its edit action back to itself.
	overview := get(router, "/books", session)
	if !strings.Contains(overview.Body.String(), "/edit?return=/books") {
		t.Error("overview edit link should carry return=/books")
	}

	// Opened from the overview: cancel points back to the overview...
	rec, token, csrf := authedGet(t, router, session, "/books/"+book.PublicID+"/edit?return=/books")
	if !strings.Contains(rec.Body.String(), `href="/books"`) {
		t.Error("cancel should return to the overview when opened from it")
	}
	// ...and saving redirects there too.
	save := authedPostForm(router, session, csrf, "/books/"+book.PublicID+"/edit",
		url.Values{auth.CSRFFormField: {token}, "name": {"Clients"}, "return": {"/books"}})
	if save.Code != http.StatusSeeOther || save.Header().Get("Location") != "/books" {
		t.Errorf("save redirect = %d %q, want 303 /books", save.Code, save.Header().Get("Location"))
	}

	// An unsafe (off-site) return is ignored: cancel falls back to the book's own page.
	rec2, _, _ := authedGet(t, router, session, "/books/"+book.PublicID+"/edit?return=https://evil.test")
	if strings.Contains(rec2.Body.String(), `href="https://evil.test"`) {
		t.Error("unsafe return must not become the cancel target")
	}
	if !strings.Contains(rec2.Body.String(), `href="/books/`+book.PublicID+`"`) {
		t.Error("unsafe return should fall back to the book page")
	}
}
