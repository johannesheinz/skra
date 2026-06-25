package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
)

// TestBodyLimitOnFormRoute: an oversized body on an ordinary form route is
// rejected (4xx), while a normal-sized submission on the same route succeeds.
func TestBodyLimitOnFormRoute(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	user := seedUser(t, d, "u", "pw", models.RoleUser)
	session := sessionCookieFor(t, d, user.ID)

	_, token, csrf := authedGet(t, router, session, "/books/new")

	// > 1 MiB body → rejected before the handler can act on it.
	oversized := url.Values{auth.CSRFFormField: {token}, "name": {"x"}, "description": {strings.Repeat("x", 2<<20)}}
	req := httptest.NewRequest(http.MethodPost, "/books", strings.NewReader(oversized.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	req.AddCookie(csrf)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code < 400 {
		t.Errorf("oversized body POST = %d, want 4xx", rec.Code)
	}

	// A normal submission on the same route is unaffected.
	_, token2, csrf2 := authedGet(t, router, session, "/books/new")
	ok := authedPostForm(router, session, csrf2, "/books", url.Values{auth.CSRFFormField: {token2}, "name": {"My Book"}})
	if ok.Code != http.StatusSeeOther {
		t.Errorf("normal create = %d, want 303", ok.Code)
	}
}
