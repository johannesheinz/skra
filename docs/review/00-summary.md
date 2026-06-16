# Skrá Repository Review — Executive Summary

Five parallel reviews covered best practices/maintainability, performance/operations, QA/test coverage, security, and architecture/data model. Each has its own report in this directory:

- [01-best-practices.md](01-best-practices.md)
- [02-performance-ops.md](02-performance-ops.md)
- [03-qa-testing.md](03-qa-testing.md)
- [04-security.md](04-security.md)
- [05-architecture.md](05-architecture.md)

## Overall verdict

This is a well-engineered codebase. Layering is clean (no SQL outside `internal/models`, every route funneled through `rbac.Can`), authentication is best-practice (argon2id, CSPRNG opaque IDs, parameterized SQL, autoescaped templates, restrictive CSP, CSRF on every state-changing route), and it is closely faithful to its own baseline spec. No confirmed Critical, remotely-exploitable security vulnerability was found.

The real risks are operational and DoS-adjacent — the app can be starved of memory or accumulate unbounded state — plus a CI signal that is quietly misreporting test coverage. These are the things that will bite in production, not in review.

## Most critical findings (fix first)

Ranked by real-world impact. Severity in parentheses; bracketed tags show which reviews independently flagged it.

### 1. Image decode has no pixel/dimension cap — memory-exhaustion DoS (High) [security M3, perf, qa]
`internal/images/process.go:28` decodes attacker-supplied bytes fully into memory and allocates full-resolution RGBA buffers before downscaling. The 10 MiB body cap does not bound decoded pixels — a small, highly-compressible image declaring huge dimensions can allocate gigabytes and OOM the process. Reachable by any authenticated book manager, directly or via an embedded vCard import photo. **The rotation/flip code paths that would amplify this are also at 0% test coverage.**
Fix: `image.DecodeConfig` first, reject `Width*Height` over ~40 MP before decoding.

### 2. No automated garbage collection — unbounded state growth (High) [perf, architecture]
Three leaks with no scheduler behind them:
- `SessionStore.DeleteExpired` exists but is called only from tests — the `sessions` table grows for the life of the deployment (`internal/auth/session.go:74`, `main.go:57`).
- `import_uploads` BLOBs (up to 10 MiB) are purged only opportunistically on the *next* upload (`internal/web/handlers/import.go:45`).
- `auto_vacuum=INCREMENTAL` is set but `PRAGMA incremental_vacuum` is never run, so the file never shrinks after deletes (`internal/db/db.go:160`).
The baseline spec calls disk space the #1 operational risk. Fix: a single background maintenance ticker in `serve()` running session purge, stale-upload purge, and incremental vacuum.

### 3. Export path: N+1 photo loads + full-response buffering (High) [perf, best-practices]
Book/contact exports issue one `GetPhotoBytes` per photographed contact and assemble the entire response in a `bytes.Buffer` before writing (`internal/web/handlers/export.go:37`, `:47`; `public.go:190`). With base64 inflation, a large book holds every photo BLOB plus the whole inflated output in RAM at once — the single largest memory-spike vector. The `export.WriteVCards`/`WriteCSV` functions already take `io.Writer`; the handlers just need to stream to `w` and fetch photos lazily. Bonus: single-contact export currently loads the *entire book* and linear-scans for one row (`export.go:94`).

### 4. CI coverage number is misleading; no linter or vuln scan (High) [qa, perf]
CI reports handler coverage as **0.3%**, a measurement artifact — handlers are exercised end-to-end from the `internal/web` package, and real coverage measured with `-coverpkg` is **55.6%**. CI computes coverage, then neither reports, thresholds, nor `-coverpkg`-corrects it, so the headline number is actively false. There is also no `staticcheck`/`golangci-lint`/`errcheck` (which would enforce the project's own "no ignored errors / no defaults" rule) and no `govulncheck` (relevant given untrusted vCard + image parsing). Fix: add `-coverpkg=./internal/...`, `golangci-lint`, and `govulncheck` to `.github/workflows/ci.yml`.

### 5. Untested privileged and public-facing paths (High) [qa]
At 0% real coverage: contact-scoped sharing end to end (`ContactShares`/`ContactShareCreate`/`ContactShareRevoke`/`shareContact`/`ShareContactPhoto`/`ShareExportCSV`), plus the destructive/privileged `BookDelete` and `AdminUserPassword`. Contact shares are the app's public attack surface; only book shares are currently tested.

### 6. Incomplete HTTP server timeouts + no global body limit (High) [perf, security]
Only `ReadHeaderTimeout` is set (`internal/web/server.go:34`). Missing `ReadTimeout`/`WriteTimeout`/`IdleTimeout` leaves slowloris/slow-body/stuck-reader exposure, and `MaxBytesReader` is applied only on the two upload handlers, so other POST routes have no body ceiling.

### 7. No connection-pool limits on SQLite (High) [perf]
`sql.Open` is never followed by `SetMaxOpenConns`/`SetMaxIdleConns` (`internal/db/db.go:56`). With WAL plus the app-level write mutex, an unbounded reader pool can starve writers and surface `busy_timeout` errors under load, with no correctness upside.

### 8. Silent default in `ImportCommit` violates project principle (High) [best-practices]
`internal/web/handlers/import.go:121` coerces an unrecognized `action` to `skip` rather than rejecting it — a direct violation of the "never fall back to a guessed default" rule in `docs/01_skra-development-principles.md` and `CLAUDE.md`, and it silently changes which contacts get inserted.

### 9. `share_links.target_id` is an unconstrained polymorphic FK (High) [architecture]
It references either a book or a contact by internal id with no foreign key and no cascade (`internal/db/migrations/0001_init.sql:71`), so deleting a book/contact orphans its share links. Masked by a read-time 404, but integrity is unenforced and rows accumulate. Fix: cascade cleanup on delete, or split into `book_share_links` / `contact_share_links` with real FKs (also helps the future M:N story).

## Secondary findings worth scheduling

- **No login rate limiting / lockout** (security M1) — online brute force possible; also a CPU-DoS via the expensive argon2 hash. `internal/web/handlers/auth.go:24`.
- **Bootstrap admin password has no strength check** (security M2) — `create-admin` accepts any length while every in-app path enforces 8 chars. `main.go:115`.
- **Anemic domain models** (architecture) — roles, access levels, share modes are bare strings validated procedurally; `User{Role:"root"}` is representable. Consider defined types with validating constructors.
- **No repository/service seam** (architecture) — `models` is free functions over a concrete `*db.DB`; handlers can't be tested against fakes and transactions can't span models. This is the main friction against the roadmap's M:N multi-book change.
- **Timestamps stored/compared as hand-formatted text** (architecture) — `ShareLink.Usable` parses a hardcoded layout; a row with different precision silently bricks the link. `internal/models/share_link.go:155`.
- **Duplicated helpers/constants** (best-practices) — two `nullString`/`nullableString` functions with divergent trim semantics; two identical time-format constants across `auth` and `models` that would silently break expiry if they desync.
- **Swallowed housekeeping errors** (best-practices) — `DeleteStaleImportUploads`/`DeleteImportUpload` drop errors with bare `_ =`, inconsistent with logging done elsewhere.
- **Missing index on `share_links(scope, target_id)`** (perf, architecture) — full scan on the shares-management page; negligible today.
- **No container HEALTHCHECK, no `/metrics`, fixed log level, no access log** (perf) — operational observability gaps.
- **Ignored setup errors in tests** (`x, _ := ...`) and underused table-driven style (qa) — a failing setup call fails confusingly downstream.

## Confirmed strengths (do not regress these)

- argon2id (64 MiB, t=3), constant-time compare, transparent rehash, no user enumeration.
- 128/256/160-bit CSPRNG identifiers; internal integer ids never leave the DB layer — no IDOR found.
- Fully parameterized SQL; `html/template` autoescaping with no `template.HTML` bypasses; restrictive CSP; CSV formula-injection defused.
- CSRF double-submit enforced on every state-changing route, including multipart uploads (correct parse-then-verify ordering).
- Clean layering and spec fidelity; robust migration runner with per-migration transactions and pre-migration `VACUUM INTO` snapshots.
- Strong static-asset serving (embedded FS, SHA-256 ETags, immutable versioned URLs, conditional GET).
- Distroless non-root static Docker image; strict fail-closed config validation; graceful shutdown; `-race` in CI.
- Test quality where it exists is high: real router + SQLite + CSRF/session flows, genuine leak/lockout/RBAC assertions, no skipped or assertion-free tests, clean per-test isolation.

## Suggested order of work

1. Ship the safety fixes: image pixel cap (#1), maintenance ticker (#2), server timeouts + global body limit (#6). Small, high-leverage, prevent outages.
2. Fix the CI signal (#4) so coverage and lint/vuln gaps stop hiding — this makes everything after it measurable.
3. Stream exports and drop the N+1 (#3); add the connection-pool cap (#7).
4. Close the test gaps on privileged/public paths (#5) and the `ImportCommit` silent default (#8).
5. Address the schema integrity gap (#9) and the secondary hardening items as a follow-up pass.
