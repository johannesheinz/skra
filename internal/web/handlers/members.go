package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/rbac"
)

type memberView struct {
	Username  string
	Email     string
	Level     string
	RevokeURL string
	IsOwner   bool
}

// BookMembers lists a book's members and the grant forms (manager+).
func (h *Handlers) BookMembers(w http.ResponseWriter, r *http.Request) {
	book, _, ok := h.authorizeBook(w, r, rbac.Write)
	if !ok {
		return
	}
	h.renderMembers(w, r, http.StatusOK, book, "")
}

// BookMemberAdd grants an existing user (by username) access to the book.
func (h *Handlers) BookMemberAdd(w http.ResponseWriter, r *http.Request) {
	book, current, ok := h.authorizeBook(w, r, rbac.Write)
	if !ok {
		return
	}
	if !h.checkForm(w, r) {
		return
	}
	username := strings.TrimSpace(r.PostFormValue("username"))
	level := r.PostFormValue("level")
	if !models.ValidAccessLevel(level) {
		h.renderMembers(w, r, http.StatusUnprocessableEntity, book, "Choose a valid access level.")
		return
	}
	user, err := models.GetUserByUsername(r.Context(), h.DB, username)
	if err != nil {
		if errors.Is(err, models.ErrUserNotFound) {
			h.renderMembers(w, r, http.StatusUnprocessableEntity, book, "No user with that username.")
			return
		}
		h.Logger.Error("member lookup failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if err := models.AddOrUpdateMember(r.Context(), h.DB, book.ID, user.ID, level, current.ID); err != nil {
		h.Logger.Error("add member failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/books/"+book.PublicID+"/members", http.StatusSeeOther)
}

// BookMemberCreate creates a new account and grants it access to this book.
// The new account is always a normal user — a manager cannot mint admins.
func (h *Handlers) BookMemberCreate(w http.ResponseWriter, r *http.Request) {
	book, current, ok := h.authorizeBook(w, r, rbac.Write)
	if !ok {
		return
	}
	if !h.checkForm(w, r) {
		return
	}
	username := strings.TrimSpace(r.PostFormValue("username"))
	email := strings.TrimSpace(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	level := r.PostFormValue("level")

	switch {
	case username == "" || email == "":
		h.renderMembers(w, r, http.StatusUnprocessableEntity, book, "Username and email are required.")
		return
	case !models.ValidAccessLevel(level):
		h.renderMembers(w, r, http.StatusUnprocessableEntity, book, "Choose a valid access level.")
		return
	case len(password) < MinPasswordLen:
		h.renderMembers(w, r, http.StatusUnprocessableEntity, book, "Password must be at least 8 characters.")
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		h.Logger.Error("hash password failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	user, err := models.CreateUser(r.Context(), h.DB, username, email, hash, models.RoleUser)
	if err != nil {
		// Collisions are reported generically; managers must create new accounts.
		h.renderMembers(w, r, http.StatusUnprocessableEntity, book, "That username or email is already taken.")
		return
	}
	if err := models.AddOrUpdateMember(r.Context(), h.DB, book.ID, user.ID, level, current.ID); err != nil {
		h.Logger.Error("grant new member failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/books/"+book.PublicID+"/members", http.StatusSeeOther)
}

// BookMemberRevoke removes a member (POST .../members/{userPublicID}/revoke).
// The owner's own membership cannot be removed.
func (h *Handlers) BookMemberRevoke(w http.ResponseWriter, r *http.Request) {
	book, _, ok := h.authorizeBook(w, r, rbac.Write)
	if !ok {
		return
	}
	if !h.checkForm(w, r) {
		return
	}
	user, err := models.GetUserByPublicID(r.Context(), h.DB, chi.URLParam(r, "userPublicID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if user.ID == book.OwnerID {
		h.renderMembers(w, r, http.StatusUnprocessableEntity, book, "The book owner's access cannot be removed.")
		return
	}
	if err := models.RemoveMember(r.Context(), h.DB, book.ID, user.ID); err != nil {
		h.Logger.Error("remove member failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/books/"+book.PublicID+"/members", http.StatusSeeOther)
}

func (h *Handlers) renderMembers(w http.ResponseWriter, r *http.Request, status int, book models.AddressBook, errMsg string) {
	members, err := models.ListMembers(r.Context(), h.DB, book.ID)
	if err != nil {
		h.Logger.Error("list members failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	base := "/books/" + book.PublicID
	views := make([]memberView, 0, len(members))
	for _, m := range members {
		views = append(views, memberView{
			Username:  m.User.Username,
			Email:     m.User.Email,
			Level:     m.AccessLevel,
			RevokeURL: base + "/members/" + m.User.PublicID + "/revoke",
			IsOwner:   m.User.ID == book.OwnerID,
		})
	}
	h.render(w, r, status, "members.html", map[string]any{
		"Book":      book,
		"BackURL":   base,
		"AddURL":    base + "/members",
		"CreateURL": base + "/members/new",
		"Members":   views,
		"Error":     errMsg,
	})
}
