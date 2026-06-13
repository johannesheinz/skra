package models_test

import (
	"context"
	"testing"

	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
)

func TestListUsersAndCountAdmins(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	models.CreateUser(ctx, d, "admin", "a@e.com", "h", models.RoleAdmin)
	models.CreateUser(ctx, d, "bob", "b@e.com", "h", models.RoleUser)
	models.CreateUser(ctx, d, "alice", "al@e.com", "h", models.RoleUser)

	users, err := models.ListUsers(ctx, d)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 3 || users[0].Username != "admin" { // alphabetical: admin, alice, bob
		t.Errorf("ListUsers = %d users, first %q", len(users), users[0].Username)
	}

	n, err := models.CountAdmins(ctx, d)
	if err != nil {
		t.Fatalf("CountAdmins: %v", err)
	}
	if n != 1 {
		t.Errorf("CountAdmins = %d, want 1", n)
	}
}

func TestUpdateUser(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	u, _ := models.CreateUser(ctx, d, "bob", "b@e.com", "h", models.RoleUser)

	if err := models.UpdateUser(ctx, d, u.ID, "new@e.com", models.RoleAdmin); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	got, _ := models.GetUserByPublicID(ctx, d, u.PublicID)
	if got.Email != "new@e.com" || got.Role != models.RoleAdmin {
		t.Errorf("after update = %+v", got)
	}
	if err := models.UpdateUser(ctx, d, u.ID, "x@e.com", "wizard"); err == nil {
		t.Error("expected error for invalid role")
	}
}

func TestDeleteUserAndOwnsBooks(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	owner, _ := models.CreateUser(ctx, d, "owner", "o@e.com", "h", models.RoleUser)
	plain, _ := models.CreateUser(ctx, d, "plain", "p@e.com", "h", models.RoleUser)
	models.CreateAddressBook(ctx, d, owner.ID, "Book", "")

	owns, err := models.OwnsAddressBooks(ctx, d, owner.ID)
	if err != nil || !owns {
		t.Errorf("OwnsAddressBooks(owner) = %v, %v; want true", owns, err)
	}
	owns, _ = models.OwnsAddressBooks(ctx, d, plain.ID)
	if owns {
		t.Error("plain user should not own books")
	}

	// Deleting an owner is blocked by the FK RESTRICT.
	if err := models.DeleteUser(ctx, d, owner.ID); err == nil {
		t.Error("expected delete of book-owning user to fail")
	}
	// Deleting a non-owner succeeds.
	if err := models.DeleteUser(ctx, d, plain.ID); err != nil {
		t.Errorf("delete plain user: %v", err)
	}
}

func TestMembershipLifecycle(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	owner, _ := models.CreateUser(ctx, d, "owner", "o@e.com", "h", models.RoleUser)
	alice, _ := models.CreateUser(ctx, d, "alice", "a@e.com", "h", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")

	// Owner already has a manager grant from book creation; add alice as viewer.
	if err := models.AddOrUpdateMember(ctx, d, book.ID, alice.ID, models.AccessViewer, owner.ID); err != nil {
		t.Fatalf("AddOrUpdateMember: %v", err)
	}
	members, err := models.ListMembers(ctx, d, book.ID)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("members = %d, want 2 (owner + alice)", len(members))
	}

	// Upgrade alice to manager.
	if err := models.AddOrUpdateMember(ctx, d, book.ID, alice.ID, models.AccessManager, owner.ID); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	level, found, _ := models.GetGrant(ctx, d, alice.ID, book.ID)
	if !found || level != models.AccessManager {
		t.Errorf("alice grant = %q, %v; want manager", level, found)
	}

	// Invalid level rejected.
	if err := models.AddOrUpdateMember(ctx, d, book.ID, alice.ID, "owner", owner.ID); err == nil {
		t.Error("expected invalid access level error")
	}

	// Remove alice.
	if err := models.RemoveMember(ctx, d, book.ID, alice.ID); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if _, found, _ := models.GetGrant(ctx, d, alice.ID, book.ID); found {
		t.Error("alice grant should be gone")
	}
}
