package handlers

import (
	"bytes"
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
	contacts, err := models.ListContactsForExport(r.Context(), h.DB, book.ID)
	if err != nil {
		h.exportError(w, "list contacts for export", err)
		return
	}

	entries := make([]export.VCardEntry, 0, len(contacts))
	for _, c := range contacts {
		entry := export.VCardEntry{Raw: c.VCardRaw}
		if c.HasPhoto {
			if photo, ok, err := models.GetPhotoBytes(r.Context(), h.DB, c.ID); err != nil {
				h.exportError(w, "load photo for export", err)
				return
			} else if ok {
				entry.PhotoJPEG = photo
			}
		}
		entries = append(entries, entry)
	}

	var buf bytes.Buffer
	if err := export.WriteVCards(&buf, entries); err != nil {
		h.exportError(w, "render vcard", err)
		return
	}
	h.sendDownload(w, export.VCardMIME, downloadFilename(book.Name, "vcf"), buf.Bytes())
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

	var buf bytes.Buffer
	if err := export.WriteCSV(&buf, rows); err != nil {
		h.exportError(w, "render csv", err)
		return
	}
	h.sendDownload(w, export.CSVMIME, downloadFilename(book.Name, "csv"), buf.Bytes())
}

// ContactExportVCard streams a single contact as a vCard download
// (GET /contacts/{publicID}/export.vcf). Requires read on the contact's book.
func (h *Handlers) ContactExportVCard(w http.ResponseWriter, r *http.Request) {
	contact, _, _, ok := h.authorizeContact(w, r, rbac.Read)
	if !ok {
		return
	}
	exports, err := models.ListContactsForExport(r.Context(), h.DB, contact.AddressBookID)
	if err != nil {
		h.exportError(w, "load contact for export", err)
		return
	}
	var entry export.VCardEntry
	found := false
	for _, c := range exports {
		if c.ID == contact.ID {
			entry.Raw = c.VCardRaw
			if c.HasPhoto {
				if photo, ok, err := models.GetPhotoBytes(r.Context(), h.DB, c.ID); err == nil && ok {
					entry.PhotoJPEG = photo
				}
			}
			found = true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}

	var buf bytes.Buffer
	if err := export.WriteVCards(&buf, []export.VCardEntry{entry}); err != nil {
		h.exportError(w, "render vcard", err)
		return
	}
	h.sendDownload(w, export.VCardMIME, downloadFilename(contact.FullName, "vcf"), buf.Bytes())
}

func (h *Handlers) sendDownload(w http.ResponseWriter, mime, filename string, body []byte) {
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(body)
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
