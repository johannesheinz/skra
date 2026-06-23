package models_test

import (
	"context"
	"testing"

	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
	"github.com/johannesheinz/skra/internal/vcardio"
)

func TestListPrefsPageLimitAndSort(t *testing.T) {
	cases := []struct {
		size      int
		wantLimit int
		wantAll   bool
	}{
		{0, 24, false},   // unset -> default
		{-1, -1, true},   // all
		{48, 48, false},  // valid
		{999, 24, false}, // unknown -> default
	}
	for _, c := range cases {
		l := models.ListPrefs{PageSize: c.size}
		if lim, all := l.PageLimit(); lim != c.wantLimit || all != c.wantAll {
			t.Errorf("PageLimit(%d) = (%d,%v), want (%d,%v)", c.size, lim, all, c.wantLimit, c.wantAll)
		}
	}
	if got := (models.ListPrefs{}).SortKey(); got != "first" {
		t.Errorf("default SortKey = %q, want first", got)
	}
	if got := (models.ListPrefs{Sort: "bogus"}).SortKey(); got != "first" {
		t.Errorf("invalid SortKey = %q, want first", got)
	}
	if got := (models.ListPrefs{Sort: "age"}).SortKey(); got != "age" {
		t.Errorf("SortKey(age) = %q", got)
	}
}

func TestListContactsSortOrders(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	owner, _ := models.CreateUser(ctx, d, "owner", "o@example.com", "h", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")

	mk := func(given, family, birthday, postal, country string) {
		in := models.ContactInput{GivenName: given, FamilyName: family, Birthday: birthday}
		if postal != "" || country != "" {
			in.Addresses = []vcardio.Address{{PostalCode: postal, Country: country}}
		}
		if _, err := models.CreateContact(ctx, d, book.ID, in); err != nil {
			t.Fatal(err)
		}
	}
	// given, family, birthday, postal, country
	mk("Zed", "Adams", "1990-01-01", "30301", "USA")
	mk("Amy", "Zephyr", "1950-06-15", "10001", "Canada")
	mk("Mia", "Brown", "", "", "") // no birthday, no address

	names := func(sort string, desc bool) []string {
		cs, _, err := models.ListContacts(ctx, d, book.ID, "", sort, desc, 50, 0)
		if err != nil {
			t.Fatalf("ListContacts(%q): %v", sort, err)
		}
		var out []string
		for _, c := range cs {
			out = append(out, c.FullName)
		}
		return out
	}

	if got := names("first", false); got[0] != "Amy Zephyr" || got[2] != "Zed Adams" {
		t.Errorf("first-name order = %v", got)
	}
	if got := names("last", false); got[0] != "Zed Adams" || got[1] != "Mia Brown" || got[2] != "Amy Zephyr" {
		t.Errorf("last-name order = %v", got)
	}
	// Age: oldest (earliest birthday) first; the birthday-less contact sorts last.
	if got := names("age", false); got[0] != "Amy Zephyr" || got[1] != "Zed Adams" || got[2] != "Mia Brown" {
		t.Errorf("age order = %v", got)
	}
	// Location: country then postal; the address-less contact sorts last.
	if got := names("location", false); got[0] != "Amy Zephyr" || got[1] != "Zed Adams" || got[2] != "Mia Brown" {
		t.Errorf("location order = %v", got)
	}

	// Descending flips the value order but keeps missing values last.
	if got := names("last", true); got[0] != "Amy Zephyr" || got[2] != "Zed Adams" {
		t.Errorf("last-name desc order = %v", got)
	}
	// Age desc = youngest (latest birthday) first; unknown still last.
	if got := names("age", true); got[0] != "Zed Adams" || got[1] != "Amy Zephyr" || got[2] != "Mia Brown" {
		t.Errorf("age desc order = %v", got)
	}
}

func TestBackfillSortKeys(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	owner, _ := models.CreateUser(ctx, d, "owner", "o@example.com", "h", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")

	c, err := models.CreateContact(ctx, d, book.ID, models.ContactInput{
		GivenName: "Grace", FamilyName: "Hopper",
		Addresses: []vcardio.Address{{PostalCode: "20701", Country: "USA"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate pre-migration rows: clear the denormalized columns, keep vcard_raw.
	if _, err := d.ExecWrite(ctx,
		"UPDATE contacts SET given_name=NULL, family_name=NULL, postal_code=NULL, country=NULL WHERE id=?", c.ID); err != nil {
		t.Fatal(err)
	}

	n, err := models.BackfillSortKeys(ctx, d)
	if err != nil || n != 1 {
		t.Fatalf("BackfillSortKeys = (%d,%v), want (1,nil)", n, err)
	}
	var given, family, postal, country string
	if err := d.QueryRowContext(ctx,
		"SELECT given_name, family_name, postal_code, country FROM contacts WHERE id=?", c.ID).
		Scan(&given, &family, &postal, &country); err != nil {
		t.Fatal(err)
	}
	if given != "Grace" || family != "Hopper" || postal != "20701" || country != "USA" {
		t.Errorf("backfilled = %q/%q/%q/%q", given, family, postal, country)
	}
	// Idempotent second run.
	if n, err := models.BackfillSortKeys(ctx, d); err != nil || n != 0 {
		t.Errorf("second backfill = (%d,%v), want (0,nil)", n, err)
	}
}
