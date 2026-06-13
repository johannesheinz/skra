package auth_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/db"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
)

func newAuthenticator(t *testing.T, d *db.DB) *auth.Authenticator {
	t.Helper()
	return &auth.Authenticator{
		Sessions:  auth.NewSessionStore(d),
		DB:        d,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		LoginPath: "/login",
	}
}

func authedRequest(t *testing.T, d *db.DB, a *auth.Authenticator, userID int64) *http.Request {
	t.Helper()
	id, err := a.Sessions.Create(context.Background(), userID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: id})
	return req
}

func TestLoadUserPopulatesContext(t *testing.T) {
	d := testutil.NewDB(t)
	a := newAuthenticator(t, d)
	user, err := models.CreateUser(context.Background(), d, "alice", "a@example.com", "h", models.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	var seen models.User
	var ok bool
	handler := a.LoadUser(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen, ok = auth.UserFromContext(r.Context())
	}))
	handler.ServeHTTP(httptest.NewRecorder(), authedRequest(t, d, a, user.ID))

	if !ok || seen.ID != user.ID {
		t.Errorf("context user = (%+v, %v), want id %d", seen, ok, user.ID)
	}
}

func TestRequireAuthRedirectsAnonymous(t *testing.T) {
	d := testutil.NewDB(t)
	a := newAuthenticator(t, d)

	guarded := a.LoadUser(a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	rec := httptest.NewRecorder()
	guarded.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/secret", nil))
	if rec.Code != http.StatusSeeOther {
		t.Errorf("anonymous status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("redirect Location = %q, want /login", loc)
	}
}

func TestRequireAuthAllowsAuthenticated(t *testing.T) {
	d := testutil.NewDB(t)
	a := newAuthenticator(t, d)
	user, err := models.CreateUser(context.Background(), d, "alice", "a@example.com", "h", models.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	guarded := a.LoadUser(a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	rec := httptest.NewRecorder()
	guarded.ServeHTTP(rec, authedRequest(t, d, a, user.ID))
	if rec.Code != http.StatusOK {
		t.Errorf("authenticated status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRequireAdminHides404FromNonAdmin(t *testing.T) {
	d := testutil.NewDB(t)
	a := newAuthenticator(t, d)
	user, err := models.CreateUser(context.Background(), d, "alice", "a@example.com", "h", models.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	guarded := a.LoadUser(a.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	rec := httptest.NewRecorder()
	guarded.ServeHTTP(rec, authedRequest(t, d, a, user.ID))
	if rec.Code != http.StatusNotFound {
		t.Errorf("non-admin on admin route status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
