package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/johannesheinz/skra/internal/db"
	"github.com/johannesheinz/skra/internal/ids"
)

// SessionTTL is how long a session remains valid after creation.
const SessionTTL = 7 * 24 * time.Hour

// sessionTimeFormat matches SQLite's datetime('now') output (UTC, fixed width), so stored timestamps compare correctly with datetime('now') in SQL.
const sessionTimeFormat = "2006-01-02 15:04:05"

// ErrSessionNotFound is returned when a session id is unknown or expired.
var ErrSessionNotFound = errors.New("auth: session not found or expired")

// SessionStore manages server-side sessions in the sessions table.
// The cookie carries only the opaque, high-entropy session id; all state lives in the DB.
type SessionStore struct {
	db *db.DB
}

// NewSessionStore returns a store backed by d.
func NewSessionStore(d *db.DB) *SessionStore {
	return &SessionStore{db: d}
}

// Create issues a new session for userID and returns its id.
func (s *SessionStore) Create(ctx context.Context, userID int64) (string, error) {
	id, err := ids.NewSessionID()
	if err != nil {
		return "", err
	}
	expiresAt := time.Now().UTC().Add(SessionTTL).Format(sessionTimeFormat)
	if _, err := s.db.ExecWrite(ctx,
		`INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)`,
		id, userID, expiresAt); err != nil {
		return "", fmt.Errorf("auth: create session: %w", err)
	}
	return id, nil
}

// UserID returns the user id for a non-expired session, or ErrSessionNotFound.
func (s *SessionStore) UserID(ctx context.Context, sessionID string) (int64, error) {
	var userID int64
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id FROM sessions WHERE id = ? AND expires_at > datetime('now')`,
		sessionID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrSessionNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("auth: lookup session: %w", err)
	}
	return userID, nil
}

// Delete removes a session (logout).
// Deleting an unknown id is not an error.
func (s *SessionStore) Delete(ctx context.Context, sessionID string) error {
	if _, err := s.db.ExecWrite(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID); err != nil {
		return fmt.Errorf("auth: delete session: %w", err)
	}
	return nil
}

// DeleteExpired purges expired sessions; intended for periodic maintenance.
func (s *SessionStore) DeleteExpired(ctx context.Context) error {
	if _, err := s.db.ExecWrite(ctx, `DELETE FROM sessions WHERE expires_at <= datetime('now')`); err != nil {
		return fmt.Errorf("auth: delete expired sessions: %w", err)
	}
	return nil
}
