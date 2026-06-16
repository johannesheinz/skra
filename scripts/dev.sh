#!/usr/bin/env bash
# Local development helper: build the binary, reset and seed a demo database,
# print the demo credentials, and run the server with dev-only configuration.
#
# Usage:  ./scripts/dev.sh
# Override anything by exporting it first, e.g.  SKRA_LISTEN=127.0.0.1:4000 ./scripts/dev.sh
#
# NOT for production — the session key is a well-known dev value and cookies are
# not Secure. Each run resets the demo database.
set -euo pipefail
cd "$(dirname "$0")/.."

# --- dev configuration (override by exporting before running) ---
export SKRA_LISTEN="${SKRA_LISTEN:-127.0.0.1:3000}"
export SKRA_DB_PATH="${SKRA_DB_PATH:-./skra-dev.db}"
export SKRA_COOKIE_SECURE="${SKRA_COOKIE_SECURE:-false}"
export SKRA_EXTERNAL_URL="${SKRA_EXTERNAL_URL:-http://${SKRA_LISTEN}}"
export SKRA_SESSION_KEY="${SKRA_SESSION_KEY:-dev-only-session-key-not-secret-0000}"
DEMO_CONTACTS="${SKRA_DEMO_CONTACTS:-150}"
DEMO_PASSWORD="demo-password-123"

echo "==> building (CGO-free static binary)"
CGO_ENABLED=0 go build -o skra .

echo "==> resetting demo database ($SKRA_DB_PATH)"
rm -f "$SKRA_DB_PATH" "$SKRA_DB_PATH-wal" "$SKRA_DB_PATH-shm"

echo "==> seeding demo data ($DEMO_CONTACTS contacts)"
go run ./scripts/seed --db "$SKRA_DB_PATH" --extra "$DEMO_CONTACTS" >/dev/null

cat <<BANNER

  Skrá — local dev server
  ------------------------------------------------
  URL:       $SKRA_EXTERNAL_URL
  Users:     admin, alice, bob, carol, dave
  Password:  $DEMO_PASSWORD
  Data:      $DEMO_CONTACTS contacts across 4 books (reset on each run)

  Ctrl-C to stop.
  ------------------------------------------------

BANNER

echo "==> starting server"
exec ./skra serve
