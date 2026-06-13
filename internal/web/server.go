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
	h, err := handlers.New(database, sessions, cfg.CookieSecure, logger)
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
	r.Handle("/static/*", static.Handler())
	r.Get("/login", h.LoginForm)
	r.Post("/login", h.Login)
	r.Post("/logout", h.Logout)

	r.Group(func(r chi.Router) {
		r.Use(authenticator.RequireAuth)
		r.Get("/", h.Home)

		r.Get("/books", h.BooksList)
		r.Get("/books/new", h.BookNew)
		r.Post("/books", h.BookCreate)
		r.Get("/books/{publicID}", h.BookShow)
		r.Get("/books/{publicID}/edit", h.BookEdit)
		r.Post("/books/{publicID}/edit", h.BookUpdate)
		r.Post("/books/{publicID}/delete", h.BookDelete)
		r.Get("/books/{publicID}/contacts/new", h.ContactNew)
		r.Post("/books/{publicID}/contacts", h.ContactCreate)

		r.Get("/contacts/{publicID}", h.ContactShow)
		r.Get("/contacts/{publicID}/edit", h.ContactEdit)
		r.Post("/contacts/{publicID}/edit", h.ContactUpdate)
		r.Post("/contacts/{publicID}/delete", h.ContactDelete)
		r.Get("/contacts/{publicID}/photo", h.ContactPhoto)
	})

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
