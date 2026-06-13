package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/johannesheinz/skra/internal/db"
)

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
