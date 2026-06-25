package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/johannesheinz/skra/internal/export"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/rbac"
)

// BookExportVCard streams a book's contacts as a vCard download
// (GET /books/{publicID}/export.vcf). Requires read on the book.
func (h *Handlers) BookExportVCard(w http.ResponseWriter, r *http.Request) {
	book, _, ok := h.authorizeBook(w, r, rbac.Read)
	if !ok {
		return
	}
	h.writeBookVCard(w, r, book)
}

// writeBookVCard streams every contact in a book as a vCard download, embedding
// photos, loading each photo just-in-time so only one is held in memory at a
// time. Authorization is the caller's responsibility. The whole-book row set is
// fetched before any bytes are written, so a DB error there still becomes a 500;
// once streaming starts the status is already committed, so mid-stream photo
// errors are logged and the photo skipped rather than corrupting the download.
func (h *Handlers) writeBookVCard(w http.ResponseWriter, r *http.Request, book models.AddressBook) {
	contacts, err := models.ListContactsForExport(r.Context(), h.DB, book.ID)
	if err != nil {
		h.exportError(w, "list contacts for export", err)
		return
	}

	h.setDownloadHeaders(w, export.VCardMIME, downloadFilename(book.Name, "vcf"))
	for _, c := range contacts {
		entry := export.VCardEntry{Raw: c.VCardRaw}
		if c.HasPhoto {
			if photo, ok, err := models.GetPhotoBytes(r.Context(), h.DB, c.ID); err != nil {
				h.Logger.Error("export: load photo mid-stream", "contact", c.ID, "err", err)
			} else if ok {
				entry.PhotoJPEG = photo
			}
		}
		if err := export.WriteVCards(w, []export.VCardEntry{entry}); err != nil {
			h.Logger.Error("export: write vcard mid-stream", "contact", c.ID, "err", err)
			return
		}
	}
}

// BookExportCSV streams a book's contacts as a CSV download
// (GET /books/{publicID}/export.csv). Requires read on the book.
func (h *Handlers) BookExportCSV(w http.ResponseWriter, r *http.Request) {
	book, _, ok := h.authorizeBook(w, r, rbac.Read)
	if !ok {
		return
	}
	contacts, err := models.ListContactsForExport(r.Context(), h.DB, book.ID)
	if err != nil {
		h.exportError(w, "list contacts for export", err)
		return
	}

	rows := make([]export.CSVRow, 0, len(contacts))
	for _, c := range contacts {
		rows = append(rows, export.CSVRow{FullName: c.FullName, Org: c.Org, Email: c.Email, Phone: c.Phone})
	}

	// CSV rows carry no BLOBs, so stream straight to the response.
	h.setDownloadHeaders(w, export.CSVMIME, downloadFilename(book.Name, "csv"))
	if err := export.WriteCSV(w, rows); err != nil {
		h.Logger.Error("export: write csv mid-stream", "err", err)
	}
}

// ContactExportVCard streams a single contact as a vCard download
// (GET /contacts/{publicID}/export.vcf). Requires read on the contact's book.
func (h *Handlers) ContactExportVCard(w http.ResponseWriter, r *http.Request) {
	contact, _, _, ok := h.authorizeContact(w, r, rbac.Read)
	if !ok {
		return
	}
	h.writeContactVCard(w, r, contact)
}

// writeContactVCard renders one contact as a vCard download, embedding its photo
// if present. Authorization is the caller's responsibility.
func (h *Handlers) writeContactVCard(w http.ResponseWriter, r *http.Request, contact models.Contact) {
	c, err := models.GetContactExport(r.Context(), h.DB, contact.ID)
	if errors.Is(err, models.ErrContactNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.exportError(w, "load contact for export", err)
		return
	}
	entry := export.VCardEntry{Raw: c.VCardRaw}
	if c.HasPhoto {
		if photo, ok, err := models.GetPhotoBytes(r.Context(), h.DB, c.ID); err != nil {
			h.exportError(w, "load photo for export", err)
			return
		} else if ok {
			entry.PhotoJPEG = photo
		}
	}

	h.setDownloadHeaders(w, export.VCardMIME, downloadFilename(contact.FullName, "vcf"))
	if err := export.WriteVCards(w, []export.VCardEntry{entry}); err != nil {
		h.Logger.Error("export: write contact vcard", "contact", c.ID, "err", err)
	}
}

func (h *Handlers) setDownloadHeaders(w http.ResponseWriter, mime, filename string) {
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (h *Handlers) exportError(w http.ResponseWriter, what string, err error) {
	h.Logger.Error("export failed", "step", what, "err", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

// downloadFilename builds a safe download filename from a title, replacing any
// character outside [A-Za-z0-9-_] with an underscore and defaulting to "skra".
func downloadFilename(title, ext string) string {
	var b strings.Builder
	for _, r := range title {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	name := strings.Trim(b.String(), "_")
	if name == "" {
		name = "skra"
	}
	return name + "." + ext
}
