package handlers

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/i18n"
	"github.com/johannesheinz/skra/internal/images"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/rbac"
	"github.com/johannesheinz/skra/internal/vcardio"
	"github.com/johannesheinz/skra/internal/web/templates"
)

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
	// Seed one blank email and phone row so the form shows a field to fill.
	h.renderContactForm(w, r, http.StatusOK, "contact_form.new",
		"/books/"+book.PublicID+"/contacts", "/books/"+book.PublicID,
		vcardio.Details{Emails: []vcardio.Typed{{}}, Phones: []vcardio.Typed{{}}}, "")
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
		h.renderContactForm(w, r, http.StatusUnprocessableEntity, "contact_form.new",
			"/books/"+book.PublicID+"/contacts", "/books/"+book.PublicID, in.Details(), h.tr(r).T("msg.contact_needs_name"))
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
	_, details, err := models.GetContactDetails(r.Context(), h.DB, contact.PublicID)
	if err != nil {
		h.Logger.Error("load contact details failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
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
		"Details":   details,
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
	_, details, err := models.GetContactDetails(r.Context(), h.DB, contact.PublicID)
	if err != nil {
		h.Logger.Error("load contact details failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.renderContactForm(w, r, http.StatusOK, "contact_form.edit",
		"/contacts/"+contact.PublicID+"/edit", "/contacts/"+contact.PublicID, details, "")
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
		h.renderContactForm(w, r, http.StatusUnprocessableEntity, "contact_form.edit",
			"/contacts/"+contact.PublicID+"/edit", "/contacts/"+contact.PublicID, in.Details(), h.tr(r).T("msg.contact_needs_name"))
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
	_, details, err := models.GetContactDetails(r.Context(), h.DB, contact.PublicID)
	if err != nil {
		h.Logger.Error("load contact details failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.render(w, r, http.StatusUnprocessableEntity, "contact_show.html", map[string]any{
		"Contact":   contact,
		"Details":   details,
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
		GivenName:  r.PostFormValue("given_name"),
		FamilyName: r.PostFormValue("family_name"),
		Org:        r.PostFormValue("org"),
		Title:      r.PostFormValue("title"),
		Birthday:   birthdayFromForm(r.PostFormValue("birthday"), r.PostFormValue("birthday_no_year") != ""),
		Note:       r.PostFormValue("note"),
		Emails:     typedFromForm(r.PostForm["email_type"], r.PostForm["email_value"]),
		Phones:     typedFromForm(r.PostForm["phone_type"], r.PostForm["phone_value"]),
		Addresses:  addressesFromForm(r),
		URLs:       nonEmptyValues(r.PostForm["url_value"]),
	}
}

// birthdayFromForm turns the date-input value into the stored birthday. When
// "no year" is chosen, the year is dropped to the vCard year-less form
// (--MM-DD); otherwise the YYYY-MM-DD value passes through.
func birthdayFromForm(date string, noYear bool) string {
	date = strings.TrimSpace(date)
	if noYear && len(date) == 10 { // YYYY-MM-DD -> --MM-DD
		return "--" + date[5:]
	}
	return date
}

// splitBirthday prepares a stored birthday for the date input: a value suitable
// for <input type="date"> (a placeholder year is supplied for year-less
// birthdays so the control can show the month/day) and whether the year is
// omitted.
func splitBirthday(raw string) (dateValue string, noYear bool) {
	norm := models.NormalizeBirthday(raw) // "YYYY-MM-DD" or "" (year 0000 = year-less)
	if norm == "" {
		return "", false
	}
	if norm[:4] == "0000" {
		return "2000" + norm[4:], true
	}
	return norm, false
}

// typedFromForm pairs parallel type/value arrays into typed values, dropping
// rows whose value is blank.
func typedFromForm(types, values []string) []vcardio.Typed {
	var out []vcardio.Typed
	for i, v := range values {
		if strings.TrimSpace(v) == "" {
			continue
		}
		t := ""
		if i < len(types) {
			t = types[i]
		}
		out = append(out, vcardio.Typed{Type: t, Value: strings.TrimSpace(v)})
	}
	return out
}

// addressesFromForm assembles addresses from the parallel adr_* arrays, dropping
// empty rows.
func addressesFromForm(r *http.Request) []vcardio.Address {
	streets := r.PostForm["adr_street"]
	cities := r.PostForm["adr_city"]
	postals := r.PostForm["adr_postal"]
	countries := r.PostForm["adr_country"]
	types := r.PostForm["adr_type"]

	n := len(streets)
	for _, s := range [][]string{cities, postals, countries} {
		if len(s) > n {
			n = len(s)
		}
	}
	at := func(s []string, i int) string {
		if i < len(s) {
			return strings.TrimSpace(s[i])
		}
		return ""
	}

	var out []vcardio.Address
	for i := 0; i < n; i++ {
		a := vcardio.Address{
			Type: at(types, i), Street: at(streets, i), City: at(cities, i),
			PostalCode: at(postals, i), Country: at(countries, i),
		}
		if !a.Empty() {
			out = append(out, a)
		}
	}
	return out
}

func nonEmptyValues(values []string) []string {
	var out []string
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}

func (h *Handlers) renderContactForm(w http.ResponseWriter, r *http.Request, status int, headingKey, action, cancelURL string, details vcardio.Details, errMsg string) {
	birthdayDate, noYear := splitBirthday(details.Birthday)
	h.render(w, r, status, "contact_form.html", map[string]any{
		"HeadingKey":     headingKey,
		"FormAction":     action,
		"CancelURL":      cancelURL,
		"D":              details,
		"BirthdayDate":   birthdayDate,
		"BirthdayNoYear": noYear,
		"Error":          errMsg,
	})
}

// ContactRowFragment returns a single blank form row for htmx to append
// (GET /ui/rows/{kind}). Auth is required but no specific resource.
func (h *Handlers) ContactRowFragment(w http.ResponseWriter, r *http.Request) {
	var tmpl string
	var data any
	switch chi.URLParam(r, "kind") {
	case "email":
		tmpl, data = "email_row", vcardio.Typed{}
	case "phone":
		tmpl, data = "phone_row", vcardio.Typed{}
	case "address":
		tmpl, data = "address_row", vcardio.Address{}
	case "url":
		tmpl, data = "url_row", ""
	default:
		http.NotFound(w, r)
		return
	}
	if err := templates.RenderFragment(w, i18n.FromContext(r.Context()).Code, tmpl, data); err != nil {
		h.Logger.Error("render row fragment failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
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
