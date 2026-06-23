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

// ErrImportUploadNotFound is returned when a staged upload token is unknown.
var ErrImportUploadNotFound = errors.New("models: import upload not found")

// ImportUpload is a staged upload awaiting confirmation.
type ImportUpload struct {
	BookID int64
	UserID int64
	Format string
	Bytes  []byte
}

// CreateImportUpload stages an uploaded file and returns its token.
func CreateImportUpload(ctx context.Context, d *db.DB, bookID, userID int64, format string, data []byte) (string, error) {
	token, err := ids.Random(18)
	if err != nil {
		return "", err
	}
	if _, err := d.ExecWrite(ctx,
		`INSERT INTO import_uploads (token, book_id, user_id, format, bytes) VALUES (?, ?, ?, ?, ?)`,
		token, bookID, userID, format, data); err != nil {
		return "", fmt.Errorf("models: create import upload: %w", err)
	}
	return token, nil
}

// GetImportUpload loads a staged upload by token.
func GetImportUpload(ctx context.Context, d *db.DB, token string) (ImportUpload, error) {
	var u ImportUpload
	err := d.QueryRowContext(ctx,
		`SELECT book_id, user_id, format, bytes FROM import_uploads WHERE token = ?`, token).
		Scan(&u.BookID, &u.UserID, &u.Format, &u.Bytes)
	if errors.Is(err, sql.ErrNoRows) {
		return ImportUpload{}, ErrImportUploadNotFound
	}
	if err != nil {
		return ImportUpload{}, fmt.Errorf("models: get import upload: %w", err)
	}
	return u, nil
}

// DeleteImportUpload removes a staged upload (after commit or cancel).
func DeleteImportUpload(ctx context.Context, d *db.DB, token string) error {
	if _, err := d.ExecWrite(ctx, `DELETE FROM import_uploads WHERE token = ?`, token); err != nil {
		return fmt.Errorf("models: delete import upload: %w", err)
	}
	return nil
}

// DeleteStaleImportUploads purges staged uploads older than an hour.
func DeleteStaleImportUploads(ctx context.Context, d *db.DB) error {
	if _, err := d.ExecWrite(ctx,
		`DELETE FROM import_uploads WHERE created_at <= datetime('now','-1 hour')`); err != nil {
		return fmt.Errorf("models: delete stale import uploads: %w", err)
	}
	return nil
}

// ExistingContactKeys returns the set of UIDs and (lowercased) primary emails
// already present in a book, for de-duplication.
func ExistingContactKeys(ctx context.Context, d *db.DB, addressBookID int64) (uids, emails map[string]bool, err error) {
	rows, err := d.QueryContext(ctx,
		`SELECT uid, primary_email FROM contacts WHERE address_book_id = ?`, addressBookID)
	if err != nil {
		return nil, nil, fmt.Errorf("models: existing contact keys: %w", err)
	}
	defer rows.Close()

	uids = make(map[string]bool)
	emails = make(map[string]bool)
	for rows.Next() {
		var uid string
		var email sql.NullString
		if err := rows.Scan(&uid, &email); err != nil {
			return nil, nil, fmt.Errorf("models: scan contact key: %w", err)
		}
		if uid != "" {
			uids[uid] = true
		}
		if e := strings.ToLower(strings.TrimSpace(email.String)); e != "" {
			emails[e] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("models: iterate contact keys: %w", err)
	}
	return uids, emails, nil
}

// PreparedImport is a fully-resolved contact ready for bulk insertion: the uid
// and vcard_raw are final (the caller handles dedup and any uid minting).
type PreparedImport struct {
	FullName  string
	Org       string
	Email     string
	Phone     string
	Birthday  string // raw BDAY from the source card; normalized on insert
	VCardRaw  string
	UID       string
	PhotoJPEG []byte // optional, already normalized
}

// ImportContacts inserts a batch of contacts (and any photos) into a book in a
// single transaction, so the import is all-or-nothing. Returns the count.
func ImportContacts(ctx context.Context, d *db.DB, addressBookID int64, records []PreparedImport) (int, error) {
	if len(records) == 0 {
		return 0, nil
	}
	err := d.Write(ctx, func(tx *sql.Tx) error {
		for _, rec := range records {
			publicID, err := ids.NewPublicID()
			if err != nil {
				return err
			}
			etag, err := ids.Random(16)
			if err != nil {
				return err
			}
			hasPhoto := 0
			if len(rec.PhotoJPEG) > 0 {
				hasPhoto = 1
			}
			// Derive the denormalized sort keys from the canonical card so imported
			// contacts sort correctly immediately (not only after a restart's backfill).
			var given, family, postal, country string
			if details, err := vcardio.Parse(rec.VCardRaw); err == nil {
				given, family, postal, country = sortKeysFromDetails(details)
			}
			res, err := tx.ExecContext(ctx,
				`INSERT INTO contacts
				   (public_id, address_book_id, full_name, org, primary_email, primary_phone, has_photo, vcard_raw, uid, etag,
				    birthday, given_name, family_name, postal_code, country)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				publicID, addressBookID, rec.FullName, nullString(rec.Org), nullString(rec.Email),
				nullString(rec.Phone), hasPhoto, rec.VCardRaw, rec.UID, etag,
				NormalizeBirthday(rec.Birthday), given, family, postal, country)
			if err != nil {
				return fmt.Errorf("insert contact: %w", err)
			}
			if hasPhoto == 1 {
				contactID, err := res.LastInsertId()
				if err != nil {
					return err
				}
				sum := sha256.Sum256(rec.PhotoJPEG)
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO contact_photos (contact_id, mime_type, bytes, etag) VALUES (?, 'image/jpeg', ?, ?)`,
					contactID, rec.PhotoJPEG, hex.EncodeToString(sum[:])); err != nil {
					return fmt.Errorf("insert photo: %w", err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("models: import contacts: %w", err)
	}
	return len(records), nil
}
