package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/johannesheinz/skra/internal/db"
	"github.com/johannesheinz/skra/internal/models"
)

type contextKey int

const userContextKey contextKey = iota

// Authenticator provides the session-loading and access-gating middleware.
type Authenticator struct {
	Sessions *SessionStore
	DB       *db.DB
	Logger   *slog.Logger
	// LoginPath is where unauthenticated browsers are redirected.
	LoginPath string
}

// LoadUser resolves the session cookie to a user and stores it in the request context.
// A missing or invalid session simply yields an anonymous request; it is not an error here (route guards decide what to require).
func (a *Authenticator) LoadUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(SessionCookieName)
		if err != nil || cookie.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		userID, err := a.Sessions.UserID(r.Context(), cookie.Value)
		if err != nil {
			if !errors.Is(err, ErrSessionNotFound) {
				a.Logger.Error("session lookup failed", "err", err)
			}
			next.ServeHTTP(w, r)
			return
		}
		user, err := models.GetUserByID(r.Context(), a.DB, userID)
		if err != nil {
			// Session points at a missing user; treat as anonymous.
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), user)))
	})
}

// RequireAuth rejects requests without an authenticated user by redirecting to the login page.
func (a *Authenticator) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFromContext(r.Context()); !ok {
			http.Redirect(w, r, a.LoginPath, http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdmin requires an authenticated admin.
// Non-admins (including anonymous requests) get 404 rather than 403, so the admin surface is not revealed.
func (a *Authenticator) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFromContext(r.Context())
		if !ok || user.Role != models.RoleAdmin {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withUser(ctx context.Context, user models.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// UserFromContext returns the authenticated user, if any.
func UserFromContext(ctx context.Context) (models.User, bool) {
	user, ok := ctx.Value(userContextKey).(models.User)
	return user, ok
}
