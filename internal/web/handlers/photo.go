package handlers

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/rbac"
)

// ContactPhoto serves a contact's photo with a strong ETag and conditional-GET support (GET /contacts/{publicID}/photo).
// Authorization inherits the contact's address book; anything the user may not see returns 404, never 403.
func (h *Handlers) ContactPhoto(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.NotFound(w, r)
		return
	}
	publicID := chi.URLParam(r, "publicID")

	meta, found, err := models.GetContactPhotoMeta(r.Context(), h.DB, publicID)
	if err != nil {
		h.Logger.Error("photo meta lookup failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}

	decision, err := rbac.Can(r.Context(), h.DB, user, meta.AddressBookID, rbac.Read)
	if err != nil {
		h.Logger.Error("photo authorization failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if !decision.Allow {
		// Covers both invisible (no grant) and any non-read state: 404, not 403.
		http.NotFound(w, r)
		return
	}
	h.streamPhoto(w, r, publicID, meta.ETag)
}

// streamPhoto writes a contact's photo with a strong ETag, conditional-GET support, and a private cache.
// The caller is responsible for authorization; publicID/etag come from a prior metadata lookup.
func (h *Handlers) streamPhoto(w http.ResponseWriter, r *http.Request, publicID, rawETag string) {
	etag := `"` + rawETag + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=300")
	if matchesETag(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	photo, found, err := models.GetContactPhoto(r.Context(), h.DB, publicID)
	if err != nil {
		h.Logger.Error("photo bytes lookup failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", photo.MIMEType)
	_, _ = w.Write(photo.Bytes)
}

// matchesETag reports whether an If-None-Match header matches the resource ETag.
// Supports the "*" wildcard and a comma-separated list of candidate tags.
func matchesETag(ifNoneMatch, etag string) bool {
	if ifNoneMatch == "" {
		return false
	}
	if strings.TrimSpace(ifNoneMatch) == "*" {
		return true
	}
	for _, candidate := range strings.Split(ifNoneMatch, ",") {
		if strings.TrimSpace(candidate) == etag {
			return true
		}
	}
	return false
}
