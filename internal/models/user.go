// Package models holds the data-access functions for Skra's core entities.
// Internal integer ids stay within this layer and the database; callers outside
// reference rows by public_id.
package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/johannesheinz/skra/internal/db"
	"github.com/johannesheinz/skra/internal/ids"
)

// Roles are the global account roles. There is no default; an unrecognized role
// is rejected.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// ErrUserNotFound is returned when a user lookup matches no row.
var ErrUserNotFound = errors.New("models: user not found")

// User is an account. PasswordHash is the PHC-encoded argon2id string.
type User struct {
	ID           int64
	PublicID     string
	Username     string
	Email        string
	PasswordHash string
	Role         string
}

// CreateUser inserts a new user with a freshly generated public_id and returns
// the stored row. The role must be RoleAdmin or RoleUser.
func CreateUser(ctx context.Context, d *db.DB, username, email, passwordHash, role string) (User, error) {
	if role != RoleAdmin && role != RoleUser {
		return User{}, fmt.Errorf("models: invalid role %q", role)
	}
	publicID, err := ids.NewPublicID()
	if err != nil {
		return User{}, err
	}

	res, err := d.ExecWrite(ctx,
		`INSERT INTO users (public_id, username, email, password_hash, role)
		 VALUES (?, ?, ?, ?, ?)`,
		publicID, username, email, passwordHash, role,
	)
	if err != nil {
		return User{}, fmt.Errorf("models: insert user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("models: user last insert id: %w", err)
	}

	return User{
		ID:           id,
		PublicID:     publicID,
		Username:     username,
		Email:        email,
		PasswordHash: passwordHash,
		Role:         role,
	}, nil
}

// GetUserByUsername loads a user by username, returning ErrUserNotFound if none
// exists.
func GetUserByUsername(ctx context.Context, d *db.DB, username string) (User, error) {
	return scanUser(d.QueryRowContext(ctx,
		`SELECT id, public_id, username, email, password_hash, role
		 FROM users WHERE username = ?`, username))
}

// GetUserByID loads a user by internal id, returning ErrUserNotFound if none
// exists.
func GetUserByID(ctx context.Context, d *db.DB, id int64) (User, error) {
	return scanUser(d.QueryRowContext(ctx,
		`SELECT id, public_id, username, email, password_hash, role
		 FROM users WHERE id = ?`, id))
}

// GetUserByPublicID loads a user by public id, returning ErrUserNotFound if none
// exists.
func GetUserByPublicID(ctx context.Context, d *db.DB, publicID string) (User, error) {
	return scanUser(d.QueryRowContext(ctx,
		`SELECT id, public_id, username, email, password_hash, role
		 FROM users WHERE public_id = ?`, publicID))
}

// ListUsers returns all users ordered by username, for admin management.
func ListUsers(ctx context.Context, d *db.DB) ([]User, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, public_id, username, email, password_hash, role
		 FROM users ORDER BY username COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("models: list users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.PublicID, &u.Username, &u.Email, &u.PasswordHash, &u.Role); err != nil {
			return nil, fmt.Errorf("models: scan user: %w", err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: iterate users: %w", err)
	}
	return users, nil
}

// UpdateUser changes a user's email and role (username is immutable) and bumps
// updated_at. The role must be RoleAdmin or RoleUser.
func UpdateUser(ctx context.Context, d *db.DB, id int64, email, role string) error {
	if role != RoleAdmin && role != RoleUser {
		return fmt.Errorf("models: invalid role %q", role)
	}
	_, err := d.ExecWrite(ctx,
		`UPDATE users SET email = ?, role = ?, updated_at = datetime('now') WHERE id = ?`,
		email, role, id)
	if err != nil {
		return fmt.Errorf("models: update user: %w", err)
	}
	return nil
}

// DeleteUser removes a user. It fails if the user still owns address books
// (owner_id is ON DELETE RESTRICT); the caller surfaces that as a usage error.
func DeleteUser(ctx context.Context, d *db.DB, id int64) error {
	if _, err := d.ExecWrite(ctx, `DELETE FROM users WHERE id = ?`, id); err != nil {
		return fmt.Errorf("models: delete user: %w", err)
	}
	return nil
}

// CountAdmins returns the number of admin accounts, for last-admin guards.
func CountAdmins(ctx context.Context, d *db.DB) (int, error) {
	var n int
	if err := d.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE role = ?", RoleAdmin).Scan(&n); err != nil {
		return 0, fmt.Errorf("models: count admins: %w", err)
	}
	return n, nil
}

// OwnsAddressBooks reports whether a user owns any address books (blocks delete).
func OwnsAddressBooks(ctx context.Context, d *db.DB, userID int64) (bool, error) {
	var n int
	if err := d.QueryRowContext(ctx, "SELECT COUNT(*) FROM address_books WHERE owner_id = ?", userID).Scan(&n); err != nil {
		return false, fmt.Errorf("models: count owned books: %w", err)
	}
	return n > 0, nil
}

// CountUsers returns the number of user rows; used by admin bootstrap to refuse
// running on a non-empty database.
func CountUsers(ctx context.Context, d *db.DB) (int, error) {
	var n int
	if err := d.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		return 0, fmt.Errorf("models: count users: %w", err)
	}
	return n, nil
}

// UpdatePasswordHash overwrites a user's password hash (used for lazy rehash on
// login) and bumps updated_at.
func UpdatePasswordHash(ctx context.Context, d *db.DB, id int64, passwordHash string) error {
	_, err := d.ExecWrite(ctx,
		`UPDATE users SET password_hash = ?, updated_at = datetime('now') WHERE id = ?`,
		passwordHash, id)
	if err != nil {
		return fmt.Errorf("models: update password hash: %w", err)
	}
	return nil
}

func scanUser(row *sql.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.PublicID, &u.Username, &u.Email, &u.PasswordHash, &u.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("models: scan user: %w", err)
	}
	return u, nil
}
