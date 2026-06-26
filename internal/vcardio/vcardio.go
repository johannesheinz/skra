// Package vcardio converts between a contact's canonical vcard_raw and a rich, editable Details struct (multiple emails/phones/addresses, name components, and assorted scalar fields). It is the single place that knows the vCard representation, so the rest of the app works with Details.
package vcardio

import (
	"bytes"
	"strings"

	"github.com/emersion/go-vcard"
)

// Typed is a value with an optional type label (e.g. home/work/mobile).
type Typed struct {
	Type  string
	Value string
}

// Address is a postal address with an optional type label.
type Address struct {
	Type       string
	Street     string
	City       string
	PostalCode string
	Country    string
}

// Empty reports whether the address has no content.
func (a Address) Empty() bool {
	return a.Street == "" && a.City == "" && a.PostalCode == "" && a.Country == ""
}

// SingleLine renders the address as one comma-separated line of its non-empty components, suitable as a free-text geocoding query for a map link.
func (a Address) SingleLine() string {
	parts := make([]string, 0, 4)
	for _, p := range []string{a.Street, a.City, a.PostalCode, a.Country} {
		if s := strings.TrimSpace(p); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}

// Details is the rich, editable view of a contact.
type Details struct {
	GivenName     string
	FamilyName    string
	FormattedName string // explicit display name; used when name components are absent
	Org           string
	Title         string
	Birthday      string
	Note          string
	Emails        []Typed
	Phones        []Typed
	Addresses     []Address
	URLs          []string
}

// DisplayName is the contact's listing name: the given/family combination when present, otherwise the formatted name carried from FN.
func (d Details) DisplayName() string {
	if n := strings.TrimSpace(d.GivenName + " " + d.FamilyName); n != "" {
		return n
	}
	return strings.TrimSpace(d.FormattedName)
}

// PrimaryEmail returns the first email value, or "".
func (d Details) PrimaryEmail() string {
	if len(d.Emails) > 0 {
		return d.Emails[0].Value
	}
	return ""
}

// PrimaryPhone returns the first phone value, or "".
func (d Details) PrimaryPhone() string {
	if len(d.Phones) > 0 {
		return d.Phones[0].Value
	}
	return ""
}

// Encode renders Details as a canonical vCard 4.0 string with the given uid. PHOTO is intentionally not included; photos are stored separately.
func Encode(d Details, uid string) (string, error) {
	card := vcard.Card{}
	card.AddName(&vcard.Name{FamilyName: d.FamilyName, GivenName: d.GivenName})
	card.SetValue(vcard.FieldFormattedName, d.DisplayName())
	setIf(card, vcard.FieldOrganization, d.Org)
	setIf(card, vcard.FieldTitle, d.Title)
	setIf(card, vcard.FieldBirthday, d.Birthday)
	setIf(card, vcard.FieldNote, d.Note)

	for _, e := range d.Emails {
		if strings.TrimSpace(e.Value) != "" {
			card.Add(vcard.FieldEmail, typedField(e))
		}
	}
	for _, p := range d.Phones {
		if strings.TrimSpace(p.Value) != "" {
			card.Add(vcard.FieldTelephone, typedField(p))
		}
	}
	for _, a := range d.Addresses {
		if a.Empty() {
			continue
		}
		adr := &vcard.Address{
			StreetAddress: a.Street, Locality: a.City,
			PostalCode: a.PostalCode, Country: a.Country,
		}
		if a.Type != "" {
			adr.Field = &vcard.Field{Params: vcard.Params{vcard.ParamType: {a.Type}}}
		}
		card.AddAddress(adr)
	}
	for _, u := range d.URLs {
		if strings.TrimSpace(u) != "" {
			card.Add(vcard.FieldURL, &vcard.Field{Value: u})
		}
	}

	card.SetValue(vcard.FieldUID, uid)
	vcard.ToV4(card)

	var buf bytes.Buffer
	if err := vcard.NewEncoder(&buf).Encode(card); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Parse reads a vCard string into Details. A card with name components absent but FN present keeps the display name in GivenName so editing preserves it.
func Parse(raw string) (Details, error) {
	card, err := vcard.NewDecoder(strings.NewReader(raw)).Decode()
	if err != nil {
		return Details{}, err
	}

	var d Details
	if n := card.Name(); n != nil {
		d.GivenName = n.GivenName
		d.FamilyName = n.FamilyName
	}
	d.FormattedName = card.PreferredValue(vcard.FieldFormattedName)
	if d.GivenName == "" && d.FamilyName == "" && d.FormattedName != "" {
		d.GivenName = d.FormattedName
	}
	d.Org = card.PreferredValue(vcard.FieldOrganization)
	d.Title = card.PreferredValue(vcard.FieldTitle)
	d.Birthday = card.PreferredValue(vcard.FieldBirthday)
	d.Note = card.PreferredValue(vcard.FieldNote)

	for _, f := range card[vcard.FieldEmail] {
		d.Emails = append(d.Emails, Typed{Type: f.Params.Get(vcard.ParamType), Value: f.Value})
	}
	for _, f := range card[vcard.FieldTelephone] {
		d.Phones = append(d.Phones, Typed{Type: f.Params.Get(vcard.ParamType), Value: f.Value})
	}
	for _, a := range card.Addresses() {
		addr := Address{
			Street: a.StreetAddress, City: a.Locality,
			PostalCode: a.PostalCode, Country: a.Country,
		}
		if a.Field != nil {
			addr.Type = a.Field.Params.Get(vcard.ParamType)
		}
		d.Addresses = append(d.Addresses, addr)
	}
	for _, f := range card[vcard.FieldURL] {
		d.URLs = append(d.URLs, f.Value)
	}
	return d, nil
}

func setIf(card vcard.Card, field, value string) {
	if strings.TrimSpace(value) != "" {
		card.SetValue(field, value)
	}
}

func typedField(t Typed) *vcard.Field {
	f := &vcard.Field{Value: t.Value}
	if t.Type != "" {
		f.Params = vcard.Params{vcard.ParamType: {t.Type}}
	}
	return f
}
