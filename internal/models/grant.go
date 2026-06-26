package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/johannesheinz/skra/internal/db"
)

// Access levels for a per-address-book grant.
const (
	AccessViewer  = "viewer"
	AccessManager = "manager"
)

// GetGrant returns the access level a user holds on an address book. found is false when no grant row exists, which the RBAC layer treats as not-visible (404, not 403).
func GetGrant(ctx context.Context, d *db.DB, userID, addressBookID int64) (level string, found bool, err error) {
	err = d.QueryRowContext(ctx,
		`SELECT access_level FROM address_book_members
		 WHERE user_id = ? AND address_book_id = ?`,
		userID, addressBookID,
	).Scan(&level)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("models: get grant: %w", err)
	}
	return level, true, nil
}

// ValidAccessLevel reports whether level is a known per-book access level.
func ValidAccessLevel(level string) bool {
	return level == AccessViewer || level == AccessManager
}

// Member is a user's membership of an address book.
type Member struct {
	User        User
	AccessLevel string
}

// ListMembers returns the members of an address book with their access level, ordered by username.
func ListMembers(ctx context.Context, d *db.DB, addressBookID int64) ([]Member, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT u.id, u.public_id, u.username, u.email, u.password_hash, u.role, m.access_level
		 FROM address_book_members m
		 JOIN users u ON u.id = m.user_id
		 WHERE m.address_book_id = ?
		 ORDER BY u.username COLLATE NOCASE`, addressBookID)
	if err != nil {
		return nil, fmt.Errorf("models: list members: %w", err)
	}
	defer rows.Close()

	var members []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.User.ID, &m.User.PublicID, &m.User.Username, &m.User.Email,
			&m.User.PasswordHash, &m.User.Role, &m.AccessLevel); err != nil {
			return nil, fmt.Errorf("models: scan member: %w", err)
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: iterate members: %w", err)
	}
	return members, nil
}

// AddOrUpdateMember grants (or updates) a user's access level on a book.
func AddOrUpdateMember(ctx context.Context, d *db.DB, addressBookID, userID int64, level string, grantedBy int64) error {
	if !ValidAccessLevel(level) {
		return fmt.Errorf("models: invalid access level %q", level)
	}
	_, err := d.ExecWrite(ctx,
		`INSERT INTO address_book_members (address_book_id, user_id, access_level, granted_by)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(address_book_id, user_id) DO UPDATE SET access_level = excluded.access_level`,
		addressBookID, userID, level, grantedBy)
	if err != nil {
		return fmt.Errorf("models: add/update member: %w", err)
	}
	return nil
}

// RemoveMember revokes a user's membership of a book.
func RemoveMember(ctx context.Context, d *db.DB, addressBookID, userID int64) error {
	if _, err := d.ExecWrite(ctx,
		`DELETE FROM address_book_members WHERE address_book_id = ? AND user_id = ?`,
		addressBookID, userID); err != nil {
		return fmt.Errorf("models: remove member: %w", err)
	}
	return nil
}
