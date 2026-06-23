package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/models"
)

// accent swatch presets offered on the appearance form.
type accentOption struct {
	Value string
	Label string
	Group string
}

var accentOptions = []accentOption{
	{"", "Default", "Brand"},
	{"slate", "Slate", "Brand"},
	{"indigo", "Indigo", "Brand"},
	{"lime", "Lime", "Neon"},
	{"magenta", "Magenta", "Neon"},
	{"cyan", "Cyan", "Neon"},
	{"rose", "Rose", "Pastel"},
	{"peach", "Peach", "Pastel"},
	{"lavender", "Lavender", "Pastel"},
}

func validAccent(v string) bool {
	for _, a := range accentOptions {
		if a.Value == v {
			return true
		}
	}
	return false
}

// AccountPage renders the account settings page (GET /account).
func (h *Handlers) AccountPage(w http.ResponseWriter, r *http.Request) {
	h.renderAccount(w, r, http.StatusOK, nil)
}

// AccountProfileUpdate updates the signed-in user's email (POST /account/profile).
func (h *Handlers) AccountProfileUpdate(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !h.checkForm(w, r) {
		return
	}
	email := strings.TrimSpace(r.PostFormValue("email"))
	if email == "" {
		h.renderAccount(w, r, http.StatusUnprocessableEntity, map[string]any{"ProfileError": "Email is required."})
		return
	}
	if err := models.UpdateEmail(r.Context(), h.DB, user.ID, email); err != nil {
		h.renderAccount(w, r, http.StatusUnprocessableEntity, map[string]any{"ProfileError": "That email is already in use."})
		return
	}
	h.renderAccount(w, r, http.StatusOK, map[string]any{"ProfileNotice": "Profile updated.", "EmailOverride": email})
}

// AccountAppearanceUpdate saves the theme preferences (POST /account/appearance).
func (h *Handlers) AccountAppearanceUpdate(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !h.checkForm(w, r) {
		return
	}
	mode := r.PostFormValue("mode")
	flavor := r.PostFormValue("flavor")
	accent := r.PostFormValue("accent")
	if (mode != "" && mode != "light" && mode != "dark") ||
		(flavor != "" && flavor != "solarized") || !validAccent(accent) {
		h.renderAccount(w, r, http.StatusUnprocessableEntity, map[string]any{"AppearanceError": "Invalid theme selection."})
		return
	}
	prefs := models.UIPreferences{Theme: models.ThemePrefs{Mode: mode, Flavor: flavor, Accent: accent}}
	if err := models.UpdatePreferences(r.Context(), h.DB, user.ID, prefs); err != nil {
		h.Logger.Error("update preferences failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.renderAccount(w, r, http.StatusOK, map[string]any{"AppearanceNotice": "Appearance saved."})
}

// AccountPasswordUpdate changes the signed-in user's password (POST /account/password).
func (h *Handlers) AccountPasswordUpdate(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !h.checkForm(w, r) {
		return
	}
	current := r.PostFormValue("current_password")
	next := r.PostFormValue("new_password")

	if err := auth.VerifyPassword(user.PasswordHash, current); err != nil {
		h.renderAccount(w, r, http.StatusUnauthorized, map[string]any{"PasswordError": "Current password is incorrect."})
		return
	}
	if len(next) < MinPasswordLen {
		h.renderAccount(w, r, http.StatusUnprocessableEntity, map[string]any{"PasswordError": "New password must be at least 8 characters."})
		return
	}
	hash, err := auth.HashPassword(next)
	if err != nil {
		h.Logger.Error("hash password failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if err := models.UpdatePasswordHash(r.Context(), h.DB, user.ID, hash); err != nil {
		h.Logger.Error("update password failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.renderAccount(w, r, http.StatusOK, map[string]any{"PasswordNotice": "Password changed."})
}

// AccountListPrefsUpdate persists the contact-list page size and sort chosen
// from the controls on a book page (POST /account/list-prefs), then returns to
// that page. Values are validated against the allowed sets; anything else is
// coerced to the default. The theme block is preserved.
func (h *Handlers) AccountListPrefsUpdate(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !h.checkForm(w, r) {
		return
	}
	size := 0 // 0 = default
	if s, err := strconv.Atoi(r.PostFormValue("size")); err == nil {
		for _, a := range models.AllowedPageSizes {
			if a == s {
				size = s
			}
		}
	}
	sort := ""
	for _, o := range models.AllowedSorts {
		if o.Key == r.PostFormValue("sort") {
			sort = o.Key
		}
	}
	prefs, err := models.GetPreferences(r.Context(), h.DB, user.ID)
	if err != nil {
		h.Logger.Error("get preferences failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	prefs.List = models.ListPrefs{PageSize: size, Sort: sort}
	if err := models.UpdatePreferences(r.Context(), h.DB, user.ID, prefs); err != nil {
		h.Logger.Error("update preferences failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, safeReturnPath(r.PostFormValue("return")), http.StatusSeeOther)
}

// AccountThemeToggle flips the saved light/dark mode from the header control
// (POST /account/theme) and returns to the current page. Persisting to the
// account keeps the toggle and the Appearance settings consistent.
func (h *Handlers) AccountThemeToggle(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !h.checkForm(w, r) {
		return
	}
	prefs, err := models.GetPreferences(r.Context(), h.DB, user.ID)
	if err != nil {
		h.Logger.Error("get preferences failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if prefs.Theme.Mode == "dark" {
		prefs.Theme.Mode = "light"
	} else {
		prefs.Theme.Mode = "dark"
	}
	if err := models.UpdatePreferences(r.Context(), h.DB, user.ID, prefs); err != nil {
		h.Logger.Error("update preferences failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	// Return to the page the toggle was clicked on. Referer is unusable because
	// the app sends Referrer-Policy: no-referrer, so the current path is passed
	// explicitly and validated to a same-origin path to avoid an open redirect.
	http.Redirect(w, r, safeReturnPath(r.PostFormValue("return")), http.StatusSeeOther)
}

// safeReturnPath accepts only an absolute, non-protocol-relative path; anything
// else falls back to "/".
func safeReturnPath(p string) string {
	if strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") {
		return p
	}
	return "/"
}

// renderAccount renders the settings page with the user's profile, memberships,
// and current theme; extra carries per-section notices/errors.
func (h *Handlers) renderAccount(w http.ResponseWriter, r *http.Request, status int, extra map[string]any) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	prefs, err := models.GetPreferences(r.Context(), h.DB, user.ID)
	if err != nil {
		h.Logger.Error("get preferences failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	memberships, err := models.ListMembershipsForUser(r.Context(), h.DB, user.ID)
	if err != nil {
		h.Logger.Error("list memberships failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	data := map[string]any{
		"Email":       user.Email,
		"Memberships": memberships,
		"Theme":       prefs.Theme,
		"Accents":     accentOptions,
		// The account page is always reachable via GET, even when this render is
		// the result of a POST, so the header toggle returns here.
		"Path": "/account",
	}
	for k, v := range extra {
		data[k] = v
	}
	if override, ok := data["EmailOverride"]; ok {
		data["Email"] = override
	}
	h.render(w, r, status, "account.html", data)
}
