// Package handlers holds the HTTP handlers for Skra's routes.
package handlers

import (
	"net/http"
)

// Health responds 200 with a small JSON body for liveness probes. It performs
// no dependency checks; readiness (a DB check) is a separate, later endpoint.
func Health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
