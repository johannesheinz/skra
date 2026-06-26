// Package icons inlines the vendored Lucide icon set (ISC-licensed; see the LICENSE file beside this package). Icons render as self-hosted inline SVG with currentColor — no icon font, no external requests, themeable and crisp.
package icons

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"regexp"
	"strings"
)

// Version is the vendored lucide-static release. Bump it together with the SVGs under svg/ (and the README "Vendored assets" table).
const Version = "1.22.0"

//go:embed svg/*.svg
var files embed.FS

// leadingComment strips the "@license" comment Lucide prepends to each file.
var leadingComment = regexp.MustCompile(`(?s)^\s*<!--.*?-->\s*`)

var set = mustLoad()

func mustLoad() map[string]template.HTML {
	entries, err := fs.ReadDir(files, "svg")
	if err != nil {
		panic("icons: read embedded svg: " + err.Error())
	}
	loaded := make(map[string]template.HTML, len(entries))
	for _, e := range entries {
		body, err := files.ReadFile("svg/" + e.Name())
		if err != nil {
			panic("icons: read " + e.Name() + ": " + err.Error())
		}
		name := strings.TrimSuffix(e.Name(), ".svg")
		// Trusted vendored asset: safe to emit as raw HTML.
		loaded[name] = template.HTML(strings.TrimSpace(leadingComment.ReplaceAllString(string(body), "")))
	}
	return loaded
}

// Inline returns the SVG markup for a Lucide icon by name (e.g. "pencil"). It panics on an unknown name so a broken reference fails loudly in tests rather than silently rendering nothing.
func Inline(name string) template.HTML {
	svg, ok := set[name]
	if !ok {
		panic(fmt.Sprintf("icons: unknown icon %q", name))
	}
	return svg
}
