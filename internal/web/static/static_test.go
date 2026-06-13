package static

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestURLIsVersioned(t *testing.T) {
	u := URL("css/app.css")
	if !strings.HasPrefix(u, "/static/css/app.css?v=") {
		t.Errorf("URL = %q, want /static/css/app.css?v=...", u)
	}
}

func TestURLPanicsOnUnknownAsset(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("URL on unknown asset did not panic")
		}
	}()
	URL("css/does-not-exist.css")
}

func TestHandlerServesAssetWithCachingHeaders(t *testing.T) {
	h := Handler()

	req := httptest.NewRequest(http.MethodGet, "/static/css/app.css?v=abc", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/css; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("versioned request Cache-Control = %q, want immutable", cc)
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("missing ETag")
	}
	if rec.Body.Len() == 0 {
		t.Error("empty body")
	}
}

func TestHandlerUnversionedRevalidates(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest(http.MethodGet, "/static/fonts/SpaceGrotesk-Medium-2.0.0.woff2", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "font/woff2" {
		t.Errorf("Content-Type = %q, want font/woff2", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); strings.Contains(cc, "immutable") {
		t.Errorf("unversioned request should not be immutable, got %q", cc)
	}
}

func TestHandlerConditionalGet(t *testing.T) {
	h := Handler()

	// First fetch to learn the ETag.
	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/static/js/htmx-v2.0.9.min.js", nil))
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on first fetch")
	}

	// Conditional fetch with matching ETag → 304.
	condReq := httptest.NewRequest(http.MethodGet, "/static/js/htmx-v2.0.9.min.js", nil)
	condReq.Header.Set("If-None-Match", etag)
	condRec := httptest.NewRecorder()
	h.ServeHTTP(condRec, condReq)
	if condRec.Code != http.StatusNotModified {
		t.Errorf("conditional GET = %d, want 304", condRec.Code)
	}
}

func TestHandlerUnknownAssetIs404(t *testing.T) {
	h := Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/css/nope.css", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestLogoAssetsAreEmbedded(t *testing.T) {
	for _, name := range []string{
		"img/skra-favicon.svg", "img/skra-lockup.svg", "img/skra-icon.svg", "img/skra-wordmark.svg",
	} {
		if _, ok := assets[name]; !ok {
			t.Errorf("expected embedded asset %q", name)
		}
	}
}
