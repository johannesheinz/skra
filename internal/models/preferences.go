package models

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/johannesheinz/skra/internal/db"
)

// ThemePrefs is the user's theme choice. Empty Mode means "follow the system";
// empty Flavor/Accent mean the defaults.
type ThemePrefs struct {
	Mode   string `json:"mode,omitempty"`   // "" | "light" | "dark"
	Flavor string `json:"flavor,omitempty"` // "" | "solarized"
	Accent string `json:"accent,omitempty"` // "" | "pine" | "lime" | ...
}

// ListPrefs is the user's contact-list display choice: how many rows per page
// and the sort order. A zero PageSize means the default; -1 means "all". An
// empty Sort means the default order.
type ListPrefs struct {
	PageSize int    `json:"pageSize,omitempty"` // 0 = default, -1 = all, else the page size
	Sort     string `json:"sort,omitempty"`     // "" | "first" | "last" | "age" | "location"
	Desc     bool   `json:"desc,omitempty"`     // false = ascending, true = descending
}

// DefaultPageSize is used when the user has not chosen one.
const DefaultPageSize = 25

// AllowedPageSizes are the selectable page sizes; -1 renders every row.
var AllowedPageSizes = []int{10, 25, 50, 100, -1}

// AllowedSorts are the selectable sort keys mapped to their menu labels, in
// display order. The empty key is the default (equivalent to "first").
var AllowedSorts = []struct{ Key, Label string }{
	{"first", "First name"},
	{"last", "Last name"},
	{"age", "Age"},
	{"location", "Location"},
}

// PageLimit returns the SQL LIMIT for the chosen page size and whether the
// choice is "all" (no paging). An unrecognized size falls back to the default.
func (l ListPrefs) PageLimit() (limit int, all bool) {
	if l.PageSize == -1 {
		return -1, true // SQLite treats LIMIT -1 as no limit
	}
	for _, s := range AllowedPageSizes {
		if s == l.PageSize && s > 0 {
			return s, false
		}
	}
	return DefaultPageSize, false
}

// SortKey returns the validated sort key, defaulting to "first".
func (l ListPrefs) SortKey() string {
	for _, s := range AllowedSorts {
		if s.Key == l.Sort {
			return s.Key
		}
	}
	return "first"
}

// UIPreferences is the user's stored UI preferences blob. It is intentionally
// extensible — locale and accessibility settings can be added as sibling fields
// without a migration.
type UIPreferences struct {
	Theme ThemePrefs `json:"theme"`
	List  ListPrefs  `json:"list"`
}

// GetPreferences loads a user's UI preferences. A missing or malformed blob
// yields the zero value (all defaults) rather than an error.
func GetPreferences(ctx context.Context, d *db.DB, userID int64) (UIPreferences, error) {
	var raw string
	if err := d.QueryRowContext(ctx, "SELECT preferences FROM users WHERE id = ?", userID).Scan(&raw); err != nil {
		return UIPreferences{}, fmt.Errorf("models: get preferences: %w", err)
	}
	var prefs UIPreferences
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &prefs) // tolerate legacy/garbage → defaults
	}
	return prefs, nil
}

// UpdatePreferences stores a user's UI preferences.
func UpdatePreferences(ctx context.Context, d *db.DB, userID int64, prefs UIPreferences) error {
	blob, err := json.Marshal(prefs)
	if err != nil {
		return fmt.Errorf("models: marshal preferences: %w", err)
	}
	if _, err := d.ExecWrite(ctx,
		"UPDATE users SET preferences = ?, updated_at = datetime('now') WHERE id = ?", string(blob), userID); err != nil {
		return fmt.Errorf("models: update preferences: %w", err)
	}
	return nil
}

// UpdateEmail changes only a user's email (self-service profile edit).
func UpdateEmail(ctx context.Context, d *db.DB, userID int64, email string) error {
	if _, err := d.ExecWrite(ctx,
		"UPDATE users SET email = ?, updated_at = datetime('now') WHERE id = ?", email, userID); err != nil {
		return fmt.Errorf("models: update email: %w", err)
	}
	return nil
}

// Membership is a user's access to one address book, for the profile view.
type Membership struct {
	BookPublicID string
	BookName     string
	Level        string
}

// ListMembershipsForUser returns the books a user can access and at what level,
// ordered by book name.
func ListMembershipsForUser(ctx context.Context, d *db.DB, userID int64) ([]Membership, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT ab.public_id, ab.name, m.access_level
		 FROM address_book_members m
		 JOIN address_books ab ON ab.id = m.address_book_id
		 WHERE m.user_id = ?
		 ORDER BY ab.name COLLATE NOCASE`, userID)
	if err != nil {
		return nil, fmt.Errorf("models: list memberships: %w", err)
	}
	defer rows.Close()

	var out []Membership
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.BookPublicID, &m.BookName, &m.Level); err != nil {
			return nil, fmt.Errorf("models: scan membership: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: iterate memberships: %w", err)
	}
	return out, nil
}
