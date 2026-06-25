# Skrá Remediation Plan — Critical Findings

Actionable, self-contained work items for the High-severity findings in [00-summary.md](00-summary.md). Each item is independent and carries the context, exact locations, fix steps, and verification needed to start without re-reading the other reports. Ordered by recommended sequence (safety fixes first).

Project constraints (from `CLAUDE.md` / `docs/01_skra-development-principles.md`): Go 1.26, never use silent defaults (fail fast with an error), prefer speaking names over comments, keep security/performance in mind.

Verify the whole plan with `go build ./... && go test ./...` after each item.

---

## 1. Image decode: pixel/dimension cap (memory-exhaustion DoS)

- **Problem:** `Process` fully decodes attacker-supplied bytes and allocates full-resolution RGBA buffers before downscaling. The 10 MiB body cap does not bound decoded pixel count; a small, highly compressible image with huge declared dimensions can allocate gigabytes and OOM the process. Reachable by any authenticated book manager directly (photo upload) or via an embedded vCard-import photo.
- **Location:** `internal/images/process.go:28` (`Process`), decode at `:29`, RGBA allocations at `:66,98,110,122,135,148`.
- **Fix:**
  1. Add a max-pixel constant (e.g. `const maxDecodedPixels = 40_000_000`).
  2. Before `image.Decode`, call `image.DecodeConfig(bytes.NewReader(data))`; if `cfg.Width*cfg.Height > maxDecodedPixels` (guard against int overflow) or either dimension exceeds a sane bound (e.g. 20000), return a descriptive error — do not decode.
  3. Ensure the decoders already imported for `Decode` are also registered for `DecodeConfig` (same `image/jpeg`, `image/png` side-effect imports cover both).
- **Verify:** unit test in `internal/images/process_test.go` feeding a crafted oversized-dimension PNG header asserts `Process` returns an error and allocates nothing large. Confirm the photo-upload handler (`handlers/contacts.go`) and import photo path (`handlers/import.go`) surface the error as a 4xx, not a 500.

---

## 2. Background maintenance ticker (unbounded state growth)

- **Problem:** three stores grow without bound because nothing prunes them on a schedule:
  - `SessionStore.DeleteExpired` exists but is only called from tests → `sessions` grows for the life of the deployment.
  - `import_uploads` BLOBs (up to 10 MiB) are purged only opportunistically at the start of the *next* upload.
  - `auto_vacuum=INCREMENTAL` is set but `PRAGMA incremental_vacuum` is never executed → the file never shrinks after deletes.
  The baseline spec (§13) names disk space the #1 operational signal.
- **Locations:** `internal/auth/session.go:74` (`DeleteExpired`), `internal/models/import.go:65` (`DeleteStaleImportUploads`), `internal/db/db.go:160` (`auto_vacuum` set, never run), `main.go:57` (`serve`), `internal/web/server.go:44` (session store built), `internal/web/server.go:163` (`Run`).
- **Fix:**
  1. Add `internal/db` method `func (d *DB) IncrementalVacuum(ctx context.Context) error` running `PRAGMA incremental_vacuum;` via `ExecWrite`.
  2. Give `Server` access to `database` and `sessions` (add fields, populated in `New`/`buildRouter`).
  3. In `Server.Run`, before/alongside serving, start one goroutine with a `time.NewTicker` (hourly is fine) bound to the `ctx` passed to `Run`; on each tick run `sessions.DeleteExpired`, `models.DeleteStaleImportUploads`, and `db.IncrementalVacuum`. Log any error (do not swallow). Stop on `ctx.Done()`.
  4. Run once immediately on startup so a long-idle deploy is cleaned at boot.
- **Verify:** existing session/import tests still pass; add a test that inserts an expired session + stale upload, invokes the maintenance function directly, and asserts both are gone. Confirm graceful shutdown still returns promptly (ticker goroutine exits on ctx cancel).

---

## 3. Stream exports; drop N+1 photo loads and whole-book scan

- **Problem:** book/contact vCard and CSV exports build the entire response in a `bytes.Buffer` before writing, and load one photo BLOB per contact into memory. With base64 inflation (~33%) a large book holds every photo plus the full inflated output at once — the largest memory-spike vector. Separately, single-contact export loads the *entire book* and linear-scans for one row.
- **Locations:** `internal/web/handlers/export.go:47,73,118` (`bytes.Buffer`), `:37,105` (`GetPhotoBytes` per contact), `:27,62,94` (`ListContactsForExport` for the whole book), `internal/web/handlers/public.go:190` (share export buffering). The writers `export.WriteVCards`/`WriteCSV` already accept `io.Writer` and flush incrementally.
- **Fix:**
  1. Set response headers first, then pass `w` (the `http.ResponseWriter`) directly to `export.WriteVCards`/`WriteCSV` instead of buffering. Note: once bytes are written you can no longer switch to an error status — validate/authorize fully before the first write.
  2. Fetch each contact's photo lazily inside the per-contact loop (already the shape) rather than pre-collecting; consider a `contacts LEFT JOIN contact_photos` cursor if a single query is preferred.
  3. Add `models.GetContactExport(ctx, d, id)` (single-row `SELECT ... WHERE id = ?`) and use it in `writeContactVCard` instead of `ListContactsForExport` + linear scan (`export.go:94-101`).
- **Verify:** export tests (`internal/web/export_test.go`) still produce byte-identical output; add a test asserting single-contact export issues one contact query (not a book-wide load). Manual: export a book with several photos and confirm correct file.

---

## 4. Fix CI coverage signal; add linter and vuln scan

- **Problem:** CI reports handler coverage as **0.3%** — a measurement artifact, because handlers are exercised end-to-end from the `internal/web` package and default per-package instrumentation misses it. Real handler coverage via `-coverpkg` is **55.6%**. The headline number is actively false. No `staticcheck`/`golangci-lint`/`errcheck` (which would enforce the project's own "no ignored errors / no defaults" rule) and no `govulncheck` (relevant given untrusted vCard + image parsing).
- **Location:** `.github/workflows/ci.yml` (test step ~`:35`; vet/gofmt ~`:31-32`).
- **Fix:**
  1. Change the test step to `go test -race -coverpkg=./internal/... -coverprofile=coverage.out ./...` and print `go tool cover -func=coverage.out | tail -1` so the real total is visible; optionally fail below a threshold.
  2. Add a `golangci-lint` step (enable at least `errcheck`, `staticcheck`, `ineffassign`). Expect it to flag the sw_allowed-error patterns in item 8 and the test setup ignores.
  3. Add a `govulncheck ./...` step.
- **Verify:** CI run shows a coverage total near 55–80% (not 0.3%), and the new lint/vuln steps run. Fix or explicitly `//nolint` any newly surfaced findings.

---

## 5. Test the untested privileged and public-facing paths

- **Problem:** at 0% real coverage: contact-scoped sharing end to end, and the destructive/privileged `BookDelete` and `AdminUserPassword`. Contact shares are the app's public attack surface; only book shares are currently tested.
- **Locations:** `handlers/shares.go:55-71` (`ContactShares`, `ContactShareCreate`, `ContactShareRevoke`), `handlers/public.go:66` (`shareContact`), `:98/108/125` (share-photo serving), `:167` (`ShareExportCSV`), `handlers/books.go:142` (`BookDelete`), `handlers/admin_users.go:116` (`AdminUserPassword`). Model of existing coverage: `internal/web/sharing_test.go` (book scope only), `internal/web/user_management_test.go`.
- **Fix:** mirror the existing `internal/web/sharing_test.go` patterns (real router via `buildRouter`, real SQLite via `testutil.NewDB`, real CSRF/session flow):
  1. Contact-scoped share: create → the `/s/{token}` public fetch returns the contact, revoke → 404, gated lockout after `GateMaxFailures`, viewer/stranger authorization (403/404).
  2. `BookDelete`: owner/manager can delete; viewer gets 403, stranger 404; cascades verified.
  3. `AdminUserPassword`: admin resets another user's password; non-admin gets 404 (surface hiding); last-admin/self guards unaffected.
- **Verify:** new tests pass and the `-coverpkg` total (item 4) rises; the named handlers are no longer at 0%.

---

## 6. Complete HTTP server timeouts + global body limit

- **Problem:** only `ReadHeaderTimeout` is set, leaving slowloris/slow-body/stuck-reader exposure. `MaxBytesReader` is applied only on the two upload handlers, so other POST routes have no body ceiling.
- **Locations:** `internal/web/server.go:34` (`http.Server` literal, only `ReadHeaderTimeout: 10s`), body limits currently only at `handlers/contacts.go:175` and `handlers/import.go:47`.
- **Fix:**
  1. Add `ReadTimeout`, `WriteTimeout` (large enough for exports, e.g. 60s), `IdleTimeout`, and set `MaxHeaderBytes` explicitly (per the no-defaults policy) on the `http.Server` literal.
  2. Add a small router middleware in `buildRouter` (`server.go:43+`) that wraps request bodies with `http.MaxBytesReader` at a sane default (e.g. 1 MiB) for all non-upload routes; keep the existing larger caps on the two upload handlers.
- **Verify:** `go test ./...` passes; a manual test posting an oversized body to a normal form route gets a 413/400, and normal requests are unaffected.

---

## 7. Bound the SQLite connection pool

- **Problem:** `sql.Open` is never followed by pool sizing. With WAL plus the app-level write mutex, an unbounded reader pool can starve writers and surface `busy_timeout` errors under load, with no correctness upside (writes are already serialized).
- **Location:** `internal/db/db.go:56` (`sql.Open`), inside `Open` (`:46`).
- **Fix:** after `sql.Open` succeeds, set `pool.SetMaxOpenConns` to a small bound (e.g. `runtime.NumCPU()`), `pool.SetMaxIdleConns` to the same, and `pool.SetConnMaxIdleTime(5 * time.Minute)`. Do not use silent magic numbers without a named constant/comment tying them to the single-writer design.
- **Verify:** `go test ./...` passes (race detector already on in CI). Optionally expose `pool.Stats()` for observability (ties into the metrics follow-up).

---

## 8. Reject unknown `action` in `ImportCommit` (no silent default)

- **Problem:** an unrecognized/absent `action` is coerced to `skip`, silently changing which contacts get inserted. Direct violation of the "never fall back to a guessed default" rule.
- **Location:** `internal/web/handlers/import.go:122-124`.
  ```go
  action := r.PostFormValue("action")
  if action != importActionSkip && action != importActionCreateNew {
      action = importActionSkip
  }
  ```
- **Fix:** replace the coercion with rejection — re-render the import preview with a validation error (mirror `parseShareForm` / admin-form validation), or return a 400. Do not mutate `action`.
- **Verify:** add a handler test posting an invalid `action` and asserting a 4xx / re-rendered error rather than a silent skip. While here, address the sibling swallowed errors flagged by lint (item 4): log `DeleteStaleImportUploads` / `DeleteImportUpload` failures (`import.go:45,186`) instead of `_ =`.

---

## 9. Constrain `share_links.target_id` (schema integrity)

- **Problem:** `target_id` is a polymorphic reference (book id when `scope='book'`, contact id when `scope='contact'`) with no foreign key and no cascade, so deleting a book or contact orphans its share links. Masked by a read-time 404 (`handlers/public.go:302`), but integrity is unenforced and rows accumulate.
- **Location:** `internal/db/migrations/0001_init.sql:71` (`target_id INTEGER NOT NULL`), read path `internal/models/share_link.go:96`.
- **Fix (pick one; migration only — do not edit `0001_init.sql`):**
  - **Option A (cleanup on delete):** in `DeleteAddressBook` / `DeleteContact`, delete matching `share_links` rows (correct `scope` + `target_id`) inside the same write transaction. Simpler; keeps the polymorphic column.
  - **Option B (real FKs, better long-term):** add a new migration splitting into `book_share_links` / `contact_share_links` with proper `ON DELETE CASCADE` FKs, and update `share_link.go` queries. Heavier; also helps the future M:N / CardDAV roadmap.
  - Recommended: Option A now, revisit B when M:N multi-book lands.
- **Verify:** add a test that creates a share link, deletes its target book/contact, and asserts no orphaned `share_links` row remains. Migration runner test (`internal/db`) still green.

---

## Out of scope for this plan (scheduled follow-ups)

Tracked in [00-summary.md](00-summary.md) "Secondary findings" — login rate limiting, bootstrap-admin password strength check, anemic domain types / defined-type enums, repository seam, text-timestamp fragility, duplicated null-helpers/time-format constants, `share_links(scope,target_id)` index, container HEALTHCHECK, `/metrics`, configurable log level, access log, and test-setup error handling.
