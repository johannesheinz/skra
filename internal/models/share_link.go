package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/johannesheinz/skra/internal/db"
	"github.com/johannesheinz/skra/internal/sharing"
)

// ErrShareLinkNotFound is returned when a share-link lookup matches no row.
var ErrShareLinkNotFound = errors.New("models: share link not found")

// sqliteTimeFormat matches SQLite's datetime('now') output (UTC, fixed width).
const sqliteTimeFormat = "2006-01-02 15:04:05"

// ShareLink is a capability link to a book or contact.
type ShareLink struct {
	ID          int64
	Token       string
	Mode        string
	Scope       string
	TargetID    int64
	SecretHash  string // empty unless mode is gated_short
	MaxUses     int64  // 0 means unlimited
	UseCount    int64
	FailedCount int64
	ExpiresAt   string // empty means no expiry; SQLite datetime format (UTC)
	Revoked     bool
	CreatedBy   int64
}

// NewShareLinkParams describes a share link to create.
type NewShareLinkParams struct {
	Mode       string
	Scope      string
	TargetID   int64
	SecretHash string // required iff Mode == gated_short
	MaxUses    int64  // 0 = unlimited
	ExpiresAt  string // "" = no expiry
	CreatedBy  int64
}

// CreateShareLink inserts a share link after enforcing the cross-cutting invariant that a gated link must carry a secret.
// The token is generated for the mode (public_long is high-entropy).
// Returns the stored link.
func CreateShareLink(ctx context.Context, d *db.DB, p NewShareLinkParams) (ShareLink, error) {
	if !sharing.ValidMode(p.Mode) {
		return ShareLink{}, fmt.Errorf("models: invalid share mode %q", p.Mode)
	}
	if !sharing.ValidScope(p.Scope) {
		return ShareLink{}, fmt.Errorf("models: invalid share scope %q", p.Scope)
	}
	if p.Mode == sharing.ModeGated && p.SecretHash == "" {
		return ShareLink{}, fmt.Errorf("models: gated_short share requires a secret")
	}
	if p.Mode != sharing.ModeGated && p.SecretHash != "" {
		return ShareLink{}, fmt.Errorf("models: only gated_short shares carry a secret")
	}

	token, err := sharing.NewToken(p.Mode)
	if err != nil {
		return ShareLink{}, err
	}

	res, err := d.ExecWrite(ctx,
		`INSERT INTO share_links (token, mode, scope, target_id, secret_hash, max_uses, expires_at, created_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		token, p.Mode, p.Scope, p.TargetID,
		nullableString(p.SecretHash), nullableInt(p.MaxUses), nullableString(p.ExpiresAt), p.CreatedBy)
	if err != nil {
		return ShareLink{}, fmt.Errorf("models: insert share link: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ShareLink{}, err
	}
	return ShareLink{
		ID: id, Token: token, Mode: p.Mode, Scope: p.Scope, TargetID: p.TargetID,
		SecretHash: p.SecretHash, MaxUses: p.MaxUses, ExpiresAt: p.ExpiresAt, CreatedBy: p.CreatedBy,
	}, nil
}

// GetShareLinkByToken resolves a share link by its token.
func GetShareLinkByToken(ctx context.Context, d *db.DB, token string) (ShareLink, error) {
	return scanShareLink(d.QueryRowContext(ctx,
		`SELECT id, token, mode, scope, target_id, secret_hash, max_uses, use_count, failed_count, expires_at, revoked, created_by
		 FROM share_links WHERE token = ?`, token))
}

// ListShareLinksForTarget returns the share links for a given scope+target, newest first.
func ListShareLinksForTarget(ctx context.Context, d *db.DB, scope string, targetID int64) ([]ShareLink, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, token, mode, scope, target_id, secret_hash, max_uses, use_count, failed_count, expires_at, revoked, created_by
		 FROM share_links WHERE scope = ? AND target_id = ? ORDER BY id DESC`, scope, targetID)
	if err != nil {
		return nil, fmt.Errorf("models: list share links: %w", err)
	}
	defer rows.Close()

	var links []ShareLink
	for rows.Next() {
		link, err := scanShareLink(rows)
		if err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: iterate share links: %w", err)
	}
	return links, nil
}

// RevokeShareLink marks a share link revoked.
func RevokeShareLink(ctx context.Context, d *db.DB, id int64) error {
	if _, err := d.ExecWrite(ctx, `UPDATE share_links SET revoked = 1 WHERE id = ?`, id); err != nil {
		return fmt.Errorf("models: revoke share link: %w", err)
	}
	return nil
}

// IncrementShareUse bumps the use counter (called once per top-level access).
func IncrementShareUse(ctx context.Context, d *db.DB, id int64) error {
	if _, err := d.ExecWrite(ctx, `UPDATE share_links SET use_count = use_count + 1 WHERE id = ?`, id); err != nil {
		return fmt.Errorf("models: increment share use: %w", err)
	}
	return nil
}

// IncrementShareFailure bumps the failed-attempt counter for gate throttling.
func IncrementShareFailure(ctx context.Context, d *db.DB, id int64) error {
	if _, err := d.ExecWrite(ctx, `UPDATE share_links SET failed_count = failed_count + 1 WHERE id = ?`, id); err != nil {
		return fmt.Errorf("models: increment share failure: %w", err)
	}
	return nil
}

// Usable reports whether the link may currently be served as a counted view: not revoked, not expired, not uses-exhausted, and not locked by too many gate failures.
func (s ShareLink) Usable(now time.Time) bool {
	if s.MaxUses > 0 && s.UseCount >= s.MaxUses {
		return false
	}
	return s.Servable(now)
}

// Servable reports whether a sub-resource of a served view (e.g. a contact photo) may be delivered: everything Usable checks except the uses-exhausted limit.
// A max-uses limit caps page views, not the assets a counted view pulls in; without this the final counted view would render but its photo request — arriving after the view incremented use_count to the limit — would be refused.
func (s ShareLink) Servable(now time.Time) bool {
	if s.Revoked {
		return false
	}
	if s.FailedCount >= sharing.GateMaxFailures {
		return false
	}
	if s.ExpiresAt != "" {
		exp, err := time.Parse(sqliteTimeFormat, s.ExpiresAt)
		if err != nil || !now.UTC().Before(exp) {
			return false
		}
	}
	return true
}

func scanShareLink(row rowScanner) (ShareLink, error) {
	var s ShareLink
	var secret, expires sql.NullString
	var maxUses sql.NullInt64
	var revoked int64
	err := row.Scan(&s.ID, &s.Token, &s.Mode, &s.Scope, &s.TargetID,
		&secret, &maxUses, &s.UseCount, &s.FailedCount, &expires, &revoked, &s.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return ShareLink{}, ErrShareLinkNotFound
	}
	if err != nil {
		return ShareLink{}, fmt.Errorf("models: scan share link: %w", err)
	}
	s.SecretHash = secret.String
	s.ExpiresAt = expires.String
	s.MaxUses = maxUses.Int64
	s.Revoked = revoked != 0
	return s, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
