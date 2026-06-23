// Package web wires the HTTP router and server for Skra.
package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/config"
	"github.com/johannesheinz/skra/internal/db"
	"github.com/johannesheinz/skra/internal/web/handlers"
	"github.com/johannesheinz/skra/internal/web/static"
)

// Server owns the HTTP server and its lifecycle.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
}

// New builds a Server from configuration, the open database, and a logger.
func New(cfg config.Config, database *db.DB, logger *slog.Logger) (*Server, error) {
	router, err := buildRouter(cfg, database, logger)
	if err != nil {
		return nil, err
	}
	return &Server{
		httpServer: &http.Server{
			Addr:              cfg.Listen,
			Handler:           router,
			ReadHeaderTimeout: 10 * time.Second,
		},
		logger: logger,
	}, nil
}

func buildRouter(cfg config.Config, database *db.DB, logger *slog.Logger) (http.Handler, error) {
	sessions := auth.NewSessionStore(database)
	authenticator := &auth.Authenticator{
		Sessions:  sessions,
		DB:        database,
		Logger:    logger,
		LoginPath: "/login",
	}
	h, err := handlers.New(database, sessions, cfg.CookieSecure, cfg.ExternalURL, cfg.SessionKey, logger)
	if err != nil {
		return nil, err
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders)
	r.Use(authenticator.LoadUser)

	r.Get("/healthz", handlers.Health)
	r.Get("/readyz", h.Readyz)
	r.Handle("/static/*", static.Handler())
	r.Get("/login", h.LoginForm)
	r.Post("/login", h.Login)
	r.Post("/logout", h.Logout)

	r.Group(func(r chi.Router) {
		r.Use(authenticator.RequireAuth)
		r.Get("/", h.Home)

		r.Get("/account", h.AccountPage)
		r.Post("/account/profile", h.AccountProfileUpdate)
		r.Post("/account/appearance", h.AccountAppearanceUpdate)
		r.Post("/account/theme", h.AccountThemeToggle)
		r.Post("/account/list-prefs", h.AccountListPrefsUpdate)
		r.Post("/account/password", h.AccountPasswordUpdate)
		r.Get("/account/password", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/account", http.StatusMovedPermanently)
		})

		r.Get("/ui/rows/{kind}", h.ContactRowFragment)

		r.Get("/books", h.BooksList)
		r.Get("/books/new", h.BookNew)
		r.Post("/books", h.BookCreate)
		r.Get("/books/{publicID}", h.BookShow)
		r.Get("/books/{publicID}/edit", h.BookEdit)
		r.Post("/books/{publicID}/edit", h.BookUpdate)
		r.Post("/books/{publicID}/delete", h.BookDelete)
		r.Get("/books/{publicID}/contacts/new", h.ContactNew)
		r.Post("/books/{publicID}/contacts", h.ContactCreate)
		r.Get("/books/{publicID}/export.vcf", h.BookExportVCard)
		r.Get("/books/{publicID}/export.csv", h.BookExportCSV)
		r.Get("/books/{publicID}/shares", h.BookShares)
		r.Post("/books/{publicID}/shares", h.BookShareCreate)
		r.Post("/books/{publicID}/shares/{shareID}/revoke", h.BookShareRevoke)
		r.Get("/books/{publicID}/members", h.BookMembers)
		r.Post("/books/{publicID}/members", h.BookMemberAdd)
		r.Post("/books/{publicID}/members/new", h.BookMemberCreate)
		r.Post("/books/{publicID}/members/{userPublicID}/revoke", h.BookMemberRevoke)
		r.Get("/books/{publicID}/import", h.ImportForm)
		r.Post("/books/{publicID}/import", h.ImportUpload)
		r.Post("/books/{publicID}/import/commit", h.ImportCommit)

		r.Get("/contacts/{publicID}", h.ContactShow)
		r.Get("/contacts/{publicID}/edit", h.ContactEdit)
		r.Post("/contacts/{publicID}/edit", h.ContactUpdate)
		r.Post("/contacts/{publicID}/delete", h.ContactDelete)
		r.Get("/contacts/{publicID}/photo", h.ContactPhoto)
		r.Post("/contacts/{publicID}/photo", h.ContactPhotoUpload)
		r.Post("/contacts/{publicID}/photo/delete", h.ContactPhotoDelete)
		r.Get("/contacts/{publicID}/export.vcf", h.ContactExportVCard)
		r.Get("/contacts/{publicID}/shares", h.ContactShares)
		r.Post("/contacts/{publicID}/shares", h.ContactShareCreate)
		r.Post("/contacts/{publicID}/shares/{shareID}/revoke", h.ContactShareRevoke)
	})

	// Admin-only user management. RequireAdmin returns 404 for non-admins
	// (including anonymous), so the surface is not revealed.
	r.Group(func(r chi.Router) {
		r.Use(authenticator.RequireAdmin)
		r.Get("/admin/users", h.AdminUsersList)
		r.Get("/admin/users/new", h.AdminUserNew)
		r.Post("/admin/users", h.AdminUserCreate)
		r.Get("/admin/users/{publicID}/edit", h.AdminUserEdit)
		r.Post("/admin/users/{publicID}/edit", h.AdminUserUpdate)
		r.Post("/admin/users/{publicID}/password", h.AdminUserPassword)
		r.Post("/admin/users/{publicID}/delete", h.AdminUserDelete)
	})

	// Public share routes — outside RequireAuth (LoadUser still applies so the
	// authenticated mode can see a session). Logged with the route pattern,
	// never the raw token.
	r.Get("/s/{token}", h.ShareEntry)
	r.Post("/s/{token}/gate", h.ShareGateSubmit)
	r.Get("/s/{token}/c/{contactPublicID}", h.ShareContactInBook)
	r.Get("/s/{token}/c/{contactPublicID}/photo", h.ShareBookContactPhoto)
	r.Get("/s/{token}/photo", h.ShareContactPhoto)
	r.Get("/s/{token}/export.vcf", h.ShareExportVCard)
	r.Get("/s/{token}/export.csv", h.ShareExportCSV)

	return r, nil
}

// securityHeaders applies a restrictive, self-only Content-Security-Policy plus
// related hardening headers. All assets are self-hosted, so no third-party
// origins are permitted; img-src allows data: for inline placeholders.
func securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; img-src 'self' data:; object-src 'none'; " +
		"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// Run starts the server and blocks until ctx is cancelled, then shuts down
// gracefully within a bounded timeout.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("http server listening", "addr", s.httpServer.Addr)
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		s.logger.Info("shutting down http server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.httpServer.Shutdown(shutdownCtx)
	}
}
