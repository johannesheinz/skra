package handlers

import (
	"net/http"

	"github.com/johannesheinz/skra/internal/i18n"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/web/templates"
)

// searchResultLimit caps a global search; a full result set surfaces a "refine"
// hint rather than silently truncating.
const searchResultLimit = 50

// searchData runs the global, RBAC-scoped contact search and returns the template
// data (Query/Cards/Truncated) shared by the inline results fragment and the host
// page (the dashboard and the address-book overview). Global search has no page of
// its own; it renders into whichever page carries the search box.
func (h *Handlers) searchData(r *http.Request, user models.User, query string) (map[string]any, error) {
	results, err := models.SearchContactsForUser(r.Context(), h.DB, user, query, searchResultLimit)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"Query":     query,
		"Cards":     buildSearchCards(results),
		"Truncated": len(results) == searchResultLimit,
	}, nil
}

// renderSearchFragment answers a host page's htmx live-search request by rendering
// just the results fragment. It reports whether it handled the request, so a host
// handler can `if h.renderSearchFragment(...) { return }` before its normal render.
func (h *Handlers) renderSearchFragment(w http.ResponseWriter, r *http.Request, user models.User) bool {
	if r.Header.Get("HX-Request") == "" {
		return false
	}
	data, err := h.searchData(r, user, r.URL.Query().Get("q"))
	if err != nil {
		h.Logger.Error("global search failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return true
	}
	if err := templates.RenderFragment(w, i18n.FromContext(r.Context()).Code, "search_results", data); err != nil {
		h.Logger.Error("render search fragment", "err", err)
	}
	return true
}
