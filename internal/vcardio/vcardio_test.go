package vcardio

import (
	"strings"
	"testing"
)

func TestEncodeParseRoundTrip(t *testing.T) {
	in := Details{
		GivenName:  "Jane",
		FamilyName: "Doe",
		Org:        "Acme",
		Title:      "Engineer",
		Birthday:   "1990-04-01",
		Note:       "met at conf",
		Emails:     []Typed{{Type: "work", Value: "jane@acme.test"}, {Type: "home", Value: "jane@home.test"}},
		Phones:     []Typed{{Type: "mobile", Value: "+1555"}},
		Addresses:  []Address{{Type: "home", Street: "1 Main", City: "Town", Region: "RG", PostalCode: "12345", Country: "Land"}},
		URLs:       []string{"https://jane.test"},
	}
	raw, err := Encode(in, "uid-1")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.Contains(raw, "VERSION:4.0") || !strings.Contains(raw, "UID:uid-1") {
		t.Errorf("missing version/uid:\n%s", raw)
	}

	out, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if out.GivenName != "Jane" || out.FamilyName != "Doe" {
		t.Errorf("name = %q %q", out.GivenName, out.FamilyName)
	}
	if out.DisplayName() != "Jane Doe" {
		t.Errorf("DisplayName = %q", out.DisplayName())
	}
	if out.Org != "Acme" || out.Title != "Engineer" || out.Birthday != "1990-04-01" || out.Note != "met at conf" {
		t.Errorf("scalars wrong: %+v", out)
	}
	if len(out.Emails) != 2 {
		t.Fatalf("emails = %d, want 2", len(out.Emails))
	}
	if out.PrimaryEmail() != "jane@acme.test" {
		t.Errorf("primary email = %q", out.PrimaryEmail())
	}
	// Type labels survive.
	if out.Emails[0].Type != "work" {
		t.Errorf("email[0] type = %q, want work", out.Emails[0].Type)
	}
	if len(out.Phones) != 1 || out.Phones[0].Value != "+1555" {
		t.Errorf("phones = %+v", out.Phones)
	}
	if len(out.Addresses) != 1 || out.Addresses[0].City != "Town" || out.Addresses[0].PostalCode != "12345" {
		t.Errorf("address = %+v", out.Addresses)
	}
	if len(out.URLs) != 1 || out.URLs[0] != "https://jane.test" {
		t.Errorf("urls = %+v", out.URLs)
	}
}

func TestDisplayNameFallbackToFormatted(t *testing.T) {
	// A card with FN only (no N components) keeps the name on parse.
	raw, err := Encode(Details{FormattedName: "Acme Corp"}, "uid-2")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if out.DisplayName() != "Acme Corp" {
		t.Errorf("DisplayName = %q, want Acme Corp", out.DisplayName())
	}
}

func TestEmptyAddressesAndBlankValuesOmitted(t *testing.T) {
	raw, err := Encode(Details{
		GivenName: "A",
		Emails:    []Typed{{Value: ""}, {Value: "a@x.test"}},
		Addresses: []Address{{}},
	}, "u")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out, _ := Parse(raw)
	if len(out.Emails) != 1 {
		t.Errorf("blank email not omitted: %+v", out.Emails)
	}
	if len(out.Addresses) != 0 {
		t.Errorf("empty address not omitted: %+v", out.Addresses)
	}
}
