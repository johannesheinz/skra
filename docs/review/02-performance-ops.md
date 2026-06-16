# Performance & Operations Review

Scope: database access patterns, SQLite/pool configuration, image processing, import/export paths, HTTP server config, static assets, Dockerfile, CI, dependency hygiene, and observability. Review only; no code was modified.

Reviewed at: current `frontend-refinement` branch.

## Top findings

- **No connection pool limits set** — with WAL and no `SetMaxOpenConns`, concurrent readers plus the write mutex can exhaust connections and starve writes under load. (`internal/db/db.go:56`, High)
- **Expired sessions are never purged in production** — `SessionStore.DeleteExpired` exists but is only called from tests; the `sessions` table grows unbounded. (`internal/auth/session.go:74`, `main.go:57`, High)
- **`incremental_vacuum` is configured but never invoked** — `auto_vacuum=INCREMENTAL` reclaims nothing without a periodic `PRAGMA incremental_vacuum`, so the file only ever grows after deletes. (`internal/db/db.go:160`, Medium)
- **Photo export loads every BLOB into memory and buffers the whole download** — book export with photos holds all photos plus the full output in RAM; large books can spike memory hard. (`internal/web/handlers/export.go:37`, `internal/web/handlers/export.go:47`, High)
- **Missing HTTP server timeouts and body-size ceiling on non-upload routes** — only `ReadHeaderTimeout` is set; no `ReadTimeout`/`WriteTimeout`/`IdleTimeout` and no default body cap, leaving slow-body and slowloris-style exposure. (`internal/web/server.go:34`, High)

---

## Database access patterns

### Missing index on `share_links(scope, target_id)` — Medium
`internal/models/share_link.go:98` lists shares by `WHERE scope = ? AND target_id = ? ORDER BY id DESC`, but `0001_init.sql` only indexes `token` (`internal/db/migrations/0001_init.sql:81`). The shares-management page (`BookShares`, `ContactShares`) does a full-table scan of `share_links`. Small today, but the only covered access path is by token.

```sql
CREATE INDEX idx_share_links_target ON share_links(scope, target_id);
```

### Contact search uses leading-wildcard LIKE — full scan — Medium
`internal/models/contact.go:229` filters with `full_name LIKE '%q%' OR org LIKE ... OR primary_email LIKE ... OR primary_phone LIKE ...`. Leading `%` defeats any B-tree index, so every search is a full scan of the book's contacts, and it runs twice per request (COUNT then SELECT at `contact.go:234` and `contact.go:239`). For the stated shared-address-book scale this is acceptable, but it will not scale to large books. Consider FTS5 (`contacts_fts`) if books grow into the tens of thousands.

### Book listing runs a correlated COUNT subquery per row — Low
`internal/models/address_book.go:132` embeds `(SELECT COUNT(*) FROM contacts c WHERE c.address_book_id = ab.id)` per book row. It is indexed by `idx_contacts_book`, so each count is an index range scan — fine for a per-user list of books, but note it is O(books × log contacts). Not a concern at expected scale.

### Export path is an N+1 photo fetch — High
`internal/web/handlers/export.go:34` iterates contacts and issues one `GetPhotoBytes` query per contact that has a photo (`export.go:37`). For a book of N photographed contacts that is N+1 round trips plus N BLOBs resident in memory simultaneously (see memory finding below). A single `JOIN` or a streamed cursor over `contacts LEFT JOIN contact_photos` would collapse this to one query.

### Single-contact vCard export loads the entire book — Medium
`internal/web/handlers/export.go:94` (`writeContactVCard`) calls `ListContactsForExport` for the **whole** address book, then linearly scans for the one matching `contact.ID` (`export.go:101`). Exporting one contact from a 10k-contact book reads all 10k rows (each including `vcard_raw`). Fetch the single contact directly by id/public_id instead.

### Query building is parameterized and safe — Positive
`ListContacts` builds the `WHERE`/args slices with placeholders, never string-concatenating user input (`internal/models/contact.go:224`). `vacuumIntoSQL` single-quote-escapes its path (`internal/db/db.go:118`). No SQL-injection surface observed.

## SQLite connection / pool configuration

### No pool sizing — writer starvation risk under load — High
`sql.Open` in `internal/db/db.go:56` is never followed by `SetMaxOpenConns`/`SetMaxIdleConns`/`SetConnMaxLifetime`. Consequences:

- The default pool is unbounded. Under concurrent read load the pool can open many connections; combined with the app-level `writeMu`, a write can still block behind long-running reads holding connections, and `busy_timeout(5000)` will surface as latency then errors.
- The canonical SQLite-in-Go pattern is a **single** writer connection and a bounded set of reader connections. At minimum:

```go
pool.SetMaxOpenConns(runtime.NumCPU()) // or a small fixed cap; readers
pool.SetConnMaxIdleTime(5 * time.Minute)
```

Because writes are already serialized by `writeMu`, an unbounded read pool provides no correctness benefit and adds contention.

### WAL / busy_timeout / synchronous / foreign_keys — Positive
`connectionPragmas` (`internal/db/db.go:36`) applies `journal_mode(WAL)`, `busy_timeout(5000)`, `foreign_keys(ON)`, `synchronous(NORMAL)` via the DSN so they re-apply on every connection. This is the correct approach for modernc.org/sqlite and a good default set.

### Transaction usage and the write mutex — Positive
The `DB.Write`/`ExecWrite` design (`internal/db/db.go:125`, `internal/db/db.go:142`) serializes all writes through `writeMu`, matching SQLite's single-writer model, and multi-statement writes (photo upsert + flag, import batch) run inside one transaction (`internal/models/contact.go:378`, `internal/models/import.go:122`). Rollback-on-error via `defer tx.Rollback()` is correct.

### `wal_autocheckpoint` not tuned — Low
No explicit checkpoint policy. WAL defaults to auto-checkpoint at 1000 pages, which is usually fine, but on a write-heavy import the `-wal` file can grow between checkpoints. Not a defect; note for capacity planning.

## Database maintenance / lifecycle

### Expired sessions never purged — High
`SessionStore.DeleteExpired` (`internal/auth/session.go:74`) is only referenced from `session_test.go`. `serve()` (`main.go:57`) starts no background maintenance goroutine, so the `sessions` table grows for the life of the deployment (7-day TTL means rows accumulate but are never deleted). Add a periodic ticker in `serve()`:

```go
go func() {
    t := time.NewTicker(1 * time.Hour)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-t.C:
            _ = sessions.DeleteExpired(ctx)
        }
    }
}()
```

### `incremental_vacuum` configured but never run — Medium
`initAutoVacuum` sets `auto_vacuum=INCREMENTAL` (`internal/db/db.go:160`), which by design reclaims space **only** when `PRAGMA incremental_vacuum` is executed. Nothing ever runs it. After bulk deletes (books/contacts/photos), the file will not shrink. Either run `PRAGMA incremental_vacuum` on the maintenance ticker, or document that operators rely on the `skra backup` `VACUUM INTO` for compaction.

### Stale import uploads only GC'd opportunistically — Low
`DeleteStaleImportUploads` runs only at the start of `ImportUpload` (`internal/web/handlers/import.go:45`). If imports stop happening, orphaned upload BLOBs (which can be up to 10 MiB each) persist. Folding this into the maintenance ticker would make cleanup deterministic.

## Image processing (`internal/images/process.go`)

### No decode-bomb / pixel-dimension guard — High
`Process` calls `image.Decode` on attacker-controlled bytes (`internal/images/process.go:29`) with only a 10 MiB request-body cap upstream. A 10 MiB PNG can decode to a very large pixel buffer (a "decompression bomb"): e.g. a highly compressible image declaring huge dimensions allocates `width × height × 4` bytes for the `image.NewRGBA` used by every rotation/flip (`process.go:66`, `process.go:98`, etc.) and by the decoder itself. There is no check on decoded dimensions before allocation. Use `image.DecodeConfig` first and reject images whose `Width*Height` exceeds a sane cap (e.g. 40 MP) before decoding fully.

### Rotation/flip use per-pixel `src.At`/`dst.Set` — Medium
`flipH`/`flipV`/`rotate*` (`internal/images/process.go:95`–`155`) loop pixel-by-pixel through the `image.Image` interface, which is significantly slower and allocates per call. These run before downscale, so they operate on the full-resolution image. For the max input size this is CPU-heavy. Reordering to downscale first (when orientation is a no-op or after cheaply applying it), or using `draw.Draw` with a transform, would cut work. Low urgency given the 512px output cap, but the ordering means worst-case work is on the full decoded image.

### Output cap and metadata stripping — Positive
Longest-edge cap of 512 (`process.go:19`), JPEG quality 80, and re-encode-from-pixels to strip EXIF/GPS (`process.go:37`) keep stored BLOBs small and safe. Good design.

## Import / export streaming vs buffering

### Uploads and staged bytes are fully buffered — Medium
`ImportUpload` reads the whole file with `io.ReadAll` (`internal/web/handlers/import.go:62`) then stores it as a BLOB in `import_uploads` (`internal/models/import.go:33`), and `ImportCommit` re-parses the stored bytes (`import.go:133`). The 10 MiB cap bounds this, but the file is held in memory at least twice (parsed records + raw bytes) and round-tripped through the DB. Acceptable for the size cap; note the double parse (preview then commit) doubles CPU for large vCard batches.

### Exports buffer the entire response in memory — High
Both vCard and CSV exports build a `bytes.Buffer` and write it whole (`internal/web/handlers/export.go:47`, `export.go:73`, `internal/web/handlers/public.go:190`). Combined with the N+1 photo loads, a book export with many photos holds: all photo BLOBs + all `vcard_raw` strings + the full base64-inflated output buffer, all resident at once. Base64 inflates photos ~33%. This is the single largest memory-spike vector in the app. Prefer streaming directly to `w` (the `export.WriteVCards`/`WriteCSV` functions already take an `io.Writer` — the handlers just need to pass `w` and set headers first), and fetch photos lazily per contact.

### vCard/CSV writers are streaming-ready — Positive
`export.WriteVCards` and `export.WriteCSV` are written against `io.Writer` (`internal/export/vcard.go:29`, `internal/export/csv.go:24`) and `encoding/csv` flushes incrementally. Only the handlers force buffering; the library layer is already correct.

### CSV injection defused — Positive
`sanitizeCSVField` (`internal/export/csv.go:48`) neutralizes formula-injection leading characters. Good.

## HTTP server configuration

### Incomplete timeouts — High
`internal/web/server.go:34` sets only `ReadHeaderTimeout: 10s`. Missing:

- `ReadTimeout` — a slow client can dribble a request body indefinitely (the upload routes cap size but not time).
- `WriteTimeout` — a slow/stuck client reading a large export can pin a goroutine and connection.
- `IdleTimeout` — keep-alive connections have no idle bound.
- `MaxHeaderBytes` — left at default (acceptable, but worth setting explicitly per the no-defaults project policy).

```go
httpServer: &http.Server{
    Addr:              cfg.Listen,
    Handler:           router,
    ReadHeaderTimeout: 10 * time.Second,
    ReadTimeout:       30 * time.Second,
    WriteTimeout:      60 * time.Second, // exports can be large
    IdleTimeout:       120 * time.Second,
}
```

### No global request body limit — Medium
`MaxBytesReader` is applied only in the two upload handlers (`internal/web/handlers/contacts.go:175`, `internal/web/handlers/import.go:47`). Other POST routes (login, member/contact create, form posts) rely on `ParseForm` with no explicit cap. A default body-limit middleware on the router (`internal/web/server.go:56`) would bound every route.

### Graceful shutdown — Positive
`Run` uses `signal.NotifyContext` + `http.Server.Shutdown` with a 10s bounded context (`internal/web/server.go:158`, `main.go:77`). Correct and clean.

### Recoverer / RealIP / RequestID middleware — Positive
`chi` `Recoverer`, `RealIP`, `RequestID` are wired (`internal/web/server.go:57`). Panics won't crash the process; request IDs aid tracing.

## Static asset serving / caching

### Well done — Positive
`internal/web/static/static.go` embeds assets, precomputes SHA-256 ETags at startup (`static.go:37`), serves versioned URLs as `immutable, max-age=31536000` and bare URLs with a short max-age + ETag revalidation (`static.go:94`), and supports conditional GET (`static.go:100`). Content types are set explicitly. This is a strong implementation.

### No pre-compression / Content-Encoding — Low
Assets are served uncompressed with no `gzip`/`br` negotiation, and there is no compression middleware on dynamic responses either. For a self-hosted single-user-scale app this is minor, but htmx + CSS + woff2 would benefit from gzip. woff2 is already compressed; CSS/JS are not.

### Photo cache is `private, max-age=300` with strong ETag — Positive
`streamPhoto` (`internal/web/handlers/photo.go:53`) sets a private cache and honors `If-None-Match`, avoiding re-sending BLOBs. Correct for per-user authorized content.

## Dockerfile

### Solid — Positive
- Multi-stage build; final image is `distroless/static-debian12:nonroot` (`Dockerfile:17`) — minimal, non-root (uid 65532), no shell/package manager.
- `CGO_ENABLED=0` static binary with `-trimpath -ldflags="-s -w"` (`Dockerfile:13`) reduces size and strips symbols.
- Dependency layer cached separately (`Dockerfile:8`).
- Data dir pre-created and `chown`ed to `nonroot`, declared as a `VOLUME` (`Dockerfile:20`).

### No container HEALTHCHECK — Medium
The app exposes `/healthz` and `/readyz` (`internal/web/server.go:63`) but the Dockerfile declares no `HEALTHCHECK`. Distroless has no shell/curl, so a healthcheck would need the binary to grow a `healthcheck` subcommand (or rely on the orchestrator's HTTP probe). Given distroless, documenting reliance on an external/orchestrator probe is acceptable — but the gap should be explicit.

### Build does not run as non-root in build stage — Low
Minor; build stage is discarded. Not a runtime concern.

## CI workflow

### Good coverage — Positive
`.github/workflows/ci.yml` runs `gofmt` check, `go vet`, `go test -race -coverprofile`, a static build, and a Docker image build (`ci.yml:22`–`50`). Race detector in CI is valuable given the `writeMu` concurrency.

### No linting beyond vet — Medium
No `staticcheck`/`golangci-lint` step. `go vet` misses many issues (unused results, ineffective assignments, SQL-string mistakes). Adding `golangci-lint` would catch e.g. the ignored-error patterns and simplify future reviews.

### Coverage is collected but not enforced or reported — Low
`coverage.out` is produced (`ci.yml:35`) but never uploaded or gated. Consider a threshold or artifact upload.

### No govulncheck / vulnerability scan — Medium
No `govulncheck ./...` step. Given the app parses untrusted vCard and image input, a vulnerability scan on dependencies (`emersion/go-vcard`, `golang.org/x/image`, `modernc.org/sqlite`) in CI would be prudent.

### `pull_request` runs on forks with `contents: read` — Positive
Least-privilege token (`ci.yml:8`). Good.

## Dependency hygiene (dependabot)

### Comprehensive — Positive
`.github/dependabot.yml` covers `gomod`, `github-actions`, and `docker`, all weekly, with grouping to reduce PR noise (`dependabot.yml:1`–`26`). This is a good baseline.

### Docker updates ungrouped — Low
The `docker` ecosystem has no `groups` block (`dependabot.yml:22`), unlike the other two. Minor inconsistency; a single base image means it rarely matters.

## Observability & config

### Structured JSON logging — Positive
`slog` JSON handler to stdout (`main.go:58`), with `RequestID` middleware available for correlation. Errors are logged with context throughout the handlers. Appropriate for container log collection.

### No metrics endpoint — Medium
There is no `/metrics` (Prometheus) or any counter/histogram instrumentation. For an "operations" posture, request latency, error rates, DB query timing, and pool stats (`DB.Stats()`) would be valuable. `DB.Stats()` in particular would surface the pool-exhaustion risk noted above.

### Log level is fixed — Low
`slog.NewJSONHandler(os.Stdout, nil)` (`main.go:58`) uses default options, so level is Info and not configurable. A `SKRA_LOG_LEVEL` env var (validated per the no-defaults policy) would help debugging in production without a rebuild. Also note request logging is not enabled (chi's `middleware.Logger` is intentionally omitted — `RequestID` is present but no access log), so there is no per-request access record.

### Config validation — Positive
`internal/config/config.go` follows the project's no-silent-defaults rule strictly: every required var is validated, `parseBool` rejects anything but `true`/`false`, `ExternalURL` scheme is checked, and `SessionKey` has a minimum length (`config.go:44`–`89`). All problems are aggregated into one error. Exemplary.

### Health vs readiness split — Positive
`/healthz` is a pure liveness probe (`internal/web/handlers/health.go:10`); `/readyz` runs a cheap `SELECT 1` and returns 503 on DB failure (`health.go:18`). Correct separation for orchestration.

---

## Summary table

| Area | Finding | Severity | Location |
|------|---------|----------|----------|
| Pool | No `SetMaxOpenConns`; writer starvation risk | High | `internal/db/db.go:56` |
| Maintenance | Expired sessions never purged | High | `internal/auth/session.go:74`, `main.go:57` |
| Export | N+1 photo fetch | High | `internal/web/handlers/export.go:37` |
| Export | Whole response buffered in memory | High | `internal/web/handlers/export.go:47`, `public.go:190` |
| Images | No decode-bomb / dimension guard | High | `internal/images/process.go:29` |
| Server | Missing Read/Write/Idle timeouts | High | `internal/web/server.go:34` |
| Maintenance | `incremental_vacuum` never invoked | Medium | `internal/db/db.go:160` |
| DB | Missing index on `share_links(scope,target_id)` | Medium | `internal/models/share_link.go:98` |
| DB | Leading-wildcard LIKE full scan on search | Medium | `internal/models/contact.go:229` |
| Export | Single-contact export reads whole book | Medium | `internal/web/handlers/export.go:94` |
| Images | Per-pixel rotate/flip on full-res image | Medium | `internal/images/process.go:95` |
| Import | Full buffering + double parse | Medium | `internal/web/handlers/import.go:62` |
| Server | No global body-size limit | Medium | `internal/web/server.go:56` |
| Docker | No HEALTHCHECK (distroless constraint) | Medium | `Dockerfile:17` |
| CI | No linter / govulncheck | Medium | `.github/workflows/ci.yml` |
| Observability | No metrics endpoint | Medium | `main.go:57` |
| DB | Correlated COUNT subquery per book | Low | `internal/models/address_book.go:132` |
| Maintenance | Stale import uploads GC'd only opportunistically | Low | `internal/web/handlers/import.go:45` |
| Static | No gzip/br for CSS/JS | Low | `internal/web/static/static.go:104` |
| Observability | Fixed log level; no access log | Low | `main.go:58` |
