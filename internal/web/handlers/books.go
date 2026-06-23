package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/rbac"
)

// BooksList shows the address books visible to the current user (GET /books).
func (h *Handlers) BooksList(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	books, err := models.ListAddressBooks(r.Context(), h.DB, user)
	if err != nil {
		h.Logger.Error("list address books failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.render(w, r, http.StatusOK, "books_list.html", map[string]any{"Books": books})
}

// BookNew renders the create form (GET /books/new).
func (h *Handlers) BookNew(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, "book_form.html", map[string]any{
		"Heading":    "New address book",
		"FormAction": "/books",
	})
}

// BookCreate creates a book owned by the current user (POST /books).
func (h *Handlers) BookCreate(w http.ResponseWriter, r *http.Request) {
	if !h.checkForm(w, r) {
		return
	}
	user, _ := auth.UserFromContext(r.Context())
	name := r.PostFormValue("name")
	description := r.PostFormValue("description")

	book, err := models.CreateAddressBook(r.Context(), h.DB, user.ID, name, description)
	if err != nil {
		h.render(w, r, http.StatusUnprocessableEntity, "book_form.html", map[string]any{
			"Heading":     "New address book",
			"FormAction":  "/books",
			"Name":        name,
			"Description": description,
			"Error":       "Name is required.",
		})
		return
	}
	http.Redirect(w, r, "/books/"+book.PublicID, http.StatusSeeOther)
}

// BookShow renders a book's detail with its contacts (GET /books/{publicID}),
// supporting a search query (?q=) and pagination (?page=).
func (h *Handlers) BookShow(w http.ResponseWriter, r *http.Request) {
	book, user, ok := h.authorizeBook(w, r, rbac.Read)
	if !ok {
		return
	}
	canManage, err := rbac.Can(r.Context(), h.DB, user, book.ID, rbac.Write)
	if err != nil {
		h.Logger.Error("authorize book manage failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	prefs, err := models.GetPreferences(r.Context(), h.DB, user.ID)
	if err != nil {
		h.Logger.Error("get preferences failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	sort := prefs.List.SortKey()
	desc := prefs.List.Desc
	limit, showAll := prefs.List.PageLimit()
	// The size the selector should show as chosen: the "all" sentinel, or the
	// effective limit (so the default 0 shows as its real value, e.g. 25).
	selectedSize := limit
	if showAll {
		selectedSize = -1
	}

	query := r.URL.Query().Get("q")
	page := parsePage(r.URL.Query().Get("page"))
	offset := (page - 1) * limit
	if showAll {
		page, offset = 1, 0
	}
	contacts, total, err := models.ListContacts(r.Context(), h.DB, book.ID, query, sort, desc, limit, offset)
	if err != nil {
		h.Logger.Error("list contacts failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	cards := buildContactCards(contacts,
		func(pid string) string { return "/contacts/" + pid },
		func(pid string) string { return "/contacts/" + pid + "/photo" })

	data := map[string]any{
		"Book":         book,
		"CanManage":    canManage.Allow,
		"Cards":        cards,
		"Query":        query,
		"Total":        total,
		"Page":         page,
		"Sort":         sort,
		"Desc":         desc,
		"SelectedSize": selectedSize,
		"ShowAll":      showAll,
		"SortOptions":  models.AllowedSorts,
		"PageSizes":    models.AllowedPageSizes,
		// Where the list-prefs form returns to: this book with its current search,
		// reset to page 1 (page offsets change when size/sort change).
		"ListPrefsReturn": bookContactsURL(book.PublicID, query, 1),
	}
	if !showAll {
		totalPages := (total + limit - 1) / limit
		if page > 1 {
			data["PrevURL"] = bookContactsURL(book.PublicID, query, page-1)
		}
		if page < totalPages {
			data["NextURL"] = bookContactsURL(book.PublicID, query, page+1)
		}
	}
	h.render(w, r, http.StatusOK, "book_show.html", data)
}

// BookEdit renders the edit form (GET /books/{publicID}/edit).
func (h *Handlers) BookEdit(w http.ResponseWriter, r *http.Request) {
	book, _, ok := h.authorizeBook(w, r, rbac.Write)
	if !ok {
		return
	}
	h.render(w, r, http.StatusOK, "book_form.html", map[string]any{
		"Heading":     "Edit address book",
		"FormAction":  "/books/" + book.PublicID + "/edit",
		"Name":        book.Name,
		"Description": book.Description,
	})
}

// BookUpdate applies an edit (POST /books/{publicID}/edit).
func (h *Handlers) BookUpdate(w http.ResponseWriter, r *http.Request) {
	book, _, ok := h.authorizeBook(w, r, rbac.Write)
	if !ok {
		return
	}
	if !h.checkForm(w, r) {
		return
	}
	name := r.PostFormValue("name")
	description := r.PostFormValue("description")

	if err := models.UpdateAddressBook(r.Context(), h.DB, book.ID, name, description); err != nil {
		h.render(w, r, http.StatusUnprocessableEntity, "book_form.html", map[string]any{
			"Heading":     "Edit address book",
			"FormAction":  "/books/" + book.PublicID + "/edit",
			"Name":        name,
			"Description": description,
			"Error":       "Name is required.",
		})
		return
	}
	http.Redirect(w, r, "/books/"+book.PublicID, http.StatusSeeOther)
}

// BookDelete removes a book and its contents (POST /books/{publicID}/delete).
func (h *Handlers) BookDelete(w http.ResponseWriter, r *http.Request) {
	book, _, ok := h.authorizeBook(w, r, rbac.Write)
	if !ok {
		return
	}
	if !h.checkForm(w, r) {
		return
	}
	if err := models.DeleteAddressBook(r.Context(), h.DB, book.ID); err != nil {
		h.Logger.Error("delete address book failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/books", http.StatusSeeOther)
}

// authorizeBook resolves the {publicID} book and enforces action. It writes the
// appropriate response (404 when the book is not visible to the user, 403 when
// visible but the action is not permitted) and returns ok=false in those cases.
func (h *Handlers) authorizeBook(w http.ResponseWriter, r *http.Request, action rbac.Action) (models.AddressBook, models.User, bool) {
	user, _ := auth.UserFromContext(r.Context())
	publicID := chi.URLParam(r, "publicID")

	book, err := models.GetAddressBookByPublicID(r.Context(), h.DB, publicID)
	if err != nil {
		if !errors.Is(err, models.ErrAddressBookNotFound) {
			h.Logger.Error("get address book failed", "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return models.AddressBook{}, models.User{}, false
		}
		http.NotFound(w, r)
		return models.AddressBook{}, models.User{}, false
	}

	decision, err := rbac.Can(r.Context(), h.DB, user, book.ID, action)
	if err != nil {
		h.Logger.Error("authorize book failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return models.AddressBook{}, models.User{}, false
	}
	if !decision.Visible {
		http.NotFound(w, r)
		return models.AddressBook{}, models.User{}, false
	}
	if !decision.Allow {
		http.Error(w, "forbidden", http.StatusForbidden)
		return models.AddressBook{}, models.User{}, false
	}
	return book, user, true
}

// checkForm parses the form and verifies the CSRF token, writing an error
// response and returning false on failure.
func (h *Handlers) checkForm(w http.ResponseWriter, r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return false
	}
	if err := auth.VerifyCSRF(r); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}
