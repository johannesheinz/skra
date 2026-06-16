# QA Standards & Test Coverage Review

Reviewer focus: test coverage, test quality, integration/handler exercise, isolation/flakiness, CI gating. Measured on branch `frontend-refinement` with Go 1.26 (`go test ./... -cover`, `-coverprofile`, `-coverpkg`). No code was modified.

## Top findings

1. **Reported handler coverage (0.3%) is a measurement artifact, not reality.** The handlers are exercised end-to-end through `buildRouter`, but from the `internal/web` package, so Go's default per-package instrumentation hides it. True handler coverage measured with `-coverpkg` is **55.6%**. CI reports the misleading 0.3% and no one is measuring the real number. (High)
2. **Contact-scoped shares are entirely untested** — `ContactShares`, `ContactShareCreate`, `ContactShareRevoke`, `shareContact`, `ShareContactPhoto` all at 0.0%. Only book-scoped sharing is tested. This is a security-sensitive public-access path. (High)
3. **`BookDelete` and `AdminUserPassword` handlers are at 0.0%** — destructive/privileged operations with no test at all. (High)
4. **No coverage threshold, no linter, and no `-coverpkg` in CI** (`.github/workflows/ci.yml`). Only `gofmt` + `go vet` gate. Coverage is computed but never asserted or reported. (High)
5. **`internal/models` isolated coverage is 53.8%** and `internal/images` 54.5%, both understated because callers live in other packages — but genuine gaps exist (image EXIF orientation transforms, `vcardio.PrimaryPhone`). (Medium)

## Measured coverage

`go test ./... -cover` (default per-package instrumentation):

| Package | Coverage | Note |
| --- | --- | --- |
| internal/config | 96.2% | |
| internal/vcardio | 91.5% | |
| internal/sharing | 90.9% | |
| internal/ids | 90.0% | |
| internal/web/static | 90.2% | |
| internal/rbac | 85.7% | |
| internal/web | 81.2% | drives the handlers below |
| internal/importing | 75.8% | |
| internal/export | 75.6% | |
| internal/db | 71.5% | |
| internal/auth | 78.9% | |
| internal/images | 54.5% | |
| internal/models | 53.8% | understated; see below |
| **internal/web/handlers** | **0.3%** | **misleading — see finding 1** |
| internal/web/templates | 0.0% | no test files |
| main.go (root) | 0.0% | no test files |
| scripts/seed | 0.0% | dev tooling |
| internal/testutil | 0.0% | test helper, expected |

True handler coverage via `go test ./internal/web/... -coverpkg=./internal/web/handlers/...`: **55.6% of statements**. This is the number that matters, and it is neither reported nor gated.

## Findings by theme

### 1. Coverage measurement & CI gating

- **CI does not measure real coverage. High.** `.github/workflows/ci.yml:35` runs `go test -race -coverprofile=coverage.out ./...` but never uploads, prints, or thresholds the result. Because the handlers package is only reached transitively from `internal/web`, `coverage.out` records handlers at 0.3% — a false signal. Add `-coverpkg=./internal/...` and either fail below a threshold or surface the per-package number.
- **No static analysis beyond `go vet`. High.** `ci.yml:31-32` runs `go vet ./...` and `gofmt` only. No `staticcheck`, `golangci-lint`, `govulncheck`, or `errcheck`. Given the CLAUDE.md rule "never use default values, throw exceptions if situations are unclear," an errcheck-style linter would directly enforce project standards.
- **Race detector is on in CI (good). Low.** `ci.yml:35` uses `-race`; the session store and DB write serialization benefit from this.

### 2. Untested critical/privileged paths

Handler functions at 0.0% under `-coverpkg` (real gaps, not artifacts):

- **`handlers/books.go:142` `BookDelete` — 0.0%. High.** Destructive; no test exercises deletion or its authorization (`authorizeBook` write check) on the delete route.
- **`handlers/admin_users.go:116` `AdminUserPassword` — 0.0%. High.** Admin resetting another user's password is untested. `internal/models/user_admin_test.go` and `internal/web/user_management_test.go` cover create/edit/delete and last-admin protection, but not the password-reset handler.
- **Contact-scoped sharing — 0.0%. High.** `handlers/shares.go:55-71` (`ContactShares`, `ContactShareCreate`, `ContactShareRevoke`), `handlers/public.go:66` `shareContact`, `public.go:98/108/125` `ShareBookContactPhoto`/`ShareContactPhoto`/`serveSharePhoto`, `public.go:167` `ShareExportCSV`. `internal/web/sharing_test.go` only exercises `ScopeBook` (`sharing_test.go:187` builds `/books/{id}/shares`); the `/contacts/{id}/shares` routes and share-photo serving over `/s/{token}` have zero coverage. This is the app's public-facing attack surface.
- **`handlers/import.go:195` `importFormError`, `handlers/export.go:133` `exportError`, `handlers/public.go:302` `shareTargetError`, `handlers/contacts.go:411` `bookContactsURL` — 0.0%. Medium.** Error/helper branches never hit; error rendering paths for import/export/share are unverified.
- **`handlers/shares.go:210` `shareStatus` (33.3%), `shares.go:178` `parseShareForm` (50.0%). Medium.** Partial; failure/edge branches uncovered.

### 3. Model-layer gaps

`internal/models` reports 53.8% in isolation, but many functions are only reached through handlers (so covered in reality, e.g. `ImportContacts` is exercised by `internal/web/import_test.go:63` via `/import/commit`). Genuine untested items:

- **`models/import.go` upload lifecycle. Medium.** `CreateImportUpload`, `GetImportUpload`, `DeleteImportUpload`, `DeleteStaleImportUploads`, `ExistingContactKeys` all 0.0% at unit level. The staged-upload flow is covered end-to-end via the web test, but `DeleteStaleImportUploads` (a GC/cleanup path, likely called from a job or startup) has no caller in tests — verify it is exercised anywhere.
- **`models/contact.go` photo accessors. Medium.** `SetContactPhoto`, `GetContactPhoto`, `GetContactPhotoMeta`, `DeleteContactPhoto`, `GetPhotoBytes` show 0.0% in the models package. `internal/web/photo_test.go` and `server_test.go:190` cover photo serving through HTTP, so these are indirectly hit — but there is no direct model-level test asserting ETag/mime persistence semantics.
- **`models/contact.go:276` `ListContactsForExport`, `contact.go:164` `GetContactDetails` — 0.0% at unit level.** Reached via export/show handlers; no direct model test of their query correctness (e.g. ordering, field mapping).

### 4. Image processing

- **EXIF orientation transforms untested. Medium.** `internal/images/process.go`: `flipH` (95), `flipV` (107), `rotate180` (119), `rotate270` (145) all 0.0%; `applyOrientation` (74) only 22.2%. `process_test.go:101` tests `readOrientation` parsing for orientation 6 (rotate90, which is covered) but never feeds an image with orientations 2/4/5/7 through `Process`. Malformed/rotated uploads from real cameras will hit these untested branches. `orientationFromTIFF` (186) at 69.6% also leaves malformed-TIFF branches uncovered.

### 5. Smaller pure-function gaps

- **`vcardio.PrimaryPhone` (vcardio.go:68) — 0.0%. Low.** Package is otherwise 91.5%; this accessor has no test despite `PrimaryEmail`-style helpers being easy to table-test.
- **`sharing.ValidMode`/`ValidScope` — 0.0%. Low.** Validators unused-in-test; the invalid-input rejection is only checked indirectly. Add a trivial table test.
- **`importing.BuildCanonicalRaw` (vcard.go:97) — 0.0%. Low.** Canonicalization of the stored vCard is untested; regressions here silently corrupt exports.
- **`auth/cookie.go` `NewSessionCookie`/`ClearSessionCookie` — 0.0%. Low.** Cookie attributes (Secure/HttpOnly/SameSite/expiry) are security-relevant and asserted nowhere; add a direct test on the returned `http.Cookie`.

## Test quality

Overall the existing tests are strong: end-to-end through the real router, real SQLite, real CSRF/session flows.

- **Good: genuine assertions, not happy-path-only.** `internal/web/sharing_test.go` verifies leak prevention (`sharing_test.go:89` asserts contents are NOT present before the gate), lockout after `GateMaxFailures` (`:116`), revocation → 404 (`:156`), and RBAC (viewer → 403 at `:211`). `internal/rbac/can_test.go:42-56` covers admin bypass, viewer read-yes/write-no, and stranger invisibility.
- **Good: no skipped, empty, or trivial tests.** Grep found zero `t.Skip`/`SkipNow`/`Skipf` and no assertion-free tests.
- **Good: consistent isolation via `testutil.NewDB`.** `internal/testutil/db.go:13` gives each test a fresh migrated DB in `t.TempDir()` with `t.Cleanup`. No shared global DB, so cross-test contamination is unlikely.
- **Weak: setup errors are widely ignored. Medium.** Extensive `book, _ := models.CreateAddressBook(...)` / `contact, _ := models.CreateContact(...)` across `internal/web/*_test.go` (e.g. `sharing_test.go:44-48`, `contacts_test.go:108-114`, `export_test.go:19-20`). If a setup call starts failing, the test proceeds with zero values and fails with a confusing downstream error instead of a clear "setup failed." Prefer `require`-style fatal checks (the `seedUser` helper at `server_test.go:58` does this correctly — apply the same discipline to book/contact seeding).
- **Weak: table-driven style is underused. Low.** Most tests are sequential scenarios. Pure validators (`ValidMode`, `ValidScope`, `PrimaryPhone`, password `NeedsRehash`) and orientation transforms are ideal table-test candidates and would cheaply close the 0% gaps above.
- **Weak: coverage varies inside otherwise-tested functions.** e.g. `models/address_book.go:107` `UpdateAddressBook` 71.4%, `contact.go:135` `UpdateContact` 71.4%, `db/db.go:46` `Open` 50.0% — error branches (constraint violations, IO failures) mostly uncovered. `auth/session.go` `Delete`/`DeleteExpired` at 66.7% leave the error path untested.

## Flakiness & isolation

- **No `t.Parallel()` anywhere.** Not a correctness risk, but the suite is fully serial; with per-test temp DBs it could safely parallelize package-internal tests to cut wall time. Low priority.
- **No time-based flakiness observed.** Share lockout and session expiry are driven by explicit `IncrementShareFailure` calls and DB state, not `time.Sleep`. Good.
- **`main.go` critical paths (`backup`, `createAdmin`, `serve`, `run`) all 0.0%. Medium.** CLI subcommands — `backup` (DB snapshot) and `createAdmin` (bootstrap admin) are operationally important and have no test. `db.Snapshot`/`vacuumIntoSQL` are covered at the package level, but the CLI wiring, arg parsing, and `usageError` paths are not.

## Recommended actions (priority order)

1. Add `-coverpkg=./internal/...` to the CI test step and print/threshold real coverage; the current 0.3% handler number is actively misleading. (High)
2. Add handler tests for contact-scoped shares and share-photo serving over `/s/{token}` — the untested public attack surface. (High)
3. Add tests for `BookDelete` and `AdminUserPassword`. (High)
4. Add `golangci-lint` (with `errcheck`) to CI to enforce the "no ignored errors / no defaults" project standard. (High)
5. Add table tests for image EXIF orientations 2/4/5/7 through `images.Process`. (Medium)
6. Replace ignored-error setup (`x, _ := ...`) in `internal/web/*_test.go` with fatal checks. (Medium)
7. Add cheap table tests for `PrimaryPhone`, `ValidMode`, `ValidScope`, `BuildCanonicalRaw`, and cookie-attribute assertions. (Low)
