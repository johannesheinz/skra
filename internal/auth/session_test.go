package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/db"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
)

func newUser(t *testing.T, d *db.DB) models.User {
	t.Helper()
	u, err := models.CreateUser(context.Background(), d, "alice", "alice@example.com", "h", models.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func TestSessionLifecycle(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	store := auth.NewSessionStore(d)
	user := newUser(t, d)

	id, err := store.Create(ctx, user.ID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == "" {
		t.Fatal("empty session id")
	}

	got, err := store.UserID(ctx, id)
	if err != nil {
		t.Fatalf("UserID: %v", err)
	}
	if got != user.ID {
		t.Errorf("UserID = %d, want %d", got, user.ID)
	}

	if err := store.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.UserID(ctx, id); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Errorf("after delete UserID err = %v, want ErrSessionNotFound", err)
	}
}

func TestExpiredSessionRejected(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	store := auth.NewSessionStore(d)
	user := newUser(t, d)

	// Insert a session that expired an hour ago.
	if _, err := d.ExecWrite(ctx,
		`INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, datetime('now','-1 hour'))`,
		"expired-session", user.ID); err != nil {
		t.Fatalf("seed expired session: %v", err)
	}

	if _, err := store.UserID(ctx, "expired-session"); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Errorf("expired session UserID err = %v, want ErrSessionNotFound", err)
	}

	if err := store.DeleteExpired(ctx); err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	var count int
	if err := d.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("sessions remaining after DeleteExpired = %d, want 0", count)
	}
}

func TestUnknownSessionRejected(t *testing.T) {
	d := testutil.NewDB(t)
	store := auth.NewSessionStore(d)
	if _, err := store.UserID(context.Background(), "nope"); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Errorf("unknown session err = %v, want ErrSessionNotFound", err)
	}
}
