package models

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

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

	res, err := d.ExecWrite(ctx,
		`INSERT INTO contacts
		   (public_id, address_book_id, full_name, org, primary_email, primary_phone, has_photo, vcard_raw, uid, etag)
		 VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?)`,
		publicID, addressBookID, name, nullString(details.Org), nullString(details.PrimaryEmail()),
		nullString(details.PrimaryPhone()), vcardRaw, uid, etag)
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
	_, err = d.ExecWrite(ctx,
		`UPDATE contacts
		    SET full_name = ?, org = ?, primary_email = ?, primary_phone = ?,
		        vcard_raw = ?, etag = ?, updated_at = datetime('now')
		  WHERE id = ?`,
		name, nullString(details.Org), nullString(details.PrimaryEmail()), nullString(details.PrimaryPhone()),
		vcardRaw, etag, contact.ID)
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

// ListContacts returns a page of contacts in a book, optionally filtered by a
// case-insensitive substring across the structured columns, plus the total
// matching count for pagination.
func ListContacts(ctx context.Context, d *db.DB, addressBookID int64, query string, limit, offset int) ([]Contact, int, error) {
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
		 ORDER BY full_name COLLATE NOCASE LIMIT ? OFFSET ?`, pageArgs...)
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
