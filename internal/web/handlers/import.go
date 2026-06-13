package handlers

import (
	"io"
	"net/http"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/ids"
	"github.com/johannesheinz/skra/internal/images"
	"github.com/johannesheinz/skra/internal/importing"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/rbac"
)

const (
	importActionSkip      = "skip"
	importActionCreateNew = "create_new"
)

type importRow struct {
	Name   string
	Email  string
	Status string
}

// ImportForm renders the import upload form (GET /books/{publicID}/import).
func (h *Handlers) ImportForm(w http.ResponseWriter, r *http.Request) {
	book, _, ok := h.authorizeBook(w, r, rbac.Write)
	if !ok {
		return
	}
	h.render(w, r, http.StatusOK, "import_form.html", map[string]any{
		"Book":      book,
		"UploadURL": "/books/" + book.PublicID + "/import",
	})
}

// ImportUpload parses an uploaded vCard file, stages it, and shows a dry-run
// preview (POST /books/{publicID}/import).
func (h *Handlers) ImportUpload(w http.ResponseWriter, r *http.Request) {
	book, user, ok := h.authorizeBook(w, r, rbac.Write)
	if !ok {
		return
	}
	_ = models.DeleteStaleImportUploads(r.Context(), h.DB)

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadMemory); err != nil {
		http.Error(w, "upload too large or malformed", http.StatusRequestEntityTooLarge)
		return
	}
	if err := auth.VerifyCSRF(r); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		h.importFormError(w, r, book, "Choose a .vcf file to import.")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		h.Logger.Error("read import upload failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	records, malformed := importing.ParseVCards(data)
	if len(records) == 0 {
		h.importFormError(w, r, book, "No contacts found in that file.")
		return
	}

	uids, emails, err := models.ExistingContactKeys(r.Context(), h.DB, book.ID)
	if err != nil {
		h.Logger.Error("existing keys failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	classified, summary := importing.Analyze(records, uids, emails)

	token, err := models.CreateImportUpload(r.Context(), h.DB, book.ID, user.ID, "vcard", data)
	if err != nil {
		h.Logger.Error("stage import failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	rows := make([]importRow, 0, len(classified))
	for _, c := range classified {
		status := "new"
		if c.Duplicate {
			status = "duplicate"
		}
		rows = append(rows, importRow{Name: c.Record.FullName, Email: c.Record.Email, Status: status})
	}

	h.render(w, r, http.StatusOK, "import_preview.html", map[string]any{
		"Book":      book,
		"CommitURL": "/books/" + book.PublicID + "/import/commit",
		"CancelURL": "/books/" + book.PublicID,
		"Token":     token,
		"New":       summary.New,
		"Duplicate": summary.Duplicate,
		"Malformed": malformed,
		"Total":     len(records),
		"Rows":      rows,
	})
}

// ImportCommit applies a staged import (POST /books/{publicID}/import/commit).
func (h *Handlers) ImportCommit(w http.ResponseWriter, r *http.Request) {
	book, _, ok := h.authorizeBook(w, r, rbac.Write)
	if !ok {
		return
	}
	if !h.checkForm(w, r) {
		return
	}
	token := r.PostFormValue("token")
	action := r.PostFormValue("action")
	if action != importActionSkip && action != importActionCreateNew {
		action = importActionSkip
	}

	upload, err := models.GetImportUpload(r.Context(), h.DB, token)
	if err != nil || upload.BookID != book.ID {
		http.NotFound(w, r)
		return
	}

	records, _ := importing.ParseVCards(upload.Bytes)
	uids, emails, err := models.ExistingContactKeys(r.Context(), h.DB, book.ID)
	if err != nil {
		h.Logger.Error("existing keys failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	classified, _ := importing.Analyze(records, uids, emails)

	usedUIDs := make(map[string]bool, len(uids))
	for u := range uids {
		usedUIDs[u] = true
	}

	var prepared []models.PreparedImport
	skipped := 0
	for _, c := range classified {
		if action == importActionSkip && c.Duplicate {
			skipped++
			continue
		}
		rec := c.Record
		uid, raw := rec.UID, rec.CanonicalRaw
		if uid == "" || usedUIDs[uid] {
			minted, err := ids.Random(16)
			if err != nil {
				h.Logger.Error("mint uid failed", "err", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			uid = minted
			raw = importing.BuildCanonicalRaw(rec.FullName, rec.Org, rec.Email, rec.Phone, uid)
		}
		usedUIDs[uid] = true

		var jpeg []byte
		if len(rec.PhotoData) > 0 {
			if processed, err := images.Process(rec.PhotoData); err == nil {
				jpeg = processed
			}
		}
		prepared = append(prepared, models.PreparedImport{
			FullName: rec.FullName, Org: rec.Org, Email: rec.Email, Phone: rec.Phone,
			VCardRaw: raw, UID: uid, PhotoJPEG: jpeg,
		})
	}

	inserted, err := models.ImportContacts(r.Context(), h.DB, book.ID, prepared)
	if err != nil {
		h.Logger.Error("import contacts failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	_ = models.DeleteImportUpload(r.Context(), h.DB, token)

	h.render(w, r, http.StatusOK, "import_result.html", map[string]any{
		"Book":     book,
		"Inserted": inserted,
		"Skipped":  skipped,
	})
}

func (h *Handlers) importFormError(w http.ResponseWriter, r *http.Request, book models.AddressBook, msg string) {
	h.render(w, r, http.StatusUnprocessableEntity, "import_form.html", map[string]any{
		"Book":      book,
		"UploadURL": "/books/" + book.PublicID + "/import",
		"Error":     msg,
	})
}
