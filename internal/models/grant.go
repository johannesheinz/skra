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

// GetGrant returns the access level a user holds on an address book. found is
// false when no grant row exists, which the RBAC layer treats as
// not-visible (404, not 403).
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
