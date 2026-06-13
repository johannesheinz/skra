package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
)

func TestAdminUsersRequiresAdmin(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	plain := seedUser(t, d, "plain", "pw", models.RoleUser)
	admin := seedUser(t, d, "admin", "pw", models.RoleAdmin)

	if rec, _, _ := authedGet(t, router, sessionCookieFor(t, d, plain.ID), "/admin/users"); rec.Code != http.StatusNotFound {
		t.Errorf("non-admin /admin/users = %d, want 404", rec.Code)
	}
	if rec, _, _ := authedGet(t, router, sessionCookieFor(t, d, admin.ID), "/admin/users"); rec.Code != http.StatusOK {
		t.Errorf("admin /admin/users = %d, want 200", rec.Code)
	}
	_ = ctx
}

func TestAdminCreateUser(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	admin := seedUser(t, d, "admin", "pw", models.RoleAdmin)
	session := sessionCookieFor(t, d, admin.ID)

	_, token, csrf := authedGet(t, router, session, "/admin/users/new")
	rec := authedPostForm(router, session, csrf, "/admin/users", url.Values{
		auth.CSRFFormField: {token},
		"username":         {"newadmin"},
		"email":            {"na@example.com"},
		"role":             {"admin"},
		"password":         {"longenough1"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create user = %d, want 303", rec.Code)
	}
	list, _, _ := authedGet(t, router, session, "/admin/users")
	if !strings.Contains(list.Body.String(), "newadmin") {
		t.Error("created user not listed")
	}

	// Too-short password is rejected.
	_, t2, c2 := authedGet(t, router, session, "/admin/users/new")
	short := authedPostForm(router, session, c2, "/admin/users", url.Values{
		auth.CSRFFormField: {t2}, "username": {"x"}, "email": {"x@e.com"}, "role": {"user"}, "password": {"short"},
	})
	if short.Code != http.StatusUnprocessableEntity {
		t.Errorf("short password create = %d, want 422", short.Code)
	}
}

func TestAdminGuards(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	admin := seedUser(t, d, "admin", "pw", models.RoleAdmin)
	session := sessionCookieFor(t, d, admin.ID)

	post := func(path string, form url.Values) int {
		_, token, csrf := authedGet(t, router, session, "/admin/users/"+admin.PublicID+"/edit")
		form.Set(auth.CSRFFormField, token)
		return authedPostForm(router, session, csrf, path, form).Code
	}

	// Cannot delete self.
	if code := post("/admin/users/"+admin.PublicID+"/delete", url.Values{}); code != http.StatusUnprocessableEntity {
		t.Errorf("self-delete = %d, want 422", code)
	}
	// Cannot demote the last admin.
	if code := post("/admin/users/"+admin.PublicID+"/edit", url.Values{"email": {"admin@e.com"}, "role": {"user"}}); code != http.StatusUnprocessableEntity {
		t.Errorf("demote last admin = %d, want 422", code)
	}

	// Cannot delete a user who owns books.
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	_, token, csrf := authedGet(t, router, session, "/admin/users/"+owner.PublicID+"/edit")
	del := authedPostForm(router, session, csrf, "/admin/users/"+owner.PublicID+"/delete", url.Values{auth.CSRFFormField: {token}})
	if del.Code != http.StatusUnprocessableEntity {
		t.Errorf("delete book-owner = %d, want 422", del.Code)
	}
}

func TestBookMembershipFlow(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()
	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	alice := seedUser(t, d, "alice", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	ownerSession := sessionCookieFor(t, d, owner.ID)
	membersURL := "/books/" + book.PublicID + "/members"

	// Grant alice viewer by username.
	_, token, csrf := authedGet(t, router, ownerSession, membersURL)
	grant := authedPostForm(router, ownerSession, csrf, membersURL, url.Values{
		auth.CSRFFormField: {token}, "username": {"alice"}, "level": {"viewer"},
	})
	if grant.Code != http.StatusSeeOther {
		t.Fatalf("grant = %d, want 303", grant.Code)
	}
	// Alice can now see the book.
	if rec, _, _ := authedGet(t, router, sessionCookieFor(t, d, alice.ID), "/books/"+book.PublicID); rec.Code != http.StatusOK {
		t.Errorf("alice book access = %d, want 200", rec.Code)
	}
	// Alice (viewer) cannot open the members page (manager required).
	if rec, _, _ := authedGet(t, router, sessionCookieFor(t, d, alice.ID), membersURL); rec.Code != http.StatusForbidden {
		t.Errorf("viewer members page = %d, want 403", rec.Code)
	}

	// Create a brand-new user into the book.
	_, t2, c2 := authedGet(t, router, ownerSession, membersURL)
	create := authedPostForm(router, ownerSession, c2, membersURL+"/new", url.Values{
		auth.CSRFFormField: {t2}, "username": {"newbie"}, "email": {"n@e.com"}, "password": {"longenough1"}, "level": {"manager"},
	})
	if create.Code != http.StatusSeeOther {
		t.Fatalf("create member = %d, want 303", create.Code)
	}
	newbie, err := models.GetUserByUsername(ctx, d, "newbie")
	if err != nil {
		t.Fatalf("new user not created: %v", err)
	}
	if newbie.Role != models.RoleUser {
		t.Errorf("manager-created account role = %q, want user", newbie.Role)
	}
	if level, found, _ := models.GetGrant(ctx, d, newbie.ID, book.ID); !found || level != models.AccessManager {
		t.Errorf("newbie grant = %q,%v, want manager", level, found)
	}

	// Cannot revoke the owner's own membership.
	_, t3, c3 := authedGet(t, router, ownerSession, membersURL)
	rev := authedPostForm(router, ownerSession, c3, membersURL+"/"+owner.PublicID+"/revoke", url.Values{auth.CSRFFormField: {t3}})
	if rev.Code != http.StatusUnprocessableEntity {
		t.Errorf("revoke owner = %d, want 422", rev.Code)
	}
}

func TestAccountPasswordChange(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	user := seedUser(t, d, "alice", "pw", models.RoleUser)
	session := sessionCookieFor(t, d, user.ID)

	// Wrong current password.
	_, token, csrf := authedGet(t, router, session, "/account/password")
	wrong := authedPostForm(router, session, csrf, "/account/password", url.Values{
		auth.CSRFFormField: {token}, "current_password": {"nope"}, "new_password": {"longenough1"},
	})
	if wrong.Code != http.StatusUnauthorized {
		t.Errorf("wrong current password = %d, want 401", wrong.Code)
	}

	// Correct current password, valid new one.
	_, t2, c2 := authedGet(t, router, session, "/account/password")
	ok := authedPostForm(router, session, c2, "/account/password", url.Values{
		auth.CSRFFormField: {t2}, "current_password": {"pw"}, "new_password": {"longenough1"},
	})
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), "Password changed") {
		t.Errorf("password change = %d, body lacks confirmation", ok.Code)
	}
}
