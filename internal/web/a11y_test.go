package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
	"github.com/johannesheinz/skra/internal/vcardio"
)

// TestAccessibilityInvariants renders every page and asserts a set of concrete,
// unambiguous accessibility invariants. It is a dependency-free regression guard
// that runs in the normal CI test pass (rather than pulling a Node/axe toolchain
// into the build, which the local-first principle rules out); a manual
// axe/Lighthouse spot-check remains a recommended periodic complement.
func TestAccessibilityInvariants(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()

	admin := seedUser(t, d, "admin", "pw", models.RoleAdmin)
	session := sessionCookieFor(t, d, admin.ID)
	book, _ := models.CreateAddressBook(ctx, d, admin.ID, "Book", "A book")
	contact, _ := models.CreateContact(ctx, d, book.ID, models.ContactInput{
		GivenName: "Ada", FamilyName: "Lovelace", PrimaryEmail: "ada@example.com",
		Addresses: []vcardio.Address{{Street: "1 Main", City: "Town", PostalCode: "12345", Country: "USA"}},
	})

	pages := []string{
		"/",
		"/books",
		"/books/" + book.PublicID,
		"/books/" + book.PublicID + "/edit",
		"/books/" + book.PublicID + "/contacts/new",
		"/books/" + book.PublicID + "/members",
		"/books/" + book.PublicID + "/shares",
		"/books/" + book.PublicID + "/import",
		"/contacts/" + contact.PublicID,
		"/contacts/" + contact.PublicID + "/edit",
		"/account",
		"/admin/users",
		"/admin/users/new",
		"/admin/users/" + admin.PublicID + "/edit",
	}
	for _, p := range pages {
		rec, _, _ := authedGet(t, router, session, p)
		if rec.Code != http.StatusOK {
			t.Errorf("%s = %d, want 200", p, rec.Code)
			continue
		}
		checkA11y(t, p, rec.Body.String())
	}

	// The login page is unauthenticated.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
	checkA11y(t, "/login", rec.Body.String())
}

func checkA11y(t *testing.T, page, body string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("%s: parse: %v", page, err)
	}

	// First pass: collect ids targeted by any <label for=...>.
	labelledIDs := map[string]bool{}
	walk(doc, func(n *html.Node) {
		if n.DataAtom == atom.Label {
			if v := attr(n, "for"); v != "" {
				labelledIDs[v] = true
			}
		}
	})

	var (
		htmlLangOK bool
		h1Count    int
		hasTitle   bool
	)
	walkWithLabelCtx(doc, false, func(n *html.Node, insideLabel bool) {
		switch n.DataAtom {
		case atom.Html:
			if strings.TrimSpace(attr(n, "lang")) != "" {
				htmlLangOK = true
			}
		case atom.Title:
			if strings.TrimSpace(text(n)) != "" {
				hasTitle = true
			}
		case atom.H1:
			h1Count++
		case atom.Img:
			if !hasAttr(n, "alt") {
				t.Errorf("%s: <img> without alt (src=%q)", page, attr(n, "src"))
			}
		case atom.Button, atom.A:
			// A link with no href is not interactive; skip.
			if n.DataAtom == atom.A && !hasAttr(n, "href") {
				return
			}
			if !accessibleName(n) {
				t.Errorf("%s: <%s> without accessible name", page, n.Data)
			}
		case atom.Input, atom.Select, atom.Textarea:
			if !needsName(n) {
				return
			}
			if attr(n, "aria-label") != "" || attr(n, "aria-labelledby") != "" {
				return
			}
			if id := attr(n, "id"); id != "" && labelledIDs[id] {
				return
			}
			if insideLabel {
				return
			}
			t.Errorf("%s: form control <%s name=%q> has no accessible name", page, n.Data, attr(n, "name"))
		}
	})

	if !htmlLangOK {
		t.Errorf("%s: <html> missing non-empty lang", page)
	}
	if !hasTitle {
		t.Errorf("%s: missing non-empty <title>", page)
	}
	if h1Count != 1 {
		t.Errorf("%s: found %d <h1>, want exactly 1", page, h1Count)
	}
}

// needsName reports whether a form control requires an accessible name (skips
// hidden/submit/reset/button/image inputs, which don't).
func needsName(n *html.Node) bool {
	if n.DataAtom != atom.Input {
		return true // select, textarea
	}
	switch strings.ToLower(attr(n, "type")) {
	case "hidden", "submit", "reset", "button", "image":
		return false
	}
	return true
}

// accessibleName reports whether an element has a discernible name via text
// content, aria-label, aria-labelledby, or title.
func accessibleName(n *html.Node) bool {
	return strings.TrimSpace(text(n)) != "" ||
		attr(n, "aria-label") != "" || attr(n, "aria-labelledby") != "" || attr(n, "title") != ""
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

func text(n *html.Node) string {
	var b strings.Builder
	walk(n, func(c *html.Node) {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	})
	return b.String()
}

func walk(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, fn)
	}
}

func walkWithLabelCtx(n *html.Node, insideLabel bool, fn func(*html.Node, bool)) {
	fn(n, insideLabel)
	if n.DataAtom == atom.Label {
		insideLabel = true
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkWithLabelCtx(c, insideLabel, fn)
	}
}
