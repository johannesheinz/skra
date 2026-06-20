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

func TestAccountPageSections(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	user := seedUser(t, d, "alice", "pw", models.RoleUser)
	rec, _, _ := authedGet(t, router, sessionCookieFor(t, d, user.ID), "/account")
	if rec.Code != http.StatusOK {
		t.Fatalf("/account = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Profile", "Appearance", "Memberships", "Security", "swatch"} {
		if !strings.Contains(body, want) {
			t.Errorf("account page missing %q", want)
		}
	}
}

func TestAccountAppearancePersistsAndRenders(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	user := seedUser(t, d, "alice", "pw", models.RoleUser)
	session := sessionCookieFor(t, d, user.ID)

	_, token, csrf := authedGet(t, router, session, "/account")
	save := authedPostForm(router, session, csrf, "/account/appearance", url.Values{
		auth.CSRFFormField: {token}, "mode": {"dark"}, "flavor": {"solarized"}, "accent": {"lime"},
	})
	if save.Code != http.StatusOK || !strings.Contains(save.Body.String(), "Appearance saved") {
		t.Fatalf("appearance save = %d", save.Code)
	}

	// Stored in the DB.
	prefs, err := models.GetPreferences(ctx, d, user.ID)
	if err != nil {
		t.Fatalf("GetPreferences: %v", err)
	}
	if prefs.Theme.Mode != "dark" || prefs.Theme.Flavor != "solarized" || prefs.Theme.Accent != "lime" {
		t.Errorf("stored prefs = %+v", prefs.Theme)
	}

	// Rendered as <html data-*> on other authenticated pages.
	home, _, _ := authedGet(t, router, session, "/")
	hb := home.Body.String()
	for _, want := range []string{`data-theme="dark"`, `data-flavor="solarized"`, `data-accent="lime"`} {
		if !strings.Contains(hb, want) {
			t.Errorf("home missing %q", want)
		}
	}

	// Invalid accent rejected.
	_, t2, c2 := authedGet(t, router, session, "/account")
	bad := authedPostForm(router, session, c2, "/account/appearance", url.Values{
		auth.CSRFFormField: {t2}, "mode": {"dark"}, "flavor": {""}, "accent": {"chartreuse"},
	})
	if bad.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid accent = %d, want 422", bad.Code)
	}
}

func TestAccountProfileUpdatesEmail(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	user := seedUser(t, d, "alice", "pw", models.RoleUser)
	session := sessionCookieFor(t, d, user.ID)

	_, token, csrf := authedGet(t, router, session, "/account")
	rec := authedPostForm(router, session, csrf, "/account/profile", url.Values{
		auth.CSRFFormField: {token}, "email": {"new@alice.test"},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Profile updated") {
		t.Fatalf("profile update = %d", rec.Code)
	}
	reloaded, _ := models.GetUserByID(ctx, d, user.ID)
	if reloaded.Email != "new@alice.test" {
		t.Errorf("email = %q, want new@alice.test", reloaded.Email)
	}
}

func TestAccountThemeTogglePersists(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	user := seedUser(t, d, "alice", "pw", models.RoleUser)
	session := sessionCookieFor(t, d, user.ID)

	// The header toggle is a form on every authenticated page.
	_, token, csrf := authedGet(t, router, session, "/")
	toggle := func() int {
		return authedPostForm(router, session, csrf, "/account/theme", url.Values{auth.CSRFFormField: {token}}).Code
	}
	if code := toggle(); code != http.StatusSeeOther {
		t.Fatalf("toggle = %d, want 303", code)
	}
	// From the default (system) it flips to dark, persisted to the DB.
	prefs, _ := models.GetPreferences(ctx, d, user.ID)
	if prefs.Theme.Mode != "dark" {
		t.Errorf("after toggle mode = %q, want dark", prefs.Theme.Mode)
	}
	// And it shows on the authenticated page + stays consistent with settings.
	home, _, _ := authedGet(t, router, session, "/")
	if !strings.Contains(home.Body.String(), `data-theme="dark"`) || !strings.Contains(home.Body.String(), "data-theme-managed") {
		t.Error("home should render data-theme=dark and data-theme-managed after toggle")
	}
}

func TestAccountPasswordRedirect(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	user := seedUser(t, d, "alice", "pw", models.RoleUser)
	rec, _, _ := authedGet(t, router, sessionCookieFor(t, d, user.ID), "/account/password")
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/account" {
		t.Errorf("GET /account/password = %d %q, want 301 /account", rec.Code, rec.Header().Get("Location"))
	}
}
