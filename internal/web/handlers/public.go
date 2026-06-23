package handlers

import (
	"bytes"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/export"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/sharing"
)

// shareDirectoryLimit caps contacts rendered in a public directory page.
const shareDirectoryLimit = 500

// ShareEntry is the public landing for a share link (GET /s/{token}). It
// dispatches by scope after the mode gate is satisfied, and counts the access.
func (h *Handlers) ShareEntry(w http.ResponseWriter, r *http.Request) {
	link, ok := h.resolveShareForView(w, r)
	if !ok {
		return
	}
	if err := models.IncrementShareUse(r.Context(), h.DB, link.ID); err != nil {
		h.Logger.Error("increment share use failed", "err", err)
	}

	switch link.Scope {
	case sharing.ScopeBook:
		h.shareDirectory(w, r, link)
	case sharing.ScopeContact:
		h.shareContact(w, r, link)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handlers) shareDirectory(w http.ResponseWriter, r *http.Request, link models.ShareLink) {
	book, err := models.GetAddressBookByID(r.Context(), h.DB, link.TargetID)
	if err != nil {
		h.shareTargetError(w, r, err)
		return
	}
	contacts, _, err := models.ListContacts(r.Context(), h.DB, book.ID, "", "", shareDirectoryLimit, 0)
	if err != nil {
		h.Logger.Error("share directory list failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	base := "/s/" + link.Token
	cards := buildContactCards(contacts,
		func(pid string) string { return base + "/c/" + pid },
		func(pid string) string { return base + "/c/" + pid + "/photo" })

	h.render(w, r, http.StatusOK, "share_directory.html", map[string]any{
		"Book":      book,
		"Cards":     cards,
		"ExportVCF": base + "/export.vcf",
		"ExportCSV": base + "/export.csv",
	})
}

func (h *Handlers) shareContact(w http.ResponseWriter, r *http.Request, link models.ShareLink) {
	contact, err := models.GetContactByID(r.Context(), h.DB, link.TargetID)
	if err != nil {
		h.shareTargetError(w, r, err)
		return
	}
	base := "/s/" + link.Token
	h.render(w, r, http.StatusOK, "share_contact.html", map[string]any{
		"Contact":     contact,
		"PhotoURL":    base + "/photo",
		"DownloadURL": base + "/export.vcf",
	})
}

// ShareContactInBook renders a single contact within a book share
// (GET /s/{token}/c/{contactPublicID}).
func (h *Handlers) ShareContactInBook(w http.ResponseWriter, r *http.Request) {
	link, contact, ok := h.resolveShareContact(w, r)
	if !ok {
		return
	}
	base := "/s/" + link.Token + "/c/" + contact.PublicID
	h.render(w, r, http.StatusOK, "share_contact.html", map[string]any{
		"Contact":  contact,
		"PhotoURL": base + "/photo",
		// Per-contact download is omitted in a book share; the directory offers
		// a whole-book export.
	})
}

// ShareBookContactPhoto serves a contact's photo within a book share
// (GET /s/{token}/c/{contactPublicID}/photo).
func (h *Handlers) ShareBookContactPhoto(w http.ResponseWriter, r *http.Request) {
	_, contact, ok := h.resolveShareContact(w, r)
	if !ok {
		return
	}
	h.serveSharePhoto(w, r, contact.PublicID)
}

// ShareContactPhoto serves the photo for a contact-scoped share
// (GET /s/{token}/photo).
func (h *Handlers) ShareContactPhoto(w http.ResponseWriter, r *http.Request) {
	link, ok := h.resolveShareForView(w, r)
	if !ok {
		return
	}
	if link.Scope != sharing.ScopeContact {
		http.NotFound(w, r)
		return
	}
	contact, err := models.GetContactByID(r.Context(), h.DB, link.TargetID)
	if err != nil {
		h.shareTargetError(w, r, err)
		return
	}
	h.serveSharePhoto(w, r, contact.PublicID)
}

func (h *Handlers) serveSharePhoto(w http.ResponseWriter, r *http.Request, contactPublicID string) {
	meta, found, err := models.GetContactPhotoMeta(r.Context(), h.DB, contactPublicID)
	if err != nil {
		h.Logger.Error("share photo meta failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	h.streamPhoto(w, r, contactPublicID, meta.ETag)
}

// ShareExportVCard exports the shared book or contact as vCard
// (GET /s/{token}/export.vcf).
func (h *Handlers) ShareExportVCard(w http.ResponseWriter, r *http.Request) {
	link, ok := h.resolveShareForView(w, r)
	if !ok {
		return
	}
	switch link.Scope {
	case sharing.ScopeBook:
		book, err := models.GetAddressBookByID(r.Context(), h.DB, link.TargetID)
		if err != nil {
			h.shareTargetError(w, r, err)
			return
		}
		h.writeBookVCard(w, r, book)
	case sharing.ScopeContact:
		contact, err := models.GetContactByID(r.Context(), h.DB, link.TargetID)
		if err != nil {
			h.shareTargetError(w, r, err)
			return
		}
		h.writeContactVCard(w, r, contact)
	default:
		http.NotFound(w, r)
	}
}

// ShareExportCSV exports a shared book as CSV (GET /s/{token}/export.csv).
func (h *Handlers) ShareExportCSV(w http.ResponseWriter, r *http.Request) {
	link, ok := h.resolveShareForView(w, r)
	if !ok {
		return
	}
	if link.Scope != sharing.ScopeBook {
		http.NotFound(w, r)
		return
	}
	book, err := models.GetAddressBookByID(r.Context(), h.DB, link.TargetID)
	if err != nil {
		h.shareTargetError(w, r, err)
		return
	}
	contacts, err := models.ListContactsForExport(r.Context(), h.DB, book.ID)
	if err != nil {
		h.exportError(w, "share csv list", err)
		return
	}
	rows := make([]export.CSVRow, 0, len(contacts))
	for _, c := range contacts {
		rows = append(rows, export.CSVRow{FullName: c.FullName, Org: c.Org, Email: c.Email, Phone: c.Phone})
	}
	var buf bytes.Buffer
	if err := export.WriteCSV(&buf, rows); err != nil {
		h.exportError(w, "share csv render", err)
		return
	}
	h.sendDownload(w, export.CSVMIME, downloadFilename(book.Name, "csv"), buf.Bytes())
}

// ShareGateSubmit verifies a gated link's secret (POST /s/{token}/gate).
func (h *Handlers) ShareGateSubmit(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	link, err := models.GetShareLinkByToken(r.Context(), h.DB, token)
	if err != nil || link.Mode != sharing.ModeGated || !link.Usable(time.Now()) {
		http.NotFound(w, r)
		return
	}
	if !h.checkForm(w, r) {
		return
	}

	if err := auth.VerifyPassword(link.SecretHash, r.PostFormValue("secret")); err != nil {
		if err := models.IncrementShareFailure(r.Context(), h.DB, link.ID); err != nil {
			h.Logger.Error("increment share failure failed", "err", err)
		}
		h.renderGate(w, r, token, "Incorrect secret.")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sharing.GateCookieName,
		Value:    h.Gate.Issue(token, time.Now()),
		Path:     "/s/" + token,
		HttpOnly: true,
		Secure:   h.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sharing.GateTTL.Seconds()),
	})
	http.Redirect(w, r, "/s/"+token, http.StatusSeeOther)
}

// resolveShareForView resolves the share for the current request and enforces
// its mode. It returns ok=false (having written a 404, a login redirect, or the
// gate page) when the caller must not proceed.
func (h *Handlers) resolveShareForView(w http.ResponseWriter, r *http.Request) (models.ShareLink, bool) {
	token := chi.URLParam(r, "token")
	link, err := models.GetShareLinkByToken(r.Context(), h.DB, token)
	if err != nil {
		if !errors.Is(err, models.ErrShareLinkNotFound) {
			h.Logger.Error("resolve share failed", "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return models.ShareLink{}, false
		}
		http.NotFound(w, r)
		return models.ShareLink{}, false
	}
	if !link.Usable(time.Now()) {
		http.NotFound(w, r)
		return models.ShareLink{}, false
	}

	switch link.Mode {
	case sharing.ModePublicLong:
		return link, true
	case sharing.ModeAuthenticated:
		if _, ok := auth.UserFromContext(r.Context()); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return models.ShareLink{}, false
		}
		return link, true
	case sharing.ModeGated:
		if cookie, err := r.Cookie(sharing.GateCookieName); err == nil && h.Gate.Valid(cookie.Value, token, time.Now()) {
			return link, true
		}
		h.renderGate(w, r, token, "")
		return models.ShareLink{}, false
	default:
		http.NotFound(w, r)
		return models.ShareLink{}, false
	}
}

// resolveShareContact resolves a book-scoped share and the {contactPublicID}
// within it, verifying the contact belongs to the shared book.
func (h *Handlers) resolveShareContact(w http.ResponseWriter, r *http.Request) (models.ShareLink, models.Contact, bool) {
	link, ok := h.resolveShareForView(w, r)
	if !ok {
		return models.ShareLink{}, models.Contact{}, false
	}
	if link.Scope != sharing.ScopeBook {
		http.NotFound(w, r)
		return models.ShareLink{}, models.Contact{}, false
	}
	contact, err := models.GetContactByPublicID(r.Context(), h.DB, chi.URLParam(r, "contactPublicID"))
	if err != nil || contact.AddressBookID != link.TargetID {
		http.NotFound(w, r)
		return models.ShareLink{}, models.Contact{}, false
	}
	return link, contact, true
}

func (h *Handlers) renderGate(w http.ResponseWriter, r *http.Request, token, errMsg string) {
	status := http.StatusOK
	if errMsg != "" {
		status = http.StatusUnauthorized
	}
	h.render(w, r, status, "gate.html", map[string]any{
		"GateAction": "/s/" + token + "/gate",
		"Error":      errMsg,
	})
}

// shareTargetError handles the rare case where a share's target row is missing
// (e.g. the book/contact was deleted): treat as not found.
func (h *Handlers) shareTargetError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, models.ErrAddressBookNotFound) || errors.Is(err, models.ErrContactNotFound) {
		http.NotFound(w, r)
		return
	}
	h.Logger.Error("share target load failed", "err", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}
