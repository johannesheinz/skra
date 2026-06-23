package models

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/johannesheinz/skra/internal/db"
	"github.com/johannesheinz/skra/internal/ids"
	"github.com/johannesheinz/skra/internal/vcardio"
)

// ErrContactNotFound is returned when a contact lookup matches no row.
var ErrContactNotFound = errors.New("models: contact not found")

// Contact is a person/organization entry. The structured columns drive listing
// and search; vcard_raw holds the canonical full record (the hybrid model).
type Contact struct {
	ID            int64
	PublicID      string
	AddressBookID int64
	FullName      string
	Org           string
	PrimaryEmail  string
	PrimaryPhone  string
	HasPhoto      bool
	UID           string
	ETag          string
}

// ContactInput carries the editable fields of a contact. It supports the rich
// model (name components plus multi-value emails/phones/addresses) and keeps a
// couple of legacy convenience fields (FullName, PrimaryEmail, PrimaryPhone) so
// simple callers and tests stay terse.
type ContactInput struct {
	GivenName  string
	FamilyName string
	FullName   string // explicit display name, used when name components are absent
	Org        string
	Title      string
	Birthday   string
	Note       string
	Emails     []vcardio.Typed
	Phones     []vcardio.Typed
	Addresses  []vcardio.Address
	URLs       []string

	// Legacy convenience: folded into Emails/Phones when those are empty.
	PrimaryEmail string
	PrimaryPhone string
}

// Details converts the input to its rich vCard representation (also used to
// re-render the form after a validation error).
func (in ContactInput) Details() vcardio.Details {
	return in.toDetails()
}

func (in ContactInput) toDetails() vcardio.Details {
	d := vcardio.Details{
		GivenName:     strings.TrimSpace(in.GivenName),
		FamilyName:    strings.TrimSpace(in.FamilyName),
		FormattedName: strings.TrimSpace(in.FullName),
		Org:           strings.TrimSpace(in.Org),
		Title:         strings.TrimSpace(in.Title),
		Birthday:      strings.TrimSpace(in.Birthday),
		Note:          strings.TrimSpace(in.Note),
		Emails:        in.Emails,
		Phones:        in.Phones,
		Addresses:     in.Addresses,
		URLs:          in.URLs,
	}
	if len(d.Emails) == 0 && strings.TrimSpace(in.PrimaryEmail) != "" {
		d.Emails = []vcardio.Typed{{Value: strings.TrimSpace(in.PrimaryEmail)}}
	}
	if len(d.Phones) == 0 && strings.TrimSpace(in.PrimaryPhone) != "" {
		d.Phones = []vcardio.Typed{{Value: strings.TrimSpace(in.PrimaryPhone)}}
	}
	return d
}

// CreateContact inserts a contact via the hybrid write path: the full record is
// encoded into vcard_raw while the structured columns (display name, primary
// email/phone) are denormalized for listing and search. uid is stable; etag new.
func CreateContact(ctx context.Context, d *db.DB, addressBookID int64, in ContactInput) (Contact, error) {
	details := in.toDetails()
	name := details.DisplayName()
	if name == "" {
		return Contact{}, fmt.Errorf("models: contact must have a name")
	}
	publicID, err := ids.NewPublicID()
	if err != nil {
		return Contact{}, err
	}
	uid, err := ids.Random(16)
	if err != nil {
		return Contact{}, err
	}
	etag, err := ids.Random(16)
	if err != nil {
		return Contact{}, err
	}
	vcardRaw, err := vcardio.Encode(details, uid)
	if err != nil {
		return Contact{}, fmt.Errorf("models: encode vcard: %w", err)
	}

	given, family, postal, country := sortKeysFromDetails(details)
	res, err := d.ExecWrite(ctx,
		`INSERT INTO contacts
		   (public_id, address_book_id, full_name, org, primary_email, primary_phone, has_photo, vcard_raw, uid, etag,
		    birthday, given_name, family_name, postal_code, country)
		 VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?)`,
		publicID, addressBookID, name, nullString(details.Org), nullString(details.PrimaryEmail()),
		nullString(details.PrimaryPhone()), vcardRaw, uid, etag,
		NormalizeBirthday(details.Birthday), given, family, postal, country)
	if err != nil {
		return Contact{}, fmt.Errorf("models: insert contact: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Contact{}, err
	}

	return Contact{
		ID: id, PublicID: publicID, AddressBookID: addressBookID,
		FullName: name, Org: details.Org, PrimaryEmail: details.PrimaryEmail(), PrimaryPhone: details.PrimaryPhone(),
		UID: uid, ETag: etag,
	}, nil
}

// UpdateContact rewrites a contact's fields, regenerating vcard_raw and bumping
// the etag while preserving the stable uid.
func UpdateContact(ctx context.Context, d *db.DB, contact Contact, in ContactInput) error {
	details := in.toDetails()
	name := details.DisplayName()
	if name == "" {
		return fmt.Errorf("models: contact must have a name")
	}
	etag, err := ids.Random(16)
	if err != nil {
		return err
	}
	vcardRaw, err := vcardio.Encode(details, contact.UID)
	if err != nil {
		return fmt.Errorf("models: encode vcard: %w", err)
	}
	given, family, postal, country := sortKeysFromDetails(details)
	_, err = d.ExecWrite(ctx,
		`UPDATE contacts
		    SET full_name = ?, org = ?, primary_email = ?, primary_phone = ?,
		        vcard_raw = ?, etag = ?, birthday = ?,
		        given_name = ?, family_name = ?, postal_code = ?, country = ?,
		        updated_at = datetime('now')
		  WHERE id = ?`,
		name, nullString(details.Org), nullString(details.PrimaryEmail()), nullString(details.PrimaryPhone()),
		vcardRaw, etag, NormalizeBirthday(details.Birthday), given, family, postal, country, contact.ID)
	if err != nil {
		return fmt.Errorf("models: update contact: %w", err)
	}
	return nil
}

// GetContactDetails loads a contact and its parsed rich Details (from vcard_raw)
// for the detail and edit views.
func GetContactDetails(ctx context.Context, d *db.DB, publicID string) (Contact, vcardio.Details, error) {
	var c Contact
	var org, email, phone sql.NullString
	var hasPhoto int64
	var vcardRaw string
	err := d.QueryRowContext(ctx,
		`SELECT id, public_id, address_book_id, full_name, org, primary_email, primary_phone, has_photo, uid, etag, vcard_raw
		 FROM contacts WHERE public_id = ?`, publicID).
		Scan(&c.ID, &c.PublicID, &c.AddressBookID, &c.FullName, &org, &email, &phone, &hasPhoto, &c.UID, &c.ETag, &vcardRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return Contact{}, vcardio.Details{}, ErrContactNotFound
	}
	if err != nil {
		return Contact{}, vcardio.Details{}, fmt.Errorf("models: get contact details: %w", err)
	}
	c.Org = org.String
	c.PrimaryEmail = email.String
	c.PrimaryPhone = phone.String
	c.HasPhoto = hasPhoto != 0

	details, err := vcardio.Parse(vcardRaw)
	if err != nil {
		return Contact{}, vcardio.Details{}, fmt.Errorf("models: parse contact vcard: %w", err)
	}
	return c, details, nil
}

// DeleteContact removes a contact; its photo cascades.
func DeleteContact(ctx context.Context, d *db.DB, contactID int64) error {
	if _, err := d.ExecWrite(ctx, `DELETE FROM contacts WHERE id = ?`, contactID); err != nil {
		return fmt.Errorf("models: delete contact: %w", err)
	}
	return nil
}

// GetContactByPublicID resolves a contact by its public id.
func GetContactByPublicID(ctx context.Context, d *db.DB, publicID string) (Contact, error) {
	c, err := scanContact(d.QueryRowContext(ctx,
		`SELECT id, public_id, address_book_id, full_name, org, primary_email, primary_phone, has_photo, uid, etag
		 FROM contacts WHERE public_id = ?`, publicID))
	if errors.Is(err, sql.ErrNoRows) {
		return Contact{}, ErrContactNotFound
	}
	return c, err
}

// RecentContact is a recently-added contact with its book, for the dashboard.
type RecentContact struct {
	PublicID     string
	FullName     string
	HasPhoto     bool
	BookName     string
	BookPublicID string
}

// RecentContactsForUser returns the most recently created contacts across the
// books a user may see (admins see all), newest first.
func RecentContactsForUser(ctx context.Context, d *db.DB, user User, limit int) ([]RecentContact, error) {
	const base = `SELECT c.public_id, c.full_name, c.has_photo, ab.name, ab.public_id
		FROM contacts c JOIN address_books ab ON ab.id = c.address_book_id`
	var (
		rows *sql.Rows
		err  error
	)
	if user.Role == RoleAdmin {
		rows, err = d.QueryContext(ctx, base+` ORDER BY c.created_at DESC, c.id DESC LIMIT ?`, limit)
	} else {
		rows, err = d.QueryContext(ctx, base+`
			WHERE ab.owner_id = ?
			   OR EXISTS (SELECT 1 FROM address_book_members m WHERE m.address_book_id = ab.id AND m.user_id = ?)
			ORDER BY c.created_at DESC, c.id DESC LIMIT ?`, user.ID, user.ID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("models: recent contacts: %w", err)
	}
	defer rows.Close()

	var out []RecentContact
	for rows.Next() {
		var rc RecentContact
		var hasPhoto int64
		if err := rows.Scan(&rc.PublicID, &rc.FullName, &hasPhoto, &rc.BookName, &rc.BookPublicID); err != nil {
			return nil, fmt.Errorf("models: scan recent contact: %w", err)
		}
		rc.HasPhoto = hasPhoto != 0
		out = append(out, rc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: iterate recent contacts: %w", err)
	}
	return out, nil
}

// NormalizeBirthday canonicalises a birthday string to YYYY-MM-DD for the
// denormalized birthday column. A year-less birthday (vCard "--MMDD" /
// "--MM-DD") keeps year 0000. Anything unparseable — including an empty value —
// yields "", which the schema treats as "no birthday" (distinct from a NULL
// column that has not been backfilled yet).
func NormalizeBirthday(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var year, month, day int
	if rest, ok := strings.CutPrefix(s, "--"); ok {
		digits := strings.ReplaceAll(rest, "-", "")
		if len(digits) < 4 {
			return ""
		}
		month, day = atoi2(digits[0:2]), atoi2(digits[2:4])
	} else {
		digits := strings.ReplaceAll(s, "-", "")
		if len(digits) < 8 {
			return ""
		}
		year, month, day = atoi2(digits[0:4]), atoi2(digits[4:6]), atoi2(digits[6:8])
	}
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day)
}

// sortKeysFromDetails derives the denormalized list sort keys: the name
// components and the primary (first non-empty) address's postal code and
// country. Empty values are stored as "" (known-empty, distinct from a NULL
// not-yet-backfilled column).
func sortKeysFromDetails(d vcardio.Details) (given, family, postal, country string) {
	given = strings.TrimSpace(d.GivenName)
	family = strings.TrimSpace(d.FamilyName)
	for _, a := range d.Addresses {
		if !a.Empty() {
			return given, family, strings.TrimSpace(a.PostalCode), strings.TrimSpace(a.Country)
		}
	}
	return given, family, "", ""
}

// atoi2 parses a run of digits, returning -1 on any non-numeric input so the
// caller's range check rejects it.
func atoi2(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}

// UpcomingBirthday is a dashboard row: a contact whose birthday is next on the
// calendar within the books the viewer may see.
type UpcomingBirthday struct {
	PublicID     string
	FullName     string
	HasPhoto     bool
	BookName     string
	BookPublicID string
	Month        time.Month
	Day          int
	Age          int // age reached on the upcoming birthday; only set when HasAge
	HasAge       bool
	DaysUntil    int // whole days until the next occurrence (0 = today)
}

// DateLabel renders the day and abbreviated month, e.g. "Apr 1".
func (u UpcomingBirthday) DateLabel() string {
	return fmt.Sprintf("%s %d", u.Month.String()[:3], u.Day)
}

// CountdownLabel renders the countdown to the next occurrence in words.
func (u UpcomingBirthday) CountdownLabel() string {
	switch u.DaysUntil {
	case 0:
		return "Today"
	case 1:
		return "Tomorrow"
	default:
		return fmt.Sprintf("%d days", u.DaysUntil)
	}
}

// UpcomingBirthdaysForUser returns the contacts whose next birthday is soonest,
// scoped to the books a user may see (admins see all), ordered by the next
// calendar occurrence (wrapping past year-end). The ordering is computed in SQL
// from the normalized YYYY-MM-DD birthday; age is derived in Go against today.
func UpcomingBirthdaysForUser(ctx context.Context, d *db.DB, user User, limit int) ([]UpcomingBirthday, error) {
	// The upcoming occurrence is this year's month-day if it has not passed yet,
	// otherwise next year's. Lexicographic comparison of the YYYY-MM-DD strings
	// is correct because both sides are zero-padded and same-width.
	const nextOccurrence = `CASE
		WHEN strftime('%Y','now') || substr(c.birthday, 5) >= date('now')
		THEN strftime('%Y','now') || substr(c.birthday, 5)
		ELSE CAST(CAST(strftime('%Y','now') AS INTEGER) + 1 AS TEXT) || substr(c.birthday, 5)
	END`
	const base = `SELECT c.public_id, c.full_name, c.has_photo, ab.name, ab.public_id, c.birthday
		FROM contacts c JOIN address_books ab ON ab.id = c.address_book_id
		WHERE c.birthday IS NOT NULL AND length(c.birthday) = 10`
	var (
		rows *sql.Rows
		err  error
	)
	if user.Role == RoleAdmin {
		rows, err = d.QueryContext(ctx, base+` ORDER BY (`+nextOccurrence+`) ASC, c.full_name COLLATE NOCASE LIMIT ?`, limit)
	} else {
		rows, err = d.QueryContext(ctx, base+`
			AND (ab.owner_id = ?
			     OR EXISTS (SELECT 1 FROM address_book_members m WHERE m.address_book_id = ab.id AND m.user_id = ?))
			ORDER BY (`+nextOccurrence+`) ASC, c.full_name COLLATE NOCASE LIMIT ?`, user.ID, user.ID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("models: upcoming birthdays: %w", err)
	}
	defer rows.Close()

	now := time.Now()
	var out []UpcomingBirthday
	for rows.Next() {
		var (
			ub       UpcomingBirthday
			hasPhoto int64
			birthday string
		)
		if err := rows.Scan(&ub.PublicID, &ub.FullName, &hasPhoto, &ub.BookName, &ub.BookPublicID, &birthday); err != nil {
			return nil, fmt.Errorf("models: scan upcoming birthday: %w", err)
		}
		ub.HasPhoto = hasPhoto != 0
		year, month, day := atoi2(birthday[0:4]), atoi2(birthday[5:7]), atoi2(birthday[8:10])
		ub.Month, ub.Day = time.Month(month), day
		occYear := occurrenceYear(now, month, day)
		if year > 0 {
			ub.HasAge = true
			ub.Age = occYear - year
		}
		next := time.Date(occYear, time.Month(month), day, 0, 0, 0, 0, now.Location())
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		ub.DaysUntil = int(next.Sub(today).Hours()/24 + 0.5)
		out = append(out, ub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: iterate upcoming birthdays: %w", err)
	}
	return out, nil
}

// occurrenceYear returns the year in which the given month-day next occurs
// relative to now (this year if it has not passed, otherwise next year).
func occurrenceYear(now time.Time, month, day int) int {
	if month < int(now.Month()) || (month == int(now.Month()) && day < now.Day()) {
		return now.Year() + 1
	}
	return now.Year()
}

// BackfillBirthdays populates the birthday column for contacts that predate the
// column (birthday IS NULL) by parsing their stored vCard. Rows without a BDAY
// get an empty string so they are not rescanned. It is idempotent and cheap on
// subsequent runs (the NULL filter matches nothing), so it is safe to call on
// every startup. Returns the number of rows updated.
func BackfillBirthdays(ctx context.Context, d *db.DB) (int, error) {
	rows, err := d.QueryContext(ctx, `SELECT id, vcard_raw FROM contacts WHERE birthday IS NULL`)
	if err != nil {
		return 0, fmt.Errorf("models: backfill birthdays scan: %w", err)
	}
	type pending struct {
		id       int64
		birthday string
	}
	var todo []pending
	for rows.Next() {
		var (
			id       int64
			vcardRaw string
		)
		if err := rows.Scan(&id, &vcardRaw); err != nil {
			rows.Close()
			return 0, fmt.Errorf("models: backfill birthdays row: %w", err)
		}
		details, err := vcardio.Parse(vcardRaw)
		if err != nil {
			rows.Close()
			return 0, fmt.Errorf("models: backfill parse contact %d: %w", id, err)
		}
		todo = append(todo, pending{id: id, birthday: NormalizeBirthday(details.Birthday)})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("models: backfill iterate: %w", err)
	}
	rows.Close()

	for _, p := range todo {
		if _, err := d.ExecWrite(ctx, `UPDATE contacts SET birthday = ? WHERE id = ?`, p.birthday, p.id); err != nil {
			return 0, fmt.Errorf("models: backfill update contact %d: %w", p.id, err)
		}
	}
	return len(todo), nil
}

// BackfillSortKeys populates the denormalized list sort columns (given_name,
// family_name, postal_code, country) for contacts that predate them
// (family_name IS NULL) by parsing their stored vCard. Like BackfillBirthdays
// it is idempotent — the NULL filter matches nothing once every row has been
// visited — so it is safe to call on every startup. Returns the rows updated.
func BackfillSortKeys(ctx context.Context, d *db.DB) (int, error) {
	rows, err := d.QueryContext(ctx, `SELECT id, vcard_raw FROM contacts WHERE family_name IS NULL`)
	if err != nil {
		return 0, fmt.Errorf("models: backfill sort keys scan: %w", err)
	}
	type pending struct {
		id                             int64
		given, family, postal, country string
	}
	var todo []pending
	for rows.Next() {
		var (
			id       int64
			vcardRaw string
		)
		if err := rows.Scan(&id, &vcardRaw); err != nil {
			rows.Close()
			return 0, fmt.Errorf("models: backfill sort keys row: %w", err)
		}
		details, err := vcardio.Parse(vcardRaw)
		if err != nil {
			rows.Close()
			return 0, fmt.Errorf("models: backfill sort keys parse contact %d: %w", id, err)
		}
		g, f, p, c := sortKeysFromDetails(details)
		todo = append(todo, pending{id: id, given: g, family: f, postal: p, country: c})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("models: backfill sort keys iterate: %w", err)
	}
	rows.Close()

	for _, p := range todo {
		if _, err := d.ExecWrite(ctx,
			`UPDATE contacts SET given_name = ?, family_name = ?, postal_code = ?, country = ? WHERE id = ?`,
			p.given, p.family, p.postal, p.country, p.id); err != nil {
			return 0, fmt.Errorf("models: backfill sort keys update contact %d: %w", p.id, err)
		}
	}
	return len(todo), nil
}

// GetContactByID resolves a contact by its internal id.
func GetContactByID(ctx context.Context, d *db.DB, id int64) (Contact, error) {
	c, err := scanContact(d.QueryRowContext(ctx,
		`SELECT id, public_id, address_book_id, full_name, org, primary_email, primary_phone, has_photo, uid, etag
		 FROM contacts WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Contact{}, ErrContactNotFound
	}
	return c, err
}

// contactOrderBy maps a user-facing sort key to a SQL ORDER BY clause. The key
// is whitelisted here (never interpolated from user input), so it is safe to
// concatenate. Empty/unknown values always sort last; an unrecognized key falls
// back to given-name order.
func contactOrderBy(sort string) string {
	const byName = "full_name COLLATE NOCASE"
	switch sort {
	case "last":
		return "(family_name IS NULL OR family_name = ''), family_name COLLATE NOCASE, " + byName
	case "age":
		// Oldest first by birth date; unknown (NULL/empty/year-less) sort last.
		return "(birthday IS NULL OR birthday = '' OR substr(birthday,1,4) = '0000'), birthday, " + byName
	case "location":
		return "(country IS NULL OR country = ''), country COLLATE NOCASE, postal_code COLLATE NOCASE, " + byName
	default: // "first" and the default
		return "(given_name IS NULL OR given_name = ''), given_name COLLATE NOCASE, " + byName
	}
}

// ListContacts returns a page of contacts in a book, optionally filtered by a
// case-insensitive substring across the structured columns and ordered by the
// given (whitelisted) sort key, plus the total matching count for pagination.
// A limit of -1 returns every matching row (SQLite treats LIMIT -1 as no limit).
func ListContacts(ctx context.Context, d *db.DB, addressBookID int64, query, sort string, limit, offset int) ([]Contact, int, error) {
	where := "address_book_id = ?"
	args := []any{addressBookID}
	if q := strings.TrimSpace(query); q != "" {
		like := "%" + q + "%"
		where += " AND (full_name LIKE ? OR org LIKE ? OR primary_email LIKE ? OR primary_phone LIKE ?)"
		args = append(args, like, like, like, like)
	}

	var total int
	if err := d.QueryRowContext(ctx, "SELECT COUNT(*) FROM contacts WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("models: count contacts: %w", err)
	}

	pageArgs := append(append([]any{}, args...), limit, offset)
	rows, err := d.QueryContext(ctx,
		`SELECT id, public_id, address_book_id, full_name, org, primary_email, primary_phone, has_photo, uid, etag
		 FROM contacts WHERE `+where+`
		 ORDER BY `+contactOrderBy(sort)+` LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("models: list contacts: %w", err)
	}
	defer rows.Close()

	var contacts []Contact
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, 0, err
		}
		contacts = append(contacts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("models: iterate contacts: %w", err)
	}
	return contacts, total, nil
}

// ContactExport carries the fields needed to export a contact: the structured
// columns for CSV and the canonical vcard_raw for vCard.
type ContactExport struct {
	ID       int64
	FullName string
	Org      string
	Email    string
	Phone    string
	VCardRaw string
	HasPhoto bool
}

// ListContactsForExport returns every contact in a book (no pagination) with the
// data needed for vCard and CSV export, ordered by name.
func ListContactsForExport(ctx context.Context, d *db.DB, addressBookID int64) ([]ContactExport, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, full_name, org, primary_email, primary_phone, vcard_raw, has_photo
		 FROM contacts WHERE address_book_id = ? ORDER BY full_name COLLATE NOCASE`, addressBookID)
	if err != nil {
		return nil, fmt.Errorf("models: list contacts for export: %w", err)
	}
	defer rows.Close()

	var out []ContactExport
	for rows.Next() {
		var c ContactExport
		var org, email, phone sql.NullString
		var hasPhoto int64
		if err := rows.Scan(&c.ID, &c.FullName, &org, &email, &phone, &c.VCardRaw, &hasPhoto); err != nil {
			return nil, fmt.Errorf("models: scan export contact: %w", err)
		}
		c.Org = org.String
		c.Email = email.String
		c.Phone = phone.String
		c.HasPhoto = hasPhoto != 0
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: iterate export contacts: %w", err)
	}
	return out, nil
}

// GetPhotoBytes returns a contact's stored JPEG by internal id, for export.
func GetPhotoBytes(ctx context.Context, d *db.DB, contactID int64) ([]byte, bool, error) {
	var b []byte
	err := d.QueryRowContext(ctx, `SELECT bytes FROM contact_photos WHERE contact_id = ?`, contactID).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("models: get photo bytes: %w", err)
	}
	return b, true, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanContact(row rowScanner) (Contact, error) {
	var c Contact
	var org, email, phone sql.NullString
	var hasPhoto int64
	if err := row.Scan(&c.ID, &c.PublicID, &c.AddressBookID, &c.FullName,
		&org, &email, &phone, &hasPhoto, &c.UID, &c.ETag); err != nil {
		return Contact{}, fmt.Errorf("models: scan contact: %w", err)
	}
	c.Org = org.String
	c.PrimaryEmail = email.String
	c.PrimaryPhone = phone.String
	c.HasPhoto = hasPhoto != 0
	return c, nil
}

// --- Photo metadata/bytes (used by the photo-serving endpoint) ---

// PhotoMeta is the lightweight metadata needed to authorize and conditionally
// serve a contact photo without loading the BLOB.
type PhotoMeta struct {
	AddressBookID int64
	ETag          string
}

// Photo is a contact photo BLOB and its content type.
type Photo struct {
	MIMEType string
	Bytes    []byte
	ETag     string
}

// GetContactPhotoMeta returns the owning book id and the photo ETag for a
// contact, looked up by public_id. found is false when the contact does not
// exist or has no photo (both resolve to 404 at the handler).
func GetContactPhotoMeta(ctx context.Context, d *db.DB, contactPublicID string) (PhotoMeta, bool, error) {
	var m PhotoMeta
	err := d.QueryRowContext(ctx,
		`SELECT c.address_book_id, cp.etag
		 FROM contacts c
		 JOIN contact_photos cp ON cp.contact_id = c.id
		 WHERE c.public_id = ?`, contactPublicID).Scan(&m.AddressBookID, &m.ETag)
	if errors.Is(err, sql.ErrNoRows) {
		return PhotoMeta{}, false, nil
	}
	if err != nil {
		return PhotoMeta{}, false, fmt.Errorf("models: get contact photo meta: %w", err)
	}
	return m, true, nil
}

// SetContactPhoto stores (or replaces) a contact's normalized JPEG photo and
// flags the contact as having one, in a single transaction. The ETag is the
// content hash, so re-uploading identical bytes is a no-op for caches.
func SetContactPhoto(ctx context.Context, d *db.DB, contactID int64, jpeg []byte) error {
	sum := sha256.Sum256(jpeg)
	etag := hex.EncodeToString(sum[:])
	err := d.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO contact_photos (contact_id, mime_type, bytes, etag)
			 VALUES (?, 'image/jpeg', ?, ?)
			 ON CONFLICT(contact_id) DO UPDATE SET
			   mime_type = excluded.mime_type, bytes = excluded.bytes,
			   etag = excluded.etag, updated_at = datetime('now')`,
			contactID, jpeg, etag); err != nil {
			return fmt.Errorf("upsert photo: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE contacts SET has_photo = 1, updated_at = datetime('now') WHERE id = ?`, contactID); err != nil {
			return fmt.Errorf("flag has_photo: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("models: set contact photo: %w", err)
	}
	return nil
}

// DeleteContactPhoto removes a contact's photo and clears its has_photo flag.
func DeleteContactPhoto(ctx context.Context, d *db.DB, contactID int64) error {
	err := d.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM contact_photos WHERE contact_id = ?`, contactID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE contacts SET has_photo = 0, updated_at = datetime('now') WHERE id = ?`, contactID)
		return err
	})
	if err != nil {
		return fmt.Errorf("models: delete contact photo: %w", err)
	}
	return nil
}

// GetContactPhoto loads the photo BLOB for a contact by public_id.
func GetContactPhoto(ctx context.Context, d *db.DB, contactPublicID string) (Photo, bool, error) {
	var p Photo
	err := d.QueryRowContext(ctx,
		`SELECT cp.mime_type, cp.bytes, cp.etag
		 FROM contacts c
		 JOIN contact_photos cp ON cp.contact_id = c.id
		 WHERE c.public_id = ?`, contactPublicID).Scan(&p.MIMEType, &p.Bytes, &p.ETag)
	if errors.Is(err, sql.ErrNoRows) {
		return Photo{}, false, nil
	}
	if err != nil {
		return Photo{}, false, fmt.Errorf("models: get contact photo: %w", err)
	}
	return p, true, nil
}
