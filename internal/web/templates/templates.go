// Package templates embeds and renders Skrá's server-rendered HTML. Each page is
// parsed together with the shared base layout into its own template set, so the
// page's "title"/"content" blocks override the base without colliding across
// pages. Sets are built once per supported locale, each with locale-bound
// translation/formatting functions, so rendering is a map lookup with no
// per-request parsing.
package templates

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"unicode"

	"github.com/johannesheinz/skra/internal/i18n"
	"github.com/johannesheinz/skra/internal/web/icons"
	"github.com/johannesheinz/skra/internal/web/static"
)

//go:embed *.html
var files embed.FS

// initial returns the uppercased first letter of s for avatar placeholders.
func initial(s string) string {
	for _, r := range strings.TrimSpace(s) {
		return string(unicode.ToUpper(r))
	}
	return "?"
}

// localeFuncs builds the FuncMap for one locale: the locale-independent helpers
// plus the translator/formatter bound to that locale.
func localeFuncs(tr *i18n.Translator) template.FuncMap {
	return template.FuncMap{
		"static":           static.URL,
		"icon":             icons.Inline,
		"initial":          initial,
		"t":                tr.T,
		"tf":               tr.Tf,
		"tn":               tr.Plural,
		"num":              tr.Num,
		"monthday":         tr.MonthDay,
		"isodate":          tr.ISODate,
		"typelabel":        tr.TypeLabel,
		"postalBeforeCity": tr.PostalBeforeCity,
	}
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
	"account.html",
	"import_form.html",
	"import_preview.html",
	"import_result.html",
}

// pages[localeCode][page] and fragments[localeCode] are parsed once at init.
var pages, fragments = mustParse()

func mustParse() (map[string]map[string]*template.Template, map[string]*template.Template) {
	pageSets := make(map[string]map[string]*template.Template)
	fragSets := make(map[string]*template.Template)
	for _, loc := range i18n.Locales() {
		funcs := localeFuncs(i18n.For(loc))
		set := make(map[string]*template.Template, len(pageFiles))
		for _, page := range pageFiles {
			t := template.New(page).Funcs(funcs)
			template.Must(t.ParseFS(files, "base.html", "partials.html", "fragments.html", page))
			set[page] = t
		}
		pageSets[loc.Code] = set
		fragSets[loc.Code] = template.Must(template.New("fragments").Funcs(funcs).ParseFS(files, "fragments.html"))
	}
	return pageSets, fragSets
}

// RenderFragment writes a single named fragment (no base layout) for the locale,
// buffering so a template error yields a clean 500.
func RenderFragment(w http.ResponseWriter, localeCode, name string, data any) error {
	set, ok := fragments[localeCode]
	if !ok {
		set = fragments[i18n.Default().Code]
	}
	var buf bytes.Buffer
	if err := set.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return fmt.Errorf("templates: render fragment %q: %w", name, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := buf.WriteTo(w)
	return err
}

// Render executes a page (composed with the base layout) for the locale into a
// buffer first, so a template error yields a clean 500 instead of a half-written
// 200. An unknown locale falls back to the default.
func Render(w http.ResponseWriter, status int, localeCode, page string, data any) error {
	set, ok := pages[localeCode]
	if !ok {
		set = pages[i18n.Default().Code]
	}
	t, ok := set[page]
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
