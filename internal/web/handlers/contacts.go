package handlers

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/images"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/rbac"
)

const contactsPageSize = 25

// ContactCard is the view-model for one contact in a directory card grid. The
// URLs are precomputed by the caller so the same card partial renders both the
// authenticated browse and the public share (with share-scoped links).
type ContactCard struct {
	Name     string
	Org      string
	Email    string
	URL      string
	PhotoURL string
	HasPhoto bool
}

// buildContactCards maps contacts to cards, deriving each card's detail and
// photo URL via the supplied functions (which differ per context).
func buildContactCards(contacts []models.Contact, detailURL, photoURL func(publicID string) string) []ContactCard {
	cards := make([]ContactCard, 0, len(contacts))
	for _, c := range contacts {
		cards = append(cards, ContactCard{
			Name:     c.FullName,
			Org:      c.Org,
			Email:    c.PrimaryEmail,
			URL:      detailURL(c.PublicID),
			PhotoURL: photoURL(c.PublicID),
			HasPhoto: c.HasPhoto,
		})
	}
	return cards
}

// Photo upload limits.
const (
	maxUploadBytes  = 10 << 20 // 10 MiB total request body
	maxUploadMemory = 1 << 20  // keep up to 1 MiB in memory, spill the rest to temp
)

// ContactNew renders the create form for a contact in a book
// (GET /books/{publicID}/contacts/new). Requires manager on the book.
func (h *Handlers) ContactNew(w http.ResponseWriter, r *http.Request) {
	book, _, ok := h.authorizeBook(w, r, rbac.Write)
	if !ok {
		return
	}
	h.render(w, r, http.StatusOK, "contact_form.html", map[string]any{
		"Heading":    "New contact",
		"FormAction": "/books/" + book.PublicID + "/contacts",
		"CancelURL":  "/books/" + book.PublicID,
	})
}

// ContactCreate creates a contact (POST /books/{publicID}/contacts).
func (h *Handlers) ContactCreate(w http.ResponseWriter, r *http.Request) {
	book, _, ok := h.authorizeBook(w, r, rbac.Write)
	if !ok {
		return
	}
	if !h.checkForm(w, r) {
		return
	}
	in := contactInputFromForm(r)
	contact, err := models.CreateContact(r.Context(), h.DB, book.ID, in)
	if err != nil {
		h.renderContactForm(w, r, http.StatusUnprocessableEntity, "New contact",
			"/books/"+book.PublicID+"/contacts", "/books/"+book.PublicID, in, "Full name is required.")
		return
	}
	http.Redirect(w, r, "/contacts/"+contact.PublicID, http.StatusSeeOther)
}

// ContactShow renders a contact's detail (GET /contacts/{publicID}).
func (h *Handlers) ContactShow(w http.ResponseWriter, r *http.Request) {
	contact, book, user, ok := h.authorizeContact(w, r, rbac.Read)
	if !ok {
		return
	}
	canManage, err := rbac.Can(r.Context(), h.DB, user, contact.AddressBookID, rbac.Write)
	if err != nil {
		h.Logger.Error("authorize contact manage failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.render(w, r, http.StatusOK, "contact_show.html", map[string]any{
		"Contact":   contact,
		"Book":      book,
		"CanManage": canManage.Allow,
	})
}

// ContactEdit renders the edit form (GET /contacts/{publicID}/edit).
func (h *Handlers) ContactEdit(w http.ResponseWriter, r *http.Request) {
	contact, _, _, ok := h.authorizeContact(w, r, rbac.Write)
	if !ok {
		return
	}
	h.renderContactForm(w, r, http.StatusOK, "Edit contact",
		"/contacts/"+contact.PublicID+"/edit", "/contacts/"+contact.PublicID,
		models.ContactInput{
			FullName: contact.FullName, Org: contact.Org,
			PrimaryEmail: contact.PrimaryEmail, PrimaryPhone: contact.PrimaryPhone,
		}, "")
}

// ContactUpdate applies an edit (POST /contacts/{publicID}/edit).
func (h *Handlers) ContactUpdate(w http.ResponseWriter, r *http.Request) {
	contact, _, _, ok := h.authorizeContact(w, r, rbac.Write)
	if !ok {
		return
	}
	if !h.checkForm(w, r) {
		return
	}
	in := contactInputFromForm(r)
	if err := models.UpdateContact(r.Context(), h.DB, contact, in); err != nil {
		h.renderContactForm(w, r, http.StatusUnprocessableEntity, "Edit contact",
			"/contacts/"+contact.PublicID+"/edit", "/contacts/"+contact.PublicID, in, "Full name is required.")
		return
	}
	http.Redirect(w, r, "/contacts/"+contact.PublicID, http.StatusSeeOther)
}

// ContactDelete removes a contact (POST /contacts/{publicID}/delete).
func (h *Handlers) ContactDelete(w http.ResponseWriter, r *http.Request) {
	contact, book, _, ok := h.authorizeContact(w, r, rbac.Write)
	if !ok {
		return
	}
	if !h.checkForm(w, r) {
		return
	}
	if err := models.DeleteContact(r.Context(), h.DB, contact.ID); err != nil {
		h.Logger.Error("delete contact failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/books/"+book.PublicID, http.StatusSeeOther)
}

// ContactPhotoUpload accepts a multipart photo, runs it through the ingest
// pipeline, and stores the normalized JPEG (POST /contacts/{publicID}/photo).
func (h *Handlers) ContactPhotoUpload(w http.ResponseWriter, r *http.Request) {
	contact, book, user, ok := h.authorizeContact(w, r, rbac.Write)
	if !ok {
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

	file, _, err := r.FormFile("photo")
	if err != nil {
		h.renderContactWithError(w, r, contact, book, user, "Choose an image file to upload.")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		h.Logger.Error("read upload failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	jpeg, err := images.Process(data)
	if err != nil {
		// Decode failures are user error (unsupported/corrupt image, e.g. HEIC).
		h.renderContactWithError(w, r, contact, book, user, "That image could not be processed. Use JPEG or PNG.")
		return
	}
	if err := models.SetContactPhoto(r.Context(), h.DB, contact.ID, jpeg); err != nil {
		h.Logger.Error("store photo failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/contacts/"+contact.PublicID, http.StatusSeeOther)
}

// ContactPhotoDelete removes a contact's photo (POST /contacts/{publicID}/photo/delete).
func (h *Handlers) ContactPhotoDelete(w http.ResponseWriter, r *http.Request) {
	contact, _, _, ok := h.authorizeContact(w, r, rbac.Write)
	if !ok {
		return
	}
	if !h.checkForm(w, r) {
		return
	}
	if err := models.DeleteContactPhoto(r.Context(), h.DB, contact.ID); err != nil {
		h.Logger.Error("delete photo failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/contacts/"+contact.PublicID, http.StatusSeeOther)
}

func (h *Handlers) renderContactWithError(w http.ResponseWriter, r *http.Request, contact models.Contact, book models.AddressBook, user models.User, errMsg string) {
	canManage, _ := rbac.Can(r.Context(), h.DB, user, contact.AddressBookID, rbac.Write)
	h.render(w, r, http.StatusUnprocessableEntity, "contact_show.html", map[string]any{
		"Contact":   contact,
		"Book":      book,
		"CanManage": canManage.Allow,
		"Error":     errMsg,
	})
}

// authorizeContact resolves the {publicID} contact, loads its book (for links),
// and enforces action against the contact's book. 404 when not visible, 403 when
// visible but forbidden.
func (h *Handlers) authorizeContact(w http.ResponseWriter, r *http.Request, action rbac.Action) (models.Contact, models.AddressBook, models.User, bool) {
	user, _ := auth.UserFromContext(r.Context())
	publicID := chi.URLParam(r, "publicID")

	contact, err := models.GetContactByPublicID(r.Context(), h.DB, publicID)
	if err != nil {
		if !errors.Is(err, models.ErrContactNotFound) {
			h.Logger.Error("get contact failed", "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return models.Contact{}, models.AddressBook{}, models.User{}, false
		}
		http.NotFound(w, r)
		return models.Contact{}, models.AddressBook{}, models.User{}, false
	}

	decision, err := rbac.Can(r.Context(), h.DB, user, contact.AddressBookID, action)
	if err != nil {
		h.Logger.Error("authorize contact failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return models.Contact{}, models.AddressBook{}, models.User{}, false
	}
	if !decision.Visible {
		http.NotFound(w, r)
		return models.Contact{}, models.AddressBook{}, models.User{}, false
	}
	if !decision.Allow {
		http.Error(w, "forbidden", http.StatusForbidden)
		return models.Contact{}, models.AddressBook{}, models.User{}, false
	}

	book, err := models.GetAddressBookByID(r.Context(), h.DB, contact.AddressBookID)
	if err != nil {
		h.Logger.Error("load contact book failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return models.Contact{}, models.AddressBook{}, models.User{}, false
	}
	return contact, book, user, true
}

func contactInputFromForm(r *http.Request) models.ContactInput {
	return models.ContactInput{
		FullName:     r.PostFormValue("full_name"),
		Org:          r.PostFormValue("org"),
		PrimaryEmail: r.PostFormValue("primary_email"),
		PrimaryPhone: r.PostFormValue("primary_phone"),
	}
}

func (h *Handlers) renderContactForm(w http.ResponseWriter, r *http.Request, status int, heading, action, cancelURL string, in models.ContactInput, errMsg string) {
	h.render(w, r, status, "contact_form.html", map[string]any{
		"Heading":      heading,
		"FormAction":   action,
		"CancelURL":    cancelURL,
		"FullName":     in.FullName,
		"Org":          in.Org,
		"PrimaryEmail": in.PrimaryEmail,
		"PrimaryPhone": in.PrimaryPhone,
		"Error":        errMsg,
	})
}

// parsePage parses a 1-based page number, defaulting to 1 on any bad value.
func parsePage(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// bookContactsURL builds a book detail URL carrying the search query and page.
func bookContactsURL(publicID, query string, page int) string {
	v := url.Values{}
	if query != "" {
		v.Set("q", query)
	}
	if page > 1 {
		v.Set("page", strconv.Itoa(page))
	}
	u := "/books/" + publicID
	if encoded := v.Encode(); encoded != "" {
		u += "?" + encoded
	}
	return u
}
