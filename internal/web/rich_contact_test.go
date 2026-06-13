package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
)

func TestRichContactMultiValueRoundTrip(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	session := sessionCookieFor(t, d, owner.ID)
	bookURL := "/books/" + book.PublicID

	_, token, csrf := authedGet(t, router, session, bookURL+"/contacts/new")
	form := url.Values{
		auth.CSRFFormField: {token},
		"given_name":       {"Jane"},
		"family_name":      {"Doe"},
		// two emails
		"email_type":  {"work", "home"},
		"email_value": {"jane@work.test", "jane@home.test"},
		// two phones
		"phone_type":  {"mobile", "work"},
		"phone_value": {"+15551111", "+15552222"},
		// one address
		"adr_type":    {"home"},
		"adr_street":  {"1 Main St"},
		"adr_city":    {"Townsville"},
		"adr_region":  {"RG"},
		"adr_postal":  {"12345"},
		"adr_country": {"Country"},
		// links + scalars
		"url_value": {"https://jane.test"},
		"birthday":  {"1990-04-01"},
		"note":      {"a note"},
	}
	create := authedPostForm(router, session, csrf, bookURL+"/contacts", form)
	if create.Code != http.StatusSeeOther {
		t.Fatalf("create = %d, want 303", create.Code)
	}
	contactURL := create.Header().Get("Location")

	// Detail shows both emails, both phones, the address, link, note.
	show, _, _ := authedGet(t, router, session, contactURL)
	body := show.Body.String()
	// Phone "+" is rendered as the HTML entity &#43;, so assert on the digits.
	for _, want := range []string{
		"jane@work.test", "jane@home.test", "15551111", "15552222",
		"1 Main St", "Townsville", "https://jane.test", "a note",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail missing %q", want)
		}
	}

	// The edit form is pre-populated with the multiple values (round-trips
	// through vcard_raw), so re-saving preserves them.
	publicID := strings.TrimPrefix(contactURL, "/contacts/")
	editForm, _, _ := authedGet(t, router, session, "/contacts/"+publicID+"/edit")
	eb := editForm.Body.String()
	if strings.Count(eb, `name="email_value"`) < 2 {
		t.Errorf("edit form should pre-fill 2 email rows:\n%s", eb)
	}
	if !strings.Contains(eb, "jane@home.test") || !strings.Contains(eb, "15552222") {
		t.Error("edit form missing secondary values")
	}

	// The cached primary email is the first one (used by list/search).
	contact, _ := models.GetContactByPublicID(ctx, d, publicID)
	if contact.PrimaryEmail != "jane@work.test" {
		t.Errorf("primary email cache = %q, want jane@work.test", contact.PrimaryEmail)
	}
}

func TestContactRowFragment(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	session := sessionCookieFor(t, d, owner.ID)

	rec, _, _ := authedGet(t, router, session, "/ui/rows/email")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `name="email_value"`) {
		t.Errorf("email row fragment = %d, body %q", rec.Code, rec.Body.String())
	}
	if bad, _, _ := authedGet(t, router, session, "/ui/rows/bogus"); bad.Code != http.StatusNotFound {
		t.Errorf("unknown row kind = %d, want 404", bad.Code)
	}
}
