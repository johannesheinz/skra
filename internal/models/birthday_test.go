package models_test

import (
	"context"
	"testing"
	"time"

	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
)

func TestNormalizeBirthday(t *testing.T) {
	cases := map[string]string{
		"1990-04-01": "1990-04-01",
		"19900401":   "1990-04-01",
		"--04-01":    "0000-04-01", // year-less vCard, extended
		"--0401":     "0000-04-01", // year-less vCard, basic
		"  ":         "",
		"":           "",
		"nonsense":   "",
		"1990-13-01": "", // month out of range
		"1990-04-32": "", // day out of range
	}
	for in, want := range cases {
		if got := models.NormalizeBirthday(in); got != want {
			t.Errorf("NormalizeBirthday(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUpcomingBirthdaysOrderAndAge(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	owner, _ := models.CreateUser(ctx, d, "owner", "o@example.com", "h", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")

	now := time.Now()
	// A birthday n days from today, at a fixed birth year.
	inDays := func(n, year int) string {
		day := now.AddDate(0, 0, n)
		return time.Date(year, day.Month(), day.Day(), 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	}

	// Soonest first regardless of insertion order.
	if _, err := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Far", Birthday: inDays(40, 1980)}); err != nil {
		t.Fatal(err)
	}
	if _, err := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Soon", Birthday: inDays(3, 1990)}); err != nil {
		t.Fatal(err)
	}
	// No birthday -> excluded. Year-less -> included but no age.
	if _, err := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "None"}); err != nil {
		t.Fatal(err)
	}
	if _, err := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Yearless", Birthday: "--" + now.AddDate(0, 0, 10).Format("0102")}); err != nil {
		t.Fatal(err)
	}

	got, err := models.UpcomingBirthdaysForUser(ctx, d, owner, 5)
	if err != nil {
		t.Fatalf("UpcomingBirthdaysForUser: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d birthdays, want 3 (None excluded)", len(got))
	}
	if got[0].FullName != "Soon" || got[1].FullName != "Yearless" || got[2].FullName != "Far" {
		t.Errorf("order = %q/%q/%q, want Soon/Yearless/Far", got[0].FullName, got[1].FullName, got[2].FullName)
	}
	// "Soon" turns the age it reaches on its next occurrence (3 days out, so that occurrence's year, which may roll over near year-end).
	wantAge := now.AddDate(0, 0, 3).Year() - 1990
	if !got[0].HasAge || got[0].Age != wantAge {
		t.Errorf("Soon age = %d (hasAge=%v), want %d", got[0].Age, got[0].HasAge, wantAge)
	}
	if got[1].HasAge {
		t.Error("year-less birthday should not report an age")
	}
}

func TestBackfillBirthdays(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	owner, _ := models.CreateUser(ctx, d, "owner", "o@example.com", "h", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")

	c, err := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Has BDay", Birthday: "1985-06-15"})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a pre-migration row: clear the denormalized column, keep vcard_raw.
	if _, err := d.ExecWrite(ctx, "UPDATE contacts SET birthday = NULL WHERE id = ?", c.ID); err != nil {
		t.Fatal(err)
	}

	n, err := models.BackfillBirthdays(ctx, d)
	if err != nil {
		t.Fatalf("BackfillBirthdays: %v", err)
	}
	if n != 1 {
		t.Fatalf("backfilled %d rows, want 1", n)
	}
	var bday string
	if err := d.QueryRowContext(ctx, "SELECT birthday FROM contacts WHERE id = ?", c.ID).Scan(&bday); err != nil {
		t.Fatal(err)
	}
	if bday != "1985-06-15" {
		t.Errorf("backfilled birthday = %q, want 1985-06-15", bday)
	}
	// Idempotent: a second run finds no NULLs.
	if n, err := models.BackfillBirthdays(ctx, d); err != nil || n != 0 {
		t.Errorf("second backfill = (%d, %v), want (0, nil)", n, err)
	}
}
