package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/models"
)

// AdminUsersList lists all accounts (GET /admin/users).
func (h *Handlers) AdminUsersList(w http.ResponseWriter, r *http.Request) {
	users, err := models.ListUsers(r.Context(), h.DB)
	if err != nil {
		h.Logger.Error("list users failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.render(w, r, http.StatusOK, "admin_users_list.html", map[string]any{"Users": users})
}

// AdminUserNew renders the create form (GET /admin/users/new).
func (h *Handlers) AdminUserNew(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, "admin_user_form.html", map[string]any{
		"IsNew":      true,
		"FormAction": "/admin/users",
		"Role":       models.RoleUser,
	})
}

// AdminUserCreate creates an account with the chosen role (POST /admin/users).
func (h *Handlers) AdminUserCreate(w http.ResponseWriter, r *http.Request) {
	if !h.checkForm(w, r) {
		return
	}
	username := strings.TrimSpace(r.PostFormValue("username"))
	email := strings.TrimSpace(r.PostFormValue("email"))
	role := r.PostFormValue("role")
	password := r.PostFormValue("password")

	reRender := func(msg string) {
		h.render(w, r, http.StatusUnprocessableEntity, "admin_user_form.html", map[string]any{
			"IsNew": true, "FormAction": "/admin/users",
			"Username": username, "Email": email, "Role": role, "Error": msg,
		})
	}

	if username == "" || email == "" {
		reRender(h.tr(r).T("msg.username_email_required"))
		return
	}
	if role != models.RoleAdmin && role != models.RoleUser {
		reRender(h.tr(r).T("msg.invalid_role"))
		return
	}
	if len(password) < MinPasswordLen {
		reRender(h.tr(r).T("msg.password_min"))
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		h.Logger.Error("hash password failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if _, err := models.CreateUser(r.Context(), h.DB, username, email, hash, role); err != nil {
		reRender(h.tr(r).T("msg.username_or_email_in_use"))
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// AdminUserEdit renders the edit form (GET /admin/users/{publicID}/edit).
func (h *Handlers) AdminUserEdit(w http.ResponseWriter, r *http.Request) {
	user, ok := h.adminTargetUser(w, r)
	if !ok {
		return
	}
	h.renderAdminEdit(w, r, http.StatusOK, user, "")
}

// AdminUserUpdate updates a user's email and role (POST /admin/users/{publicID}/edit).
func (h *Handlers) AdminUserUpdate(w http.ResponseWriter, r *http.Request) {
	user, ok := h.adminTargetUser(w, r)
	if !ok {
		return
	}
	if !h.checkForm(w, r) {
		return
	}
	email := strings.TrimSpace(r.PostFormValue("email"))
	role := r.PostFormValue("role")
	if email == "" || (role != models.RoleAdmin && role != models.RoleUser) {
		h.renderAdminEdit(w, r, http.StatusUnprocessableEntity, user, h.tr(r).T("msg.email_role_required"))
		return
	}
	// Guard: do not demote the last admin.
	if user.Role == models.RoleAdmin && role != models.RoleAdmin {
		if h.isLastAdmin(w, r) {
			h.renderAdminEdit(w, r, http.StatusUnprocessableEntity, user, h.tr(r).T("msg.last_admin_demote"))
			return
		}
	}
	if err := models.UpdateUser(r.Context(), h.DB, user.ID, email, role); err != nil {
		h.Logger.Error("update user failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// AdminUserPassword resets a user's password (POST /admin/users/{publicID}/password).
func (h *Handlers) AdminUserPassword(w http.ResponseWriter, r *http.Request) {
	user, ok := h.adminTargetUser(w, r)
	if !ok {
		return
	}
	if !h.checkForm(w, r) {
		return
	}
	password := r.PostFormValue("password")
	if len(password) < MinPasswordLen {
		h.renderAdminEdit(w, r, http.StatusUnprocessableEntity, user, h.tr(r).T("msg.password_min"))
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		h.Logger.Error("hash password failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if err := models.UpdatePasswordHash(r.Context(), h.DB, user.ID, hash); err != nil {
		h.Logger.Error("reset password failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.renderAdminEdit(w, r, http.StatusOK, user, h.tr(r).T("msg.password_reset"))
}

// AdminUserDelete deletes a user (POST /admin/users/{publicID}/delete), with guards against self-delete, removing the last admin, and orphaning owned books.
func (h *Handlers) AdminUserDelete(w http.ResponseWriter, r *http.Request) {
	user, ok := h.adminTargetUser(w, r)
	if !ok {
		return
	}
	if !h.checkForm(w, r) {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if user.ID == current.ID {
		h.adminUsersError(w, r, h.tr(r).T("msg.delete_self"))
		return
	}
	if user.Role == models.RoleAdmin && h.isLastAdmin(w, r) {
		h.adminUsersError(w, r, h.tr(r).T("msg.last_admin_delete"))
		return
	}
	owns, err := models.OwnsAddressBooks(r.Context(), h.DB, user.ID)
	if err != nil {
		h.Logger.Error("check owned books failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if owns {
		h.adminUsersError(w, r, h.tr(r).T("msg.user_owns_books"))
		return
	}
	if err := models.DeleteUser(r.Context(), h.DB, user.ID); err != nil {
		h.adminUsersError(w, r, h.tr(r).T("msg.user_delete_failed"))
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// adminTargetUser resolves the {publicID} user for an admin action.
func (h *Handlers) adminTargetUser(w http.ResponseWriter, r *http.Request) (models.User, bool) {
	user, err := models.GetUserByPublicID(r.Context(), h.DB, chi.URLParam(r, "publicID"))
	if err != nil {
		if !errors.Is(err, models.ErrUserNotFound) {
			h.Logger.Error("get user failed", "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return models.User{}, false
		}
		http.NotFound(w, r)
		return models.User{}, false
	}
	return user, true
}

func (h *Handlers) isLastAdmin(w http.ResponseWriter, r *http.Request) bool {
	n, err := models.CountAdmins(r.Context(), h.DB)
	if err != nil {
		h.Logger.Error("count admins failed", "err", err)
		return false
	}
	return n <= 1
}

func (h *Handlers) renderAdminEdit(w http.ResponseWriter, r *http.Request, status int, user models.User, msg string) {
	h.render(w, r, status, "admin_user_form.html", map[string]any{
		"IsNew":      false,
		"FormAction": "/admin/users/" + user.PublicID + "/edit",
		"PublicID":   user.PublicID,
		"Username":   user.Username,
		"Email":      user.Email,
		"Role":       user.Role,
		"Notice":     msg,
	})
}

func (h *Handlers) adminUsersError(w http.ResponseWriter, r *http.Request, msg string) {
	users, err := models.ListUsers(r.Context(), h.DB)
	if err != nil {
		h.Logger.Error("list users failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.render(w, r, http.StatusUnprocessableEntity, "admin_users_list.html", map[string]any{
		"Users": users, "Error": msg,
	})
}
