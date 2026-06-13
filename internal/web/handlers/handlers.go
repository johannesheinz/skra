package handlers

import (
	"log/slog"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/db"
)

// Handlers holds the dependencies shared by Skra's HTTP handlers.
type Handlers struct {
	DB           *db.DB
	Sessions     *auth.SessionStore
	CookieSecure bool
	Logger       *slog.Logger

	// dummyHash is verified against when a username is unknown, so that login
	// timing does not reveal whether an account exists.
	dummyHash string
}

// New builds the handler set. It precomputes a dummy password hash for the
// user-enumeration defense; failing to do so is fatal to construction.
func New(database *db.DB, sessions *auth.SessionStore, cookieSecure bool, logger *slog.Logger) (*Handlers, error) {
	dummy, err := auth.HashPassword("skra-nonexistent-account")
	if err != nil {
		return nil, err
	}
	return &Handlers{
		DB:           database,
		Sessions:     sessions,
		CookieSecure: cookieSecure,
		Logger:       logger,
		dummyHash:    dummy,
	}, nil
}
