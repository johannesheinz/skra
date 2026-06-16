# Skrá — Best Practices & Maintainability Review

Scope: idiomatic Go, package structure/layering, error handling, naming/readability/duplication, comment quality, consistency, and adherence to `docs/01_skra-development-principles.md` and `CLAUDE.md`. Review only — no code was modified.

Overall the codebase is well above average: clear package boundaries (`db` / `models` / `rbac` / `sharing` / `auth` / `web`), consistent error wrapping with package-prefixed sentinels, disciplined `sql.NullString` handling, buffered template rendering, and a genuinely low comment density that explains *why* rather than *what*. The findings below are mostly refinements; there are no correctness-critical maintainability defects.

## Top findings

- **Silent action fallback in `ImportCommit`** violates the "no silent defaults" principle — an unrecognized `action` is coerced to `skip` instead of rejected. (High)
- **Duplicated `nullString` / `nullableString` helpers and two identical `sqliteTimeFormat` constants** across `models` and `auth`. (Medium)
- **`ContactInput` carries dead/legacy fields** (`FullName`, `PrimaryEmail`, `PrimaryPhone`) that the form path never populates; the public `Details()` wrapper duplicates `toDetails()`. (Medium)
- **`writeContactVCard` loads and scans the entire book to export one contact** — an O(book) query plus linear scan where an O(1) lookup exists. (Medium)
- **Swallowed errors on the housekeeping/logout paths** (`DeleteStaleImportUploads`, `DeleteImportUpload`) drop errors with a bare `_ =` and no log. (Low/Medium)

---

## Adherence to project principles

### High — Silent default in `ImportCommit` contradicts "no silent defaults"
`internal/web/handlers/import.go:121-125`

```go
action := r.PostFormValue("action")
if action != importActionSkip && action != importActionCreateNew {
    action = importActionSkip
}
```

The development principles (`docs/01_skra-development-principles.md` §Code) and `CLAUDE.md` both state: "If a situation is unclear (missing config, ambiguous input), return an error or fail fast — never fall back to a guessed default." An unknown/absent `action` silently becomes `skip`. Because `skip` changes which contacts get inserted (duplicates are dropped), a malformed submission produces a different, silent outcome. Prefer re-rendering the preview with a validation error, matching how `parseShareForm` and the admin forms reject bad input. `parsePage` (`contacts.go:402`) has the same shape but is defensible (a bad `?page=` genuinely defaulting to 1 is conventional and harmless); the import case is not.

### Low — `DefaultParams` comment says "no default" territory but the field is a package-global `var`
`internal/auth/password.go:36`

`DefaultParams` is an exported mutable package variable. Nothing mutates it today, but an exported `var` invites accidental global mutation from tests or callers. Consider making it a `const`-like unexported value with an accessor, or documenting that it is read-only. Minor.

---

## Error handling

### Medium — Swallowed errors on housekeeping paths
`internal/web/handlers/import.go:45` and `internal/web/handlers/import.go:186`

```go
_ = models.DeleteStaleImportUploads(r.Context(), h.DB)   // line 45
...
_ = models.DeleteImportUpload(r.Context(), h.DB, token)  // line 186
```

Both discard the error with no log. The logout path deliberately logs its delete failure (`auth.go:76-78`), and `ShareEntry` logs `IncrementShareUse` failures (`public.go:27-29`), so silent discard here is inconsistent. A failed stale-upload purge means the `import_uploads` table grows unbounded (staged blobs can be multi-MB) with zero signal. At minimum log at `Error`/`Warn`. This is the right pattern already used elsewhere:

```go
if err := models.DeleteImportUpload(r.Context(), h.DB, token); err != nil {
    h.Logger.Error("delete import upload failed", "err", err)
}
```

### Low — `LoadUser` drops the DB error when the session's user is missing
`internal/auth/middleware.go:44-49`

```go
user, err := models.GetUserByID(r.Context(), a.DB, userID)
if err != nil {
    // Session points at a missing user; treat as anonymous.
    next.ServeHTTP(w, r)
    return
}
```

The comment assumes `err` is "user not found," but `GetUserByID` also returns wrapped scan/connection errors. A transient DB failure is silently downgraded to "anonymous," which is both a debuggability gap and a subtle availability masking. Distinguish `errors.Is(err, models.ErrUserNotFound)` (treat as anonymous) from other errors (log them), mirroring the pattern the session-lookup branch just above already uses (`middleware.go:37-40`).

### Low — `renderContactWithError` ignores the `rbac.Can` error
`internal/web/handlers/contacts.go:231`

```go
canManage, _ := rbac.Can(r.Context(), h.DB, user, contact.AddressBookID, rbac.Write)
```

Every other call site checks this error and 500s. Here it is dropped, so a DB failure yields a page rendered with `CanManage=false` silently. Low impact (this is already an error-rendering path) but inconsistent with the codebase's own rule.

### Low — `writeContactVCard` drops the photo-load error
`internal/web/handlers/export.go:105`

```go
if photo, ok, err := models.GetPhotoBytes(...); err == nil && ok {
```

The sibling `writeBookVCard` (`export.go:37-42`) treats a photo-load error as fatal (`h.exportError`). Here the single-contact path silently exports without the photo on error. Harmless to output correctness, but the divergence is the kind of drift that accumulates.

---

## Duplication

### Medium — `nullString` and `nullableString` are two names for the same helper
`internal/models/address_book.go:171` (`nullString`) and `internal/models/share_link.go:184` (`nullableString`)

```go
// address_book.go
func nullString(s string) any {
    if strings.TrimSpace(s) == "" { return nil }
    return s
}
// share_link.go
func nullableString(s string) any {
    if s == "" { return nil }
    return s
}
```

Two functions, same package, near-identical behavior (only the `TrimSpace` differs). Callers can't tell which trimming semantics apply. Collapse to one helper with defined semantics. There is also `nullableInt` (`share_link.go:191`) with the "0 means NULL" convention that is fine but should sit beside the consolidated helper.

### Medium — `sqliteTimeFormat` defined twice with identical value and comment
`internal/auth/session.go:19` (`sessionTimeFormat`) and `internal/models/share_link.go:18` (`sqliteTimeFormat`)

Both are `"2006-01-02 15:04:05"` with the same "matches SQLite's datetime('now')" comment. This is a format the whole app depends on for correct time comparisons; it should live in exactly one place (e.g. exported from `db` as `db.SQLiteTimeFormat`) so a future change can't desync the two. Right now a mismatch would silently break either session expiry or share expiry.

### Low — `authorizeBook` and `authorizeContact` share a long identical 404/403 preamble
`internal/web/handlers/books.go:161-191` and `internal/web/handlers/contacts.go:250-287`

Both repeat: load-by-publicID → distinguish not-found vs internal error → `rbac.Can` → `!Visible` 404 → `!Allow` 403. The `Contact` variant just wraps the book one plus a contact fetch. Not urgent (the 4-return-tuple makes extraction awkward), but a shared `func (h) enforce(w, r, bookID, action) (Decision, bool)` helper would remove ~25 duplicated lines and guarantee the two paths stay in lockstep.

### Low — CSV-row construction duplicated
`internal/web/handlers/export.go:69-71` and `internal/web/handlers/public.go:187-189`

Identical `export.CSVRow{...}` mapping from `ContactExport`. A one-line `export.CSVRowFrom(c)` (or a method on `ContactExport`) removes the copy.

---

## Dead / redundant code

### Medium — `ContactInput` legacy convenience fields are effectively dead in the HTTP path
`internal/models/contact.go:39-55`, `contact.go:77-82`; consumed only in tests/seed

`FullName`, `PrimaryEmail`, `PrimaryPhone` on `ContactInput` are documented as "legacy convenience … so simple callers and tests stay terse." But `contactInputFromForm` (`contacts.go:289-302`) never sets them, and the seed script uses the rich fields. So in the real application these are only-ever-zero fields plus fold-in logic in `toDetails` that never fires. Given the "prefer speaking names, low comment density, no legacy cruft" ethos, either drop them (moving the terseness into a test helper) or document them as test-only. Keeping production-dead branches around is exactly the kind of thing the principles push against.

### Medium — `Details()` is a thin public alias for `toDetails()`
`internal/models/contact.go:59-61`

```go
func (in ContactInput) Details() vcardio.Details { return in.toDetails() }
func (in ContactInput) toDetails() vcardio.Details { ... }
```

Two methods doing the same thing; `Details()` exists only because handlers need the exported name to re-render the form. Just export `Details()` and delete `toDetails()`, calling `in.Details()` internally. The private/public split adds a layer with no invariant behind it.

### Low — `Contact.HasPhoto`/`ETag` populated inconsistently across scanners
`internal/models/contact.go:322-335` vs the export/photo structs

`scanContact` sets `HasPhoto` and `ETag`, but several read paths never use them, and `ContactExport` re-derives `HasPhoto` independently (`contact.go:290-296`). Not a bug, but the `Contact` struct is doing double duty as both a list row and a detail row; a smaller list-projection type would make intent clearer. Low priority.

---

## Performance / correctness-adjacent

### Medium — Exporting one contact scans the whole book
`internal/web/handlers/export.go:93-116`

```go
exports, err := models.ListContactsForExport(r.Context(), h.DB, contact.AddressBookID)
...
for _, c := range exports {
    if c.ID == contact.ID { ... break }
}
```

To export a single contact this loads every contact in the book (all `vcard_raw` blobs) and linear-scans for one id. `GetContactDetails`/`GetContactByID` already fetch a single row; add a `models.GetContactExport(ctx, d, id)` (single-row `SELECT ... WHERE id = ?`) and drop the loop. On a large shared book this is a real, avoidable cost on every per-contact vCard download and every contact-scoped share.

### Low — `ListContacts` builds the `LIKE` filter by string concatenation
`internal/models/contact.go:224-242`

The `where`/`args` assembly is parameterized (good, no injection), but the four-column `LIKE` with a single query string is fine for now; note only that unindexed leading-`%` `LIKE` across four columns will table-scan as books grow. Acceptable at current scale; flag if books get large. No action required now.

---

## Idiomatic Go / modern features

### Low — `for i := 0; i < n; i++` where `range` over an int would read cleaner (Go 1.22+)
`internal/web/handlers/contacts.go:345` and `internal/images/process.go` loops

The project targets Go 1.26 and the principles explicitly ask to use modern features. `for i := range n` (integer range, Go 1.22) would apply at `addressesFromForm` (`contacts.go:345`). The pixel loops in `images/process.go` are genuinely 2D and clearer as-is. Very minor; call out only because the principles invite it.

### Low — `Handlers.render` mutates the caller's `data` map
`internal/web/handlers/handlers.go:51-64`

`render` writes `data["User"]` and `data["CSRFToken"]` into the passed-in map. Handlers always pass a fresh literal so there's no aliasing bug today, but a shared/reused map would be silently mutated. Documenting the contract (or defensively copying) would harden it. Minor.

### Low — `contextKey` iota with a single value
`internal/auth/middleware.go:13-15`

```go
type contextKey int
const userContextKey contextKey = iota
```

Fine and idiomatic. Noting only that a single unexported struct-type key (`type userKey struct{}`) is the more common modern pattern and avoids any collision even across `int`-typed keys. Take-it-or-leave-it.

---

## Naming / readability (positive notes + nits)

- The `rbac.Decision{Allow, Visible}` split and its 404-vs-403 documentation (`rbac.go:24-32`) is excellent and consistently applied across handlers.
- `ids` package cleanly separates entropy classes; comments explain the *why* (spec entropy requirements) not the *what*. Good.
- **Nit — comment typos:** `internal/images/process.go:158` "unparesable" → "unparseable"; `internal/web/templates/templates.go` is clean. Trivial.
- **Nit — `parseBool` reads the env var by key rather than taking a value** (`config.go:93`), coupling it to `os.Getenv` and making it awkward to unit-test in isolation. The rest of `Load` reads values into locals first; `parseBool` is the odd one out. Minor consistency point.

---

## Consistency across handlers/models — positive

- Every model read path uses the same `errors.Is(err, sql.ErrNoRows) → ErrXNotFound` shape and package-prefixed wrapping. Very consistent.
- Write serialization through `db.Write` / `db.ExecWrite` is used uniformly; no raw `pool.Exec` writes leaked into models. Good.
- CSRF + form parsing is centralized in `checkForm` and used everywhere except the two multipart upload handlers (`ContactPhotoUpload`, `ImportUpload`), which correctly can't use it because they must `ParseMultipartForm` first — and they do verify CSRF explicitly afterward. Consistent and correct.

---

## Summary of findings by severity

| Severity | Count | Items |
|---|---|---|
| High | 1 | Silent `action` fallback in `ImportCommit` |
| Medium | 6 | Swallowed housekeeping errors; duplicated null-helpers; duplicated time-format const; dead `ContactInput` legacy fields; `Details()`/`toDetails()` alias; whole-book scan for one-contact export |
| Low | ~9 | `LoadUser` error masking; dropped `rbac.Can`/photo errors; handler-preamble duplication; CSV-row dup; `DefaultParams` var; `render` map mutation; contextKey style; range-over-int; comment typo; `parseBool` coupling |

No Critical maintainability findings.
