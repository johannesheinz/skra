package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/johannesheinz/skra/internal/db"
	"github.com/johannesheinz/skra/internal/ids"
)

// ErrAddressBookNotFound is returned when an address book lookup matches no row.
var ErrAddressBookNotFound = errors.New("models: address book not found")

// AddressBook is a collection of contacts owned by a user.
type AddressBook struct {
	ID          int64
	PublicID    string
	Name        string
	Description string
	OwnerID     int64
}

// AddressBookListItem is an address book plus its contact count, for listings.
type AddressBookListItem struct {
	AddressBook
	ContactCount int
}

// CreateAddressBook creates a book and grants its owner a manager membership in
// one transaction, so the owner can immediately manage it via the normal RBAC
// path. The name must be non-empty.
func CreateAddressBook(ctx context.Context, d *db.DB, ownerID int64, name, description string) (AddressBook, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return AddressBook{}, fmt.Errorf("models: address book name must not be empty")
	}
	publicID, err := ids.NewPublicID()
	if err != nil {
		return AddressBook{}, err
	}

	var id int64
	err = d.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO address_books (public_id, name, description, owner_id) VALUES (?, ?, ?, ?)`,
			publicID, name, nullString(description), ownerID)
		if err != nil {
			return fmt.Errorf("insert address book: %w", err)
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO address_book_members (address_book_id, user_id, access_level, granted_by)
			 VALUES (?, ?, ?, ?)`,
			id, ownerID, AccessManager, ownerID); err != nil {
			return fmt.Errorf("insert owner grant: %w", err)
		}
		return nil
	})
	if err != nil {
		return AddressBook{}, fmt.Errorf("models: create address book: %w", err)
	}

	return AddressBook{ID: id, PublicID: publicID, Name: name, Description: description, OwnerID: ownerID}, nil
}

// GetAddressBookByPublicID resolves a book by its public id.
func GetAddressBookByPublicID(ctx context.Context, d *db.DB, publicID string) (AddressBook, error) {
	var b AddressBook
	var description sql.NullString
	err := d.QueryRowContext(ctx,
		`SELECT id, public_id, name, description, owner_id FROM address_books WHERE public_id = ?`,
		publicID).Scan(&b.ID, &b.PublicID, &b.Name, &description, &b.OwnerID)
	if errors.Is(err, sql.ErrNoRows) {
		return AddressBook{}, ErrAddressBookNotFound
	}
	if err != nil {
		return AddressBook{}, fmt.Errorf("models: get address book: %w", err)
	}
	b.Description = description.String
	return b, nil
}

// UpdateAddressBook updates a book's name and description and bumps updated_at.
func UpdateAddressBook(ctx context.Context, d *db.DB, id int64, name, description string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("models: address book name must not be empty")
	}
	_, err := d.ExecWrite(ctx,
		`UPDATE address_books SET name = ?, description = ?, updated_at = datetime('now') WHERE id = ?`,
		name, nullString(description), id)
	if err != nil {
		return fmt.Errorf("models: update address book: %w", err)
	}
	return nil
}

// DeleteAddressBook removes a book; its contacts (and their photos) cascade.
func DeleteAddressBook(ctx context.Context, d *db.DB, id int64) error {
	if _, err := d.ExecWrite(ctx, `DELETE FROM address_books WHERE id = ?`, id); err != nil {
		return fmt.Errorf("models: delete address book: %w", err)
	}
	return nil
}

// ListAddressBooks returns the books visible to a user: all books for an admin,
// otherwise books the user owns or holds a grant on, each with its contact count.
func ListAddressBooks(ctx context.Context, d *db.DB, user User) ([]AddressBookListItem, error) {
	const countExpr = `(SELECT COUNT(*) FROM contacts c WHERE c.address_book_id = ab.id)`
	var (
		rows *sql.Rows
		err  error
	)
	if user.Role == RoleAdmin {
		rows, err = d.QueryContext(ctx,
			`SELECT ab.id, ab.public_id, ab.name, ab.description, ab.owner_id, `+countExpr+`
			 FROM address_books ab ORDER BY ab.name COLLATE NOCASE`)
	} else {
		rows, err = d.QueryContext(ctx,
			`SELECT ab.id, ab.public_id, ab.name, ab.description, ab.owner_id, `+countExpr+`
			 FROM address_books ab
			 WHERE ab.owner_id = ?
			    OR EXISTS (SELECT 1 FROM address_book_members m WHERE m.address_book_id = ab.id AND m.user_id = ?)
			 ORDER BY ab.name COLLATE NOCASE`, user.ID, user.ID)
	}
	if err != nil {
		return nil, fmt.Errorf("models: list address books: %w", err)
	}
	defer rows.Close()

	var items []AddressBookListItem
	for rows.Next() {
		var it AddressBookListItem
		var description sql.NullString
		if err := rows.Scan(&it.ID, &it.PublicID, &it.Name, &description, &it.OwnerID, &it.ContactCount); err != nil {
			return nil, fmt.Errorf("models: scan address book: %w", err)
		}
		it.Description = description.String
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: iterate address books: %w", err)
	}
	return items, nil
}

// nullString stores empty descriptions as SQL NULL.
func nullString(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
