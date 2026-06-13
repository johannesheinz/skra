// Package static embeds and serves Skrá's frontend assets (CSS, JS, fonts,
// logos). Everything is self-hosted and shipped inside the binary — no CDNs and
// no third-party origins — so the CSP can stay locked to 'self'.
//
// Vendored asset versions (updated manually; see README "Vendored assets"):
//   - htmx 2.0.9            js/htmx-v2.0.9.min.js
//   - Space Grotesk 2.0.0   fonts/SpaceGrotesk-*-2.0.0.woff2
package static

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed css fonts img js
var files embed.FS

// URLPrefix is where assets are mounted.
const URLPrefix = "/static/"

type asset struct {
	body        []byte
	contentType string
	etag        string // strong, quoted; derived from content hash
	version     string // short hash for cache-busting query
}

// assets maps an embedded path (e.g. "css/app.css") to its loaded form.
var assets = mustLoad()

func mustLoad() map[string]asset {
	loaded := make(map[string]asset)
	err := fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		body, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		hexsum := hex.EncodeToString(sum[:])
		loaded[p] = asset{
			body:        body,
			contentType: contentType(p),
			etag:        `"` + hexsum + `"`,
			version:     hexsum[:12],
		}
		return nil
	})
	if err != nil {
		panic("static: load embedded assets: " + err.Error())
	}
	return loaded
}

// URL returns the cache-busting URL for an embedded asset path, e.g.
// URL("css/app.css") -> "/static/css/app.css?v=<hash>". It panics on an unknown
// path so a broken template reference fails loudly in tests rather than 404ing
// in production.
func URL(p string) string {
	a, ok := assets[p]
	if !ok {
		panic(fmt.Sprintf("static: unknown asset %q", p))
	}
	return URLPrefix + p + "?v=" + a.version
}

// Handler serves embedded assets mounted under URLPrefix. The asset path is read
// from the trailing path; it must be passed the request with the prefix still
// present (mount with a wildcard, e.g. chi `/static/*`).
func Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), URLPrefix)
		a, ok := assets[name]
		if !ok {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", a.contentType)
		w.Header().Set("ETag", a.etag)
		// Versioned requests are immutable; bare requests (e.g. fonts referenced
		// from CSS without a version) revalidate cheaply via the ETag.
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}

		if match := r.Header.Get("If-None-Match"); match == a.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write(a.body)
	}
}

func contentType(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".woff2":
		return "font/woff2"
	case ".svg":
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}
