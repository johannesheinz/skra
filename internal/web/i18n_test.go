package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
)

// TestLocaleFromAcceptLanguage: an anonymous request's Accept-Language drives
// the rendered language and the <html lang> attribute.
func TestLocaleFromAcceptLanguage(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.Header.Set("Accept-Language", "de-DE,de;q=0.9")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `lang="de-DE"`) {
		t.Error("expected <html lang=\"de-DE\">")
	}
	if !strings.Contains(body, "Anmelden") {
		t.Errorf("expected German login text, got:\n%s", body)
	}
}

// TestLocalePreferenceOverridesHeader: a signed-in user's saved locale wins over
// Accept-Language, and localized formatting/text appears on a real page.
func TestLocalePreferenceOverridesHeader(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	user := seedUser(t, d, "alice", "pw", models.RoleUser)
	session := sessionCookieFor(t, d, user.ID)

	// Save German via the account selector.
	_, token, csrf := authedGet(t, router, session, "/account")
	save := authedPostForm(router, session, csrf, "/account/locale", url.Values{
		auth.CSRFFormField: {token}, "locale": {"de-DE"},
	})
	if save.Code != http.StatusSeeOther {
		t.Fatalf("save locale = %d", save.Code)
	}
	if prefs, _ := models.GetPreferences(ctx, d, user.ID); prefs.Locale != "de-DE" {
		t.Fatalf("stored locale = %q", prefs.Locale)
	}

	// Even with an English Accept-Language, the page renders German.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "en-US")
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `lang="de-DE"`) {
		t.Error("saved locale should force lang=de-DE")
	}
	if !strings.Contains(body, "Willkommen") {
		t.Errorf("expected German welcome, got body without it")
	}
}
