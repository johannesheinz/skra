package handlers

import (
	"log/slog"
	"net/http"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/db"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/sharing"
	"github.com/johannesheinz/skra/internal/web/templates"
)

// MinPasswordLen is the minimum length for user-set and admin-set passwords.
const MinPasswordLen = 8

// Handlers holds the dependencies shared by Skra's HTTP handlers.
type Handlers struct {
	DB           *db.DB
	Sessions     *auth.SessionStore
	CookieSecure bool
	ExternalURL  string
	Gate         sharing.GateSigner
	Logger       *slog.Logger

	// dummyHash is verified against when a username is unknown, so that login
	// timing does not reveal whether an account exists.
	dummyHash string
}

// New builds the handler set. It precomputes a dummy password hash for the
// user-enumeration defense; failing to do so is fatal to construction.
func New(database *db.DB, sessions *auth.SessionStore, cookieSecure bool, externalURL, sessionKey string, logger *slog.Logger) (*Handlers, error) {
	dummy, err := auth.HashPassword("skra-nonexistent-account")
	if err != nil {
		return nil, err
	}
	return &Handlers{
		DB:           database,
		Sessions:     sessions,
		CookieSecure: cookieSecure,
		ExternalURL:  externalURL,
		Gate:         sharing.NewGateSigner(sessionKey),
		Logger:       logger,
		dummyHash:    dummy,
	}, nil
}

// render renders a page with the shared layout. It injects the authenticated
// user (if any) and a freshly issued CSRF token, which the base layout uses for
// its nav and the logout form.
func (h *Handlers) render(w http.ResponseWriter, r *http.Request, status int, page string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	// Path is where the header theme toggle returns to. Only a GET URL is safe
	// to return to; a POST-rendered page (a form result) would 405 on GET, so
	// fall back to home unless the caller set an explicit GET path.
	if _, set := data["Path"]; !set {
		if r.Method == http.MethodGet {
			data["Path"] = r.URL.RequestURI()
		} else {
			data["Path"] = "/"
		}
	}
	if user, ok := auth.UserFromContext(r.Context()); ok {
		data["User"] = user
		if _, set := data["Theme"]; !set {
			if prefs, err := models.GetPreferences(r.Context(), h.DB, user.ID); err == nil {
				data["Theme"] = prefs.Theme
			}
		}
	}
	token, err := auth.IssueCSRF(w, h.CookieSecure)
	if err != nil {
		h.Logger.Error("issue csrf failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	data["CSRFToken"] = token
	if err := templates.Render(w, status, page, data); err != nil {
		h.Logger.Error("render failed", "page", page, "err", err)
	}
}
