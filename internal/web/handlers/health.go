// Package handlers holds the HTTP handlers for Skra's routes.
package handlers

import (
	"net/http"
)

// Health responds 200 with a small JSON body for liveness probes. It performs no dependency checks; readiness (a DB check) is the separate Readyz endpoint.
func Health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// Readyz reports readiness with a cheap database check, returning 503 when the database is unreachable so a load balancer can route away.
func (h *Handlers) Readyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var one int
	if err := h.DB.QueryRowContext(r.Context(), "SELECT 1").Scan(&one); err != nil {
		h.Logger.Error("readiness check failed", "err", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"unavailable"}`))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ready"}`))
}
