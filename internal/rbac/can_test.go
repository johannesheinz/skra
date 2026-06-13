package rbac_test

import (
	"context"
	"testing"

	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/rbac"
	"github.com/johannesheinz/skra/internal/testutil"
)

func TestCanResolvesGrantsAndAdminBypass(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()

	admin, err := models.CreateUser(ctx, d, "admin", "admin@example.com", "h", models.RoleAdmin)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	viewer, err := models.CreateUser(ctx, d, "viewer", "viewer@example.com", "h", models.RoleUser)
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	stranger, err := models.CreateUser(ctx, d, "stranger", "stranger@example.com", "h", models.RoleUser)
	if err != nil {
		t.Fatalf("create stranger: %v", err)
	}

	res, err := d.ExecWrite(ctx,
		`INSERT INTO address_books (public_id, name, owner_id) VALUES ('b', 'Book', ?)`, admin.ID)
	if err != nil {
		t.Fatalf("seed book: %v", err)
	}
	bookID, _ := res.LastInsertId()

	if _, err := d.ExecWrite(ctx,
		`INSERT INTO address_book_members (address_book_id, user_id, access_level) VALUES (?, ?, ?)`,
		bookID, viewer.ID, models.AccessViewer); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	// Admin: full access without any grant row.
	if dec, _ := rbac.Can(ctx, d, admin, bookID, rbac.Write); !dec.Allow {
		t.Error("admin should be allowed to write")
	}
	// Viewer: read yes, write no, visible.
	if dec, _ := rbac.Can(ctx, d, viewer, bookID, rbac.Read); !dec.Allow || !dec.Visible {
		t.Errorf("viewer read = %+v, want allowed+visible", dec)
	}
	if dec, _ := rbac.Can(ctx, d, viewer, bookID, rbac.Write); dec.Allow {
		t.Error("viewer should not be allowed to write")
	}
	// Stranger: not visible at all.
	if dec, _ := rbac.Can(ctx, d, stranger, bookID, rbac.Read); dec.Allow || dec.Visible {
		t.Errorf("stranger = %+v, want denied+invisible", dec)
	}
}
