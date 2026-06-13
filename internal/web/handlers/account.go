package handlers

import (
	"net/http"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/models"
)

// AccountPasswordForm renders the self-service password change form
// (GET /account/password).
func (h *Handlers) AccountPasswordForm(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, "account_password.html", nil)
}

// AccountPasswordUpdate changes the logged-in user's password after verifying
// the current one (POST /account/password).
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
		h.render(w, r, http.StatusUnauthorized, "account_password.html", map[string]any{"Error": "Current password is incorrect."})
		return
	}
	if len(next) < MinPasswordLen {
		h.render(w, r, http.StatusUnprocessableEntity, "account_password.html", map[string]any{"Error": "New password must be at least 8 characters."})
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
	h.render(w, r, http.StatusOK, "account_password.html", map[string]any{"Notice": "Password changed."})
}
