# Skrá — Claude Code Project Guide

Self-hosted contacts app: one static Go binary + one SQLite file, server-rendered `html/template` + htmx. This is **Go 1.26** — the Java rules in the global config do not apply.

Depth lives in `docs/` — read those, this file is the quick map:
`architecture.md` (data & security model), `features.md` (what it does), `development.md` (conventions, i18n, releases), `operations.md` (running it).

## Commands
```sh
CGO_ENABLED=0 go build -trimpath -o skra .   # static binary
go test ./...                                # CI runs -race with coverage
go vet ./... && gofmt -l .                   # must be clean before commit
# lint (not on PATH by default — install once, then run); see .golangci.yml:
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest && golangci-lint run ./...
./scripts/dev.sh                             # build + seed demo DB + serve (admin / demo-password-123)
```

## Hard constraints (do not regress)
- **CGO-free.** Only `modernc.org/sqlite` (pure Go). Never add a CGO driver — it breaks the static binary.
- **SQLite is the only datastore**; photos are BLOBs in it. No external services.
- **Config is env-only (`SKRA_*`), no defaults** — a missing/empty value is a startup error. Never invent a fallback.
- **Only `public_id` is exposed externally**; resolve → id → `can()` on every route. Return 404, not 403, for resources a user may not know exist.
- **Never log PII or share tokens; never put PII in URLs.**
- **Self-only CSP:** no inline `<script>`/`<style>`. Assets are local-first (embedded) — no CDNs, no npm/Node build step.
- **Localized by default:** no hardcoded user-facing strings — every UI string (incl. `aria-label`/`title`/`alt`) goes through `internal/i18n`; add new keys to every catalog or the build fails.

## Conventions
- Speaking names over comments; comment only intent/gotchas.
- Break prose and comments at meaning (sentence/clause), not a fixed column — but avoid >~250-char lines. Don't cite ephemeral plan artifacts (phases, spec §-numbers) in code or durable docs.
- Version: `const Version` in `main.go`; patch bumps untagged, minor bumps tagged `vX.Y.Z`.
- Commit only when asked; branch off `main` for features.
