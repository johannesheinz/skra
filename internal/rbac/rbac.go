// Package rbac centralizes Skra's authorization decision: every access to an
// address book (and the contacts, photos, and exports that inherit its
// permission) routes through Can.
package rbac

import (
	"context"
	"fmt"

	"github.com/johannesheinz/skra/internal/db"
	"github.com/johannesheinz/skra/internal/models"
)

// Action is the kind of access being attempted on an address book.
type Action int

const (
	// Read covers viewing, downloading, and exporting.
	Read Action = iota
	// Write covers create/update/delete of the book and its contents.
	Write
)

// Decision is the outcome of an authorization check. Visible distinguishes
// 404 from 403: a resource the user may not know exists must be reported as
// not-found, while a permitted-to-see-but-not-do action is forbidden.
type Decision struct {
	// Allow is true when the action is permitted.
	Allow bool
	// Visible is true when the user is allowed to know the book exists.
	Visible bool
}

// Evaluate is the pure authorization rule, independent of storage. isAdmin
// short-circuits to full access; otherwise the decision derives from the grant.
func Evaluate(isAdmin bool, level string, hasGrant bool, action Action) Decision {
	if isAdmin {
		return Decision{Allow: true, Visible: true}
	}
	if !hasGrant {
		return Decision{Allow: false, Visible: false}
	}
	switch action {
	case Read:
		return Decision{Allow: level == models.AccessViewer || level == models.AccessManager, Visible: true}
	case Write:
		return Decision{Allow: level == models.AccessManager, Visible: true}
	default:
		return Decision{Allow: false, Visible: true}
	}
}

// Can resolves the grant for user on addressBookID and evaluates action. Admins
// bypass the grant lookup entirely (implicit manager on every book).
func Can(ctx context.Context, d *db.DB, user models.User, addressBookID int64, action Action) (Decision, error) {
	if user.Role == models.RoleAdmin {
		return Evaluate(true, "", false, action), nil
	}
	level, found, err := models.GetGrant(ctx, d, user.ID, addressBookID)
	if err != nil {
		return Decision{}, fmt.Errorf("rbac: resolve grant: %w", err)
	}
	return Evaluate(false, level, found, action), nil
}
