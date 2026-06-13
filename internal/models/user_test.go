package models_test

import (
	"context"
	"errors"
	"testing"

	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
)

func TestCreateAndGetUser(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()

	created, err := models.CreateUser(ctx, d, "alice", "alice@example.com", "hash", models.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if created.PublicID == "" {
		t.Error("created user has empty public_id")
	}
	if created.ID == 0 {
		t.Error("created user has zero id")
	}

	byName, err := models.GetUserByUsername(ctx, d, "alice")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if byName != created {
		t.Errorf("GetUserByUsername = %+v, want %+v", byName, created)
	}

	byID, err := models.GetUserByID(ctx, d, created.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if byID != created {
		t.Errorf("GetUserByID = %+v, want %+v", byID, created)
	}
}

func TestCreateUserRejectsInvalidRole(t *testing.T) {
	d := testutil.NewDB(t)
	if _, err := models.CreateUser(context.Background(), d, "bob", "bob@example.com", "hash", "superuser"); err == nil {
		t.Error("expected error for invalid role, got nil")
	}
}

func TestGetUserNotFound(t *testing.T) {
	d := testutil.NewDB(t)
	_, err := models.GetUserByUsername(context.Background(), d, "ghost")
	if !errors.Is(err, models.ErrUserNotFound) {
		t.Errorf("err = %v, want ErrUserNotFound", err)
	}
}

func TestCountUsers(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()

	n, err := models.CountUsers(ctx, d)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 0 {
		t.Errorf("CountUsers on empty db = %d, want 0", n)
	}

	if _, err := models.CreateUser(ctx, d, "alice", "alice@example.com", "hash", models.RoleUser); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	n, err = models.CountUsers(ctx, d)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 1 {
		t.Errorf("CountUsers = %d, want 1", n)
	}
}

func TestUpdatePasswordHash(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()

	u, err := models.CreateUser(ctx, d, "alice", "alice@example.com", "old", models.RoleUser)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := models.UpdatePasswordHash(ctx, d, u.ID, "new"); err != nil {
		t.Fatalf("UpdatePasswordHash: %v", err)
	}
	reloaded, err := models.GetUserByID(ctx, d, u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if reloaded.PasswordHash != "new" {
		t.Errorf("password hash = %q, want %q", reloaded.PasswordHash, "new")
	}
}

func TestDuplicateUsernameRejected(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()

	if _, err := models.CreateUser(ctx, d, "alice", "a@example.com", "hash", models.RoleUser); err != nil {
		t.Fatalf("first CreateUser: %v", err)
	}
	if _, err := models.CreateUser(ctx, d, "alice", "b@example.com", "hash", models.RoleUser); err == nil {
		t.Error("expected UNIQUE violation on duplicate username, got nil")
	}
}
