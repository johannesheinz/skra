// Package templates embeds and renders Skrá's server-rendered HTML. Each page is
// parsed together with the shared base layout into its own template set, so the
// page's "title"/"content" blocks override the base without colliding across
// pages.
package templates

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"unicode"

	"github.com/johannesheinz/skra/internal/web/static"
)

//go:embed *.html
var files embed.FS

var funcs = template.FuncMap{
	"static":  static.URL,
	"initial": initial,
}

// initial returns the uppercased first letter of s for avatar placeholders.
func initial(s string) string {
	for _, r := range strings.TrimSpace(s) {
		return string(unicode.ToUpper(r))
	}
	return "?"
}

// pageFiles are the content templates; each is composed with base.html and the
// shared partials.
var pageFiles = []string{
	"login.html",
	"home.html",
	"books_list.html",
	"book_form.html",
	"book_show.html",
	"contact_form.html",
	"contact_show.html",
	"shares.html",
	"gate.html",
	"share_directory.html",
	"share_contact.html",
	"admin_users_list.html",
	"admin_user_form.html",
	"members.html",
	"account_password.html",
	"import_form.html",
	"import_preview.html",
	"import_result.html",
}

var pages = mustParse()

func mustParse() map[string]*template.Template {
	set := make(map[string]*template.Template, len(pageFiles))
	for _, page := range pageFiles {
		t := template.New(page).Funcs(funcs)
		template.Must(t.ParseFS(files, "base.html", "partials.html", page))
		set[page] = t
	}
	return set
}

// Render executes a page (composed with the base layout) into a buffer first, so
// a template error yields a clean 500 instead of a half-written 200.
func Render(w http.ResponseWriter, status int, page string, data any) error {
	t, ok := pages[page]
	if !ok {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return fmt.Errorf("templates: unknown page %q", page)
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "base.html", data); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return fmt.Errorf("templates: render %q: %w", page, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, err := buf.WriteTo(w)
	return err
}
