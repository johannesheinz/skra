package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/config"
	"github.com/johannesheinz/skra/internal/db"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
)

func testRouter(t *testing.T, d *db.DB) http.Handler {
	t.Helper()
	cfg := config.Config{
		Listen:       "127.0.0.1:0",
		DBPath:       "unused",
		CookieSecure: false,
		ExternalURL:  "https://share.example.test",
		SessionKey:   "0123456789abcdef0123456789abcdef",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router, err := buildRouter(cfg, d, logger)
	if err != nil {
		t.Fatalf("buildRouter: %v", err)
	}
	return router
}

var csrfFieldRE = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

func extractCSRF(t *testing.T, body string) string {
	t.Helper()
	m := csrfFieldRE.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no csrf_token field in body")
	}
	return m[1]
}

func cookieByName(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func seedUser(t *testing.T, d *db.DB, username, password, role string) models.User {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := models.CreateUser(context.Background(), d, username, username+"@example.com", hash, role)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func TestHealthz(t *testing.T) {
	router := testRouter(t, testutil.NewDB(t))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	router := testRouter(t, testutil.NewDB(t))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/does-not-exist", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestRequireAuthRedirectsHomeWhenAnonymous(t *testing.T) {
	router := testRouter(t, testutil.NewDB(t))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Errorf("anonymous / = %d %q, want 303 /login", rec.Code, rec.Header().Get("Location"))
	}
}

func TestLoginLogoutFlow(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	seedUser(t, d, "alice", "correct-password", models.RoleUser)

	// GET /login → csrf token + cookie.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login = %d, want 200", rec.Code)
	}
	token := extractCSRF(t, rec.Body.String())
	csrfCookie := cookieByName(rec.Result().Cookies(), auth.CSRFCookieName)
	if csrfCookie == nil {
		t.Fatal("no csrf cookie on GET /login")
	}

	// Wrong password → 401, no session.
	badRec := postLogin(router, csrfCookie, token, "alice", "wrong")
	if badRec.Code != http.StatusUnauthorized {
		t.Errorf("bad login = %d, want 401", badRec.Code)
	}
	if cookieByName(badRec.Result().Cookies(), auth.SessionCookieName) != nil {
		t.Error("bad login should not set a session cookie")
	}

	// Correct password → 303 to / with a session cookie.
	okRec := postLogin(router, csrfCookie, token, "alice", "correct-password")
	if okRec.Code != http.StatusSeeOther || okRec.Header().Get("Location") != "/" {
		t.Fatalf("login = %d %q, want 303 /", okRec.Code, okRec.Header().Get("Location"))
	}
	sessionCookie := cookieByName(okRec.Result().Cookies(), auth.SessionCookieName)
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatal("login did not set a session cookie")
	}

	// Authenticated GET / shows the username.
	homeReq := httptest.NewRequest(http.MethodGet, "/", nil)
	homeReq.AddCookie(sessionCookie)
	homeRec := httptest.NewRecorder()
	router.ServeHTTP(homeRec, homeReq)
	if homeRec.Code != http.StatusOK || !strings.Contains(homeRec.Body.String(), "alice") {
		t.Errorf("home = %d, body lacks username", homeRec.Code)
	}

	// Logout (needs csrf from the home page).
	logoutToken := extractCSRF(t, homeRec.Body.String())
	logoutCSRF := cookieByName(homeRec.Result().Cookies(), auth.CSRFCookieName)
	form := url.Values{auth.CSRFFormField: {logoutToken}}
	logoutReq := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(form.Encode()))
	logoutReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	logoutReq.AddCookie(sessionCookie)
	logoutReq.AddCookie(logoutCSRF)
	logoutRec := httptest.NewRecorder()
	router.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusSeeOther || logoutRec.Header().Get("Location") != "/login" {
		t.Errorf("logout = %d %q, want 303 /login", logoutRec.Code, logoutRec.Header().Get("Location"))
	}

	// Session is gone: reusing the cookie no longer authenticates.
	reuseReq := httptest.NewRequest(http.MethodGet, "/", nil)
	reuseReq.AddCookie(sessionCookie)
	reuseRec := httptest.NewRecorder()
	router.ServeHTTP(reuseRec, reuseReq)
	if reuseRec.Code != http.StatusSeeOther {
		t.Errorf("reused session after logout = %d, want 303 redirect", reuseRec.Code)
	}
}

func postLogin(router http.Handler, csrfCookie *http.Cookie, token, username, password string) *httptest.ResponseRecorder {
	form := url.Values{
		auth.CSRFFormField: {token},
		"username":         {username},
		"password":         {password},
	}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrfCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestContactPhotoServingAndAuthorization(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()

	admin := seedUser(t, d, "admin", "pw", models.RoleAdmin)
	stranger := seedUser(t, d, "stranger", "pw", models.RoleUser)

	// Seed a book, a contact, and its photo.
	bookRes, err := d.ExecWrite(ctx,
		`INSERT INTO address_books (public_id, name, owner_id) VALUES ('book', 'Book', ?)`, admin.ID)
	if err != nil {
		t.Fatalf("seed book: %v", err)
	}
	bookID, _ := bookRes.LastInsertId()
	contactRes, err := d.ExecWrite(ctx,
		`INSERT INTO contacts (public_id, address_book_id, full_name, has_photo, vcard_raw, uid, etag)
		 VALUES ('contact-pid', ?, 'Jane', 1, 'BEGIN:VCARD', 'uid-1', 'cetag')`, bookID)
	if err != nil {
		t.Fatalf("seed contact: %v", err)
	}
	contactID, _ := contactRes.LastInsertId()
	if _, err := d.ExecWrite(ctx,
		`INSERT INTO contact_photos (contact_id, mime_type, bytes, etag) VALUES (?, 'image/jpeg', ?, 'photo-v1')`,
		contactID, []byte{0x01, 0x02, 0x03}); err != nil {
		t.Fatalf("seed photo: %v", err)
	}

	sessions := auth.NewSessionStore(d)
	cookieFor := func(userID int64) *http.Cookie {
		id, err := sessions.Create(ctx, userID)
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		return &http.Cookie{Name: auth.SessionCookieName, Value: id}
	}

	photoURL := "/contacts/contact-pid/photo"

	// Admin can read: 200 with ETag and bytes.
	req := httptest.NewRequest(http.MethodGet, photoURL, nil)
	req.AddCookie(cookieFor(admin.ID))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin photo = %d, want 200", rec.Code)
	}
	etag := rec.Header().Get("ETag")
	if etag != `"photo-v1"` {
		t.Errorf("ETag = %q, want %q", etag, `"photo-v1"`)
	}
	if rec.Body.Len() != 3 {
		t.Errorf("body length = %d, want 3", rec.Body.Len())
	}

	// Conditional GET with matching ETag → 304, no body.
	condReq := httptest.NewRequest(http.MethodGet, photoURL, nil)
	condReq.AddCookie(cookieFor(admin.ID))
	condReq.Header.Set("If-None-Match", etag)
	condRec := httptest.NewRecorder()
	router.ServeHTTP(condRec, condReq)
	if condRec.Code != http.StatusNotModified {
		t.Errorf("conditional GET = %d, want 304", condRec.Code)
	}
	if condRec.Body.Len() != 0 {
		t.Errorf("304 body length = %d, want 0", condRec.Body.Len())
	}

	// Stranger (no grant) → 404, not 403.
	strangerReq := httptest.NewRequest(http.MethodGet, photoURL, nil)
	strangerReq.AddCookie(cookieFor(stranger.ID))
	strangerRec := httptest.NewRecorder()
	router.ServeHTTP(strangerRec, strangerReq)
	if strangerRec.Code != http.StatusNotFound {
		t.Errorf("stranger photo = %d, want 404", strangerRec.Code)
	}

	// Unknown contact → 404.
	missingReq := httptest.NewRequest(http.MethodGet, "/contacts/nope/photo", nil)
	missingReq.AddCookie(cookieFor(admin.ID))
	missingRec := httptest.NewRecorder()
	router.ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Errorf("missing contact photo = %d, want 404", missingRec.Code)
	}
}
