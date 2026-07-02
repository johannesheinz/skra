package handlers

import (
	"net/http"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/i18n"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/web/templates"
)

// searchResultLimit caps a global search; a full result set surfaces a "refine"
// hint rather than silently truncating.
const searchResultLimit = 50

// Search runs a global, RBAC-scoped contact search across every book the user may
// see (GET /search?q=). It renders the full page normally and, for htmx requests
// (HX-Request), just the results fragment so the home-page live search swaps in
// place without a reload.
func (h *Handlers) Search(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	query := r.URL.Query().Get("q")
	results, err := models.SearchContactsForUser(r.Context(), h.DB, user, query, searchResultLimit)
	if err != nil {
		h.Logger.Error("global search failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	data := map[string]any{
		"Query":     query,
		"Cards":     buildSearchCards(results),
		"Truncated": len(results) == searchResultLimit,
	}
	if r.Header.Get("HX-Request") != "" {
		if err := templates.RenderFragment(w, i18n.FromContext(r.Context()).Code, "search_results", data); err != nil {
			h.Logger.Error("render search fragment", "err", err)
		}
		return
	}
	h.render(w, r, http.StatusOK, "search.html", data)
}
