package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/rbac"
	"github.com/johannesheinz/skra/internal/sharing"
)

// shareView is a share link rendered for the management list.
type shareView struct {
	ID        int64
	Mode      string
	URL       string
	Status    string
	UseCount  int64
	MaxUses   int64
	RevokeURL string
}

// --- Book shares ---

func (h *Handlers) BookShares(w http.ResponseWriter, r *http.Request) {
	book, _, ok := h.authorizeBook(w, r, rbac.Write)
	if !ok {
		return
	}
	h.renderShares(w, r, http.StatusOK, book.Name, "/books/"+book.PublicID, sharing.ScopeBook, book.ID, "")
}

func (h *Handlers) BookShareCreate(w http.ResponseWriter, r *http.Request) {
	book, _, ok := h.authorizeBook(w, r, rbac.Write)
	if !ok {
		return
	}
	h.createShare(w, r, book.Name, "/books/"+book.PublicID, sharing.ScopeBook, book.ID)
}

func (h *Handlers) BookShareRevoke(w http.ResponseWriter, r *http.Request) {
	book, _, ok := h.authorizeBook(w, r, rbac.Write)
	if !ok {
		return
	}
	h.revokeShare(w, r, "/books/"+book.PublicID, sharing.ScopeBook, book.ID)
}

// --- Contact shares ---

func (h *Handlers) ContactShares(w http.ResponseWriter, r *http.Request) {
	contact, _, _, ok := h.authorizeContact(w, r, rbac.Write)
	if !ok {
		return
	}
	h.renderShares(w, r, http.StatusOK, contact.FullName, "/contacts/"+contact.PublicID, sharing.ScopeContact, contact.ID, "")
}

func (h *Handlers) ContactShareCreate(w http.ResponseWriter, r *http.Request) {
	contact, _, _, ok := h.authorizeContact(w, r, rbac.Write)
	if !ok {
		return
	}
	h.createShare(w, r, contact.FullName, "/contacts/"+contact.PublicID, sharing.ScopeContact, contact.ID)
}

func (h *Handlers) ContactShareRevoke(w http.ResponseWriter, r *http.Request) {
	contact, _, _, ok := h.authorizeContact(w, r, rbac.Write)
	if !ok {
		return
	}
	h.revokeShare(w, r, "/contacts/"+contact.PublicID, sharing.ScopeContact, contact.ID)
}

// --- Shared implementation ---

func (h *Handlers) renderShares(w http.ResponseWriter, r *http.Request, status int, title, backURL, scope string, targetID int64, errMsg string) {
	links, err := models.ListShareLinksForTarget(r.Context(), h.DB, scope, targetID)
	if err != nil {
		h.Logger.Error("list share links failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	sharesURL := backURL + "/shares"
	now := time.Now()
	views := make([]shareView, 0, len(links))
	for _, l := range links {
		views = append(views, shareView{
			ID:        l.ID,
			Mode:      l.Mode,
			URL:       h.ExternalURL + "/s/" + l.Token,
			Status:    shareStatus(l, now),
			UseCount:  l.UseCount,
			MaxUses:   l.MaxUses,
			RevokeURL: sharesURL + "/" + strconv.FormatInt(l.ID, 10) + "/revoke",
		})
	}
	h.render(w, r, status, "shares.html", map[string]any{
		"Title":     title,
		"BackURL":   backURL,
		"CreateURL": sharesURL,
		"Links":     views,
		"Modes":     []string{sharing.ModeAuthenticated, sharing.ModePublicLong, sharing.ModeGated},
		"GatedMode": sharing.ModeGated,
		"Error":     errMsg,
	})
}

func (h *Handlers) createShare(w http.ResponseWriter, r *http.Request, title, backURL, scope string, targetID int64) {
	if !h.checkForm(w, r) {
		return
	}
	user, _ := auth.UserFromContext(r.Context())

	params, errMsg := parseShareForm(r, scope, targetID, user.ID)
	if errMsg != "" {
		h.renderShares(w, r, http.StatusUnprocessableEntity, title, backURL, scope, targetID, errMsg)
		return
	}
	if params.Mode == sharing.ModeGated {
		hash, err := auth.HashPassword(r.PostFormValue("secret"))
		if err != nil {
			h.Logger.Error("hash share secret failed", "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		params.SecretHash = hash
	}
	if _, err := models.CreateShareLink(r.Context(), h.DB, params); err != nil {
		h.renderShares(w, r, http.StatusUnprocessableEntity, title, backURL, scope, targetID, "Could not create share link.")
		return
	}
	http.Redirect(w, r, backURL+"/shares", http.StatusSeeOther)
}

func (h *Handlers) revokeShare(w http.ResponseWriter, r *http.Request, backURL, scope string, targetID int64) {
	if !h.checkForm(w, r) {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "shareID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Confirm the link belongs to this target before revoking, so a manager of
	// one book cannot revoke another's link by guessing ids.
	links, err := models.ListShareLinksForTarget(r.Context(), h.DB, scope, targetID)
	if err != nil {
		h.Logger.Error("list share links failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	owned := false
	for _, l := range links {
		if l.ID == id {
			owned = true
			break
		}
	}
	if !owned {
		http.NotFound(w, r)
		return
	}
	if err := models.RevokeShareLink(r.Context(), h.DB, id); err != nil {
		h.Logger.Error("revoke share failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, backURL+"/shares", http.StatusSeeOther)
}

// parseShareForm validates the creation form and returns the params or a
// user-facing error message.
func parseShareForm(r *http.Request, scope string, targetID, createdBy int64) (models.NewShareLinkParams, string) {
	mode := r.PostFormValue("mode")
	if !sharing.ValidMode(mode) {
		return models.NewShareLinkParams{}, "Choose a valid share mode."
	}
	if mode == sharing.ModeGated && r.PostFormValue("secret") == "" {
		return models.NewShareLinkParams{}, "A gated link requires a secret."
	}

	var maxUses int64
	if raw := r.PostFormValue("max_uses"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			return models.NewShareLinkParams{}, "Max uses must be a non-negative number."
		}
		maxUses = n
	}

	var expiresAt string
	if raw := r.PostFormValue("expires"); raw != "" {
		day, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return models.NewShareLinkParams{}, "Expiry must be a valid date."
		}
		expiresAt = time.Date(day.Year(), day.Month(), day.Day(), 23, 59, 59, 0, time.UTC).Format("2006-01-02 15:04:05")
	}

	return models.NewShareLinkParams{
		Mode: mode, Scope: scope, TargetID: targetID, MaxUses: maxUses, ExpiresAt: expiresAt, CreatedBy: createdBy,
	}, ""
}

func shareStatus(l models.ShareLink, now time.Time) string {
	switch {
	case l.Revoked:
		return "revoked"
	case l.FailedCount >= sharing.GateMaxFailures:
		return "locked"
	case l.MaxUses > 0 && l.UseCount >= l.MaxUses:
		return "exhausted"
	case !l.Usable(now):
		return "expired"
	default:
		return "active"
	}
}
