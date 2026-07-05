package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/models"
)

// LoginForm renders the login page (GET /login).
// An already-authenticated user is redirected home.
func (h *Handlers) LoginForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.UserFromContext(r.Context()); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	h.renderLogin(w, r, http.StatusOK, "", "")
}

// Login authenticates a username/password submission (POST /login).
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := auth.VerifyCSRF(r); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	username := r.PostFormValue("username")
	password := r.PostFormValue("password")

	user, err := models.GetUserByUsername(r.Context(), h.DB, username)
	if err != nil {
		// Spend the same work on an unknown user to avoid timing enumeration.
		_ = auth.VerifyPassword(h.dummyHash, password)
		if !errors.Is(err, models.ErrUserNotFound) {
			h.Logger.Error("login user lookup failed", "err", err)
		}
		h.renderLogin(w, r, http.StatusUnauthorized, username, h.tr(r).T("msg.login_failed"))
		return
	}

	if err := auth.VerifyPassword(user.PasswordHash, password); err != nil {
		h.renderLogin(w, r, http.StatusUnauthorized, username, h.tr(r).T("msg.login_failed"))
		return
	}

	h.rehashIfNeeded(r, user, password)

	sessionID, err := h.Sessions.Create(r.Context(), user.ID)
	if err != nil {
		h.Logger.Error("create session failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, auth.NewSessionCookie(sessionID, h.CookieSecure))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// Logout destroys the session and clears the cookie (POST /logout).
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := auth.VerifyCSRF(r); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil && cookie.Value != "" {
		if err := h.Sessions.Delete(r.Context(), cookie.Value); err != nil {
			h.Logger.Error("delete session failed", "err", err)
		}
	}
	http.SetCookie(w, auth.ClearSessionCookie(h.CookieSecure))
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// Home is the authenticated landing dashboard (GET /): the user's books with contact counts, a total, quick actions, and recently added contacts.
func (h *Handlers) Home(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if h.renderSearchFragment(w, r, user) {
		return
	}
	books, err := models.ListAddressBooks(r.Context(), h.DB, user)
	if err != nil {
		h.Logger.Error("dashboard books failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	total := 0
	for _, b := range books {
		total += b.ContactCount
	}
	recent, err := models.RecentContactsForUser(r.Context(), h.DB, user, 8)
	if err != nil {
		h.Logger.Error("dashboard recent failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	birthdays, err := models.UpcomingBirthdaysForUser(r.Context(), h.DB, user, 5)
	if err != nil {
		h.Logger.Error("dashboard birthdays failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	data := map[string]any{
		"Books":         books,
		"BookCount":     len(books),
		"TotalContacts": total,
		"Recent":        recent,
		"Birthdays":     birthdays,
	}
	// No-JS (or a reload of a pushed ?q= URL): render the dashboard with the
	// search results inline. The htmx path is handled by renderSearchFragment above.
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		sd, err := h.searchData(r, user, q)
		if err != nil {
			h.Logger.Error("dashboard search failed", "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		for k, v := range sd {
			data[k] = v
		}
	}
	h.render(w, r, http.StatusOK, "home.html", data)
}

func (h *Handlers) rehashIfNeeded(r *http.Request, user models.User, password string) {
	need, err := auth.NeedsRehash(user.PasswordHash)
	if err != nil || !need {
		return
	}
	newHash, err := auth.HashPassword(password)
	if err != nil {
		h.Logger.Error("rehash failed", "err", err)
		return
	}
	if err := models.UpdatePasswordHash(r.Context(), h.DB, user.ID, newHash); err != nil {
		h.Logger.Error("persist rehash failed", "err", err)
	}
}

func (h *Handlers) renderLogin(w http.ResponseWriter, r *http.Request, status int, username, errMsg string) {
	h.render(w, r, status, "login.html", map[string]any{
		"Username": username,
		"Error":    errMsg,
	})
}
