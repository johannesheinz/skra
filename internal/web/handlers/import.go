package handlers

import (
	"io"
	"net/http"
	"strings"

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
		h.importFormError(w, r, book, h.tr(r).T("msg.choose_vcf"))
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
		h.importFormError(w, r, book, h.tr(r).T("msg.no_contacts_found"))
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

	toImport := make([]importing.Record, 0, len(classified))
	skipped := 0
	for _, c := range classified {
		if action == importActionSkip && c.Duplicate {
			skipped++
			continue
		}
		toImport = append(toImport, c.Record)
	}
	prepared, err := h.buildPreparedImports(toImport, usedUIDs)
	if err != nil {
		h.Logger.Error("prepare import failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
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

// buildPreparedImports converts parsed records into insertable rows, minting a
// fresh uid (and rebuilding the canonical vCard) whenever a record has no uid or
// collides with one already used, and normalizing any embedded photo. usedUIDs
// is seeded with the uids already present in the target book and is mutated as
// uids are consumed.
func (h *Handlers) buildPreparedImports(records []importing.Record, usedUIDs map[string]bool) ([]models.PreparedImport, error) {
	prepared := make([]models.PreparedImport, 0, len(records))
	for _, rec := range records {
		uid, raw := rec.UID, rec.CanonicalRaw
		if uid == "" || usedUIDs[uid] {
			minted, err := ids.Random(16)
			if err != nil {
				return nil, err
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
			Birthday: rec.Birthday, VCardRaw: raw, UID: uid, PhotoJPEG: jpeg,
		})
	}
	return prepared, nil
}

// BookImportNew (POST /books/import) lets an admin create a new address book and
// import a vCard file into it in one step, from the address-book overview. It is
// admin-only; the target book starts empty, so there is no dedup/preview step.
func (h *Handlers) BookImportNew(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if user.Role != models.RoleAdmin {
		http.NotFound(w, r) // don't reveal the admin-only action
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadMemory); err != nil {
		http.Error(w, "upload too large or malformed", http.StatusRequestEntityTooLarge)
		return
	}
	if err := auth.VerifyCSRF(r); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" {
		h.booksListError(w, r, h.tr(r).T("msg.enter_book_name"))
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		h.booksListError(w, r, h.tr(r).T("msg.choose_vcf"))
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		h.Logger.Error("read import upload failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	records, _ := importing.ParseVCards(data)
	if len(records) == 0 {
		h.booksListError(w, r, h.tr(r).T("msg.no_contacts_found"))
		return
	}

	book, err := models.CreateAddressBook(r.Context(), h.DB, user.ID, name, "")
	if err != nil {
		h.booksListError(w, r, h.tr(r).T("msg.book_create_failed"))
		return
	}
	prepared, err := h.buildPreparedImports(records, map[string]bool{})
	if err != nil {
		h.Logger.Error("prepare import failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	inserted, err := models.ImportContacts(r.Context(), h.DB, book.ID, prepared)
	if err != nil {
		h.Logger.Error("import contacts failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.render(w, r, http.StatusOK, "import_result.html", map[string]any{
		"Book":     book,
		"Inserted": inserted,
		"Skipped":  0,
	})
}

// booksListError re-renders the address-book overview with an import error.
func (h *Handlers) booksListError(w http.ResponseWriter, r *http.Request, msg string) {
	user, _ := auth.UserFromContext(r.Context())
	books, err := models.ListAddressBooks(r.Context(), h.DB, user)
	if err != nil {
		h.Logger.Error("list address books failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.render(w, r, http.StatusUnprocessableEntity, "books_list.html", map[string]any{
		"Books":       books,
		"ImportError": msg,
	})
}

func (h *Handlers) importFormError(w http.ResponseWriter, r *http.Request, book models.AddressBook, msg string) {
	h.render(w, r, http.StatusUnprocessableEntity, "import_form.html", map[string]any{
		"Book":      book,
		"UploadURL": "/books/" + book.PublicID + "/import",
		"Error":     msg,
	})
}
