// Package templates embeds and renders Skra's server-rendered HTML.
package templates

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"
)

//go:embed *.html
var files embed.FS

var tmpl = template.Must(template.ParseFS(files, "*.html"))

// Render executes the named template into a buffer first, so a template error
// produces a clean 500 instead of a half-written 200 response.
func Render(w http.ResponseWriter, status int, name string, data any) error {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return fmt.Errorf("templates: render %q: %w", name, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, err := buf.WriteTo(w)
	return err
}
