# Architecture & Data Model Review

Scope: package layering, coupling, domain modeling, schema/migrations, RBAC + sharing, ID design, and alignment with `docs/00_skra-baseline-spec.md` / `docs/00_skra-future-changes.md`.

## Overall assessment

The architecture is coherent, disciplined, and closely faithful to the baseline spec. Layering is clean in the direction that matters most: HTTP handlers never write SQL; every statement lives in `internal/models`, and every authorization decision routes through `internal/rbac`. The two-tier ID scheme, hybrid contact storage, write-serialization discipline, and migration/backup story all match §3–§6 of the spec. The schema is well-normalized with sensible cascade behavior and indexing.

The weaknesses are mostly of the "will bite later" kind rather than present defects: the persistence layer is a package of free functions rather than an interface (hard to mock, and it hard-couples `models` to the concrete `*db.DB`), one polymorphic FK (`share_links.target_id`) is unconstrained and un-cascaded, there is no automated GC for `sessions` or `import_uploads`, and the domain types are largely anemic (invariants enforced procedurally in constructor functions rather than in the types). None of these block the roadmap; the M:N multi-book change (future §2) is the one item the current shape will make genuinely expensive.

Architectural health: **good**. Solid separation and spec-fidelity; the main debt is a missing repository abstraction and a few schema-level integrity gaps.

## Top findings

- **High** — `share_links.target_id` is a polymorphic FK with no foreign-key constraint and no cascade; deleting a book/contact orphans its share links (`0001_init.sql:71`).
- **High** — No automated garbage collection for expired `sessions` or stale `import_uploads`; unbounded growth relies on manual/opportunistic deletes (`0001_init.sql:83`, `import.go:65`).
- **Medium** — Persistence is a flat package of free functions over a concrete `*db.DB`, not a repository/service interface; handlers depend directly on `models`, so there is no seam for testing, transactions spanning models, or a future datastore swap (`models/*.go`, all handlers).
- **Medium** — Domain models are anemic: invariants (valid role, valid access level, gated-share-needs-secret) are enforced in free functions, not the types; a `Contact`/`ShareLink`/`User` value can be constructed in an invalid state (`models/contact.go:22`, `models/share_link.go:21`).
- **Medium** — `expires_at` and all timestamps are stored/compared as text in a hand-matched format; `ShareLink.Usable` parses with a hardcoded layout, which is fragile and diverges from typed time handling (`share_link.go:17`, `share_link.go:155`).
- **Low** — Business validation is split between `models` and handlers (e.g. share form parsing in the handler, secret-required invariant in the model), so the same rule is asserted in two layers.

---

## Layering & dependency direction

Overall the dependency graph is clean and acyclic: `web/handlers` → {`models`, `rbac`, `sharing`, `auth`, `images`, `importing`, `export`, `vcardio`}; `models` → {`db`, `ids`, `vcardio`, `sharing`}; `rbac` → {`db`, `models`}; `db`, `ids` are leaves. No SQL appears outside `models`.

- **Medium — No repository/service boundary.** `internal/models` is a package of package-level functions each taking `ctx, *db.DB, ...` (`models/contact.go:89`, `models/user.go:38`, `models/address_book.go:35`). There is no interface, so handlers are statically bound to the concrete implementation and to `*db.DB`. Consequences:
  - Handlers cannot be unit-tested against a fake persistence layer; every handler test needs a real SQLite file.
  - Multi-model operations cannot share a transaction — each `models.*` call opens its own `ExecWrite`/`Write`. For example `ImportCommit` (`handlers/import.go:113`) re-parses, re-analyzes, and inserts across separate calls; only the final `ImportContacts` batch is atomic, while the preceding `ExistingContactKeys` read is outside it (acceptable here, but the pattern offers no way to compose).
  - The future M:N contact change (`docs/00_skra-future-changes.md:119`) will need `can()` to union over memberships and list queries to join `contact_books`; with free functions this is a wide, untyped refactor rather than swapping a repository implementation.
  This is a deliberate, spec-endorsed "functions over `*db.DB`" style (the spec's layout §16 lists `models/` as data-access), so it is a trade-off, not a bug — but it is the single biggest limiter on testability and future refactors.

- **Low — `rbac` depends on `models` for both storage and constants.** `rbac.Can` calls `models.GetGrant` and compares against `models.AccessViewer/Manager` (`rbac/rbac.go:45`, `rbac/rbac.go:59`). The *pure* decision (`Evaluate`) is nicely separated and independently testable, but the access-level vocabulary lives in `models`, so `rbac` and `models` are mutually aware of the same enum. Minor; acceptable for the size.

- **Low — `models` imports `sharing`.** `models/share_link.go:11` pulls `sharing` for mode/scope constants and token generation, while `sharing` stays DB-free (`sharing/sharing.go:4`). The direction is fine (the DB layer depends on the pure primitives), but it means "what is a valid mode" is owned by `sharing` while "a gated link must have a secret" is owned by `models` (`share_link.go:57`) — the share invariants are split across two packages.

## Domain modeling

- **Medium — Anemic models.** The structs are plain data holders; correctness is enforced by the functions that build them, not by the types:
  - `User.Role` is a bare `string`; validity is checked only in `CreateUser`/`UpdateUser` (`models/user.go:39`, `models/user.go:121`). A `User{Role: "root"}` is representable.
  - `Member.AccessLevel` / grants use bare strings validated in `AddOrUpdateMember` (`models/grant.go:78`).
  - `ShareLink.Mode`/`Scope` are strings; the gated-needs-secret and only-gated-carries-secret invariants live in `CreateShareLink` (`models/share_link.go:57`-`62`), not the type.
  There is no defined-type (`type Role string`, `type AccessLevel string`) with a validating constructor, so the compiler cannot help and every write path must remember to re-validate. Given the user's stated preference ("Never use default values. Throw exceptions if situations are unclear."), the code does reject bad input — but at the function boundary rather than the type boundary. `ShareLink.Usable(now)` (`share_link.go:145`) is a good example of behavior living on the type; more of the model could follow it.

- **Low — `ContactInput` carries legacy convenience fields.** `ContactInput` mixes the rich model (name components, typed emails/phones, addresses) with legacy `FullName`/`PrimaryEmail`/`PrimaryPhone` folded in when the rich fields are empty (`models/contact.go:52`, `models/contact.go:77`). This is pragmatic for tests/simple callers but means two ways to express the same data; a single canonical input shape would be cleaner now that the rich form exists.

- **Low — Two `nullString` helpers.** `nullString` (`address_book.go:171`) and `nullableString`/`nullableInt` (`share_link.go:184`) do the same NULL-coalescing job with different empty-check semantics (`nullString` trims, `nullableString` does not). Minor duplication with a subtle behavioral difference worth unifying.

## Database schema & migrations

Normalization and cascades are largely correct and match the spec's schema verbatim. Photos are isolated (`contact_photos`, `0001_init.sql:46`), `has_photo` keeps list queries off the BLOB table, and `owner_id` uses `ON DELETE RESTRICT` (blocking user deletion while books exist) which `DeleteUser`/`OwnsAddressBooks` surface deliberately (`models/user.go:135`, `models/user.go:152`).

- **High — `share_links.target_id` has no FK and no cascade.** It is a polymorphic reference (book id when `scope='book'`, contact id when `scope='contact'`) with only `target_id INTEGER NOT NULL` (`0001_init.sql:71`). Because it cannot be a real FK, deleting a book or contact leaves dangling share links. The app compensates at read time — `shareTargetError` treats a missing target as 404 (`handlers/public.go:302`) — so it is not exploitable, but rows accumulate and the DB cannot enforce integrity. Consider either a cleanup on book/contact delete, or splitting into `book_share_links` / `contact_share_links` with real FKs (also improves the future CardDAV/M:N story).

- **High — No automated GC for `sessions` or `import_uploads`.** `sessions` has `expires_at` but nothing prunes expired rows on a schedule (`0001_init.sql:83`); `import_uploads` is only purged opportunistically when the *next* upload happens (`DeleteStaleImportUploads` is called at the top of `ImportUpload`, `handlers/import.go:45`, `models/import.go:65`). A user who imports once and never again leaves the staged BLOB (up to 10 MiB) indefinitely; expired sessions grow unbounded. The spec calls out disk space as the #1 operational signal (§13), so a periodic sweeper (goroutine or CLI/cron) is warranted.

- **Medium — Timestamps are text with hand-matched formats.** All `created_at`/`updated_at`/`expires_at` are `TEXT DEFAULT (datetime('now'))`. `ShareLink.Usable` parses `expires_at` with a hardcoded `"2006-01-02 15:04:05"` layout and bails to unusable on any parse error (`share_link.go:17`, `share_link.go:155`-`159`). This works only because the app both writes (`handlers/shares.go:202`) and reads with the exact same format. It is fragile: any row written with a different precision (e.g. fractional seconds, or a timezone suffix from a manual edit/import) silently makes the link permanently unusable. A typed time boundary in `models` (parse once on read, store as `time.Time`) would remove the string-format coupling.

- **Low — `contacts.uid` is `NOT NULL` but empty string is allowed.** The dedup logic treats `uid = ""` specially (`importing/analyze.go:30`, `models/import.go:91`), and `UNIQUE(address_book_id, uid)` would collide on a second empty uid. In practice `CreateContact`/`ImportContacts` always generate a uid (`contact.go:99`, `import.go:124`), so an empty uid never reaches the table — but the schema does not forbid it (`CHECK (uid <> '')` would). Defense-in-depth only.

- **Low — `share_links` lacks an index on `(scope, target_id)`.** `ListShareLinksForTarget` filters on `scope = ? AND target_id = ?` (`share_link.go:96`) and is also the ownership check in `revokeShare` (`handlers/shares.go:151`), but only `token` is indexed (`0001_init.sql:81`). Volume is low so this is negligible today; note it if share links grow.

- **Migration runner: robust.** `migrate.go` is well-designed — per-migration transactions (`migrate.go:107`), version+name recorded in `schema_migrations`, duplicate-version and malformed-filename rejection (`migrate.go:145`, `migrate.go:164`), and a pre-migration `VACUUM INTO` snapshot when anything is pending (`db.go:104`). Two observations:
  - **Low — Migrations are not idempotent by content, only by version.** `0001_init.sql` uses bare `CREATE TABLE` (not `IF NOT EXISTS`), which is correct given the version guard, but it means an out-of-band partial apply cannot be re-run. This is the standard trade-off and fine; the transaction-per-migration guarantees all-or-nothing.
  - **Low — `auto_vacuum` freshness check is size-based.** `isFreshDatabase` treats a zero-length file as fresh (`db.go:171`). A pre-created empty file works; a non-empty non-schema file would skip `initAutoVacuum` and never get INCREMENTAL auto_vacuum. Acceptable for the deployment model.

## RBAC & sharing model

- The RBAC core is the strongest part of the design. `Evaluate` is a pure function of `(isAdmin, level, hasGrant, action)` returning a `Decision{Allow, Visible}` (`rbac/rbac.go:36`), and the `Visible` flag cleanly encodes the spec's 404-not-403 rule (§6). Handlers consistently funnel through `authorizeBook`/`authorizeContact`, which map `!Visible`→404 and `!Allow`→403 (`handlers/books.go:182`, `handlers/contacts.go:271`). Contacts/photos/exports inherit book permission exactly as the spec mandates (§5) — e.g. `authorizeContact` checks the contact's `AddressBookID` (`contacts.go:265`).

- **Medium — The permission model is coherent but not very extensible.** `Action` is a two-value enum (`Read`, `Write`) (`rbac/rbac.go:17`) and access levels are two strings. This directly expresses the spec's viewer/manager split, but there is no room for finer actions (e.g. "manage members" vs "manage shares" vs "edit contacts", all currently collapsed into `Write`) or additional roles without touching the `Evaluate` switch. The future roadmap's richer profiles and multi-book changes (`docs/00_skra-future-changes.md:155`) will require reworking `can()` to take a *contact* (union over books) rather than a single book id — the current `Can(user, addressBookID, action)` signature (`rbac/rbac.go:55`) is book-centric and will need a second entry point. Adequate for today; plan a `CanContact` variant when M:N lands.

- **Low — Sharing invariants are split across layers.** `sharing` owns `ValidMode`/`ValidScope`/`NewToken` and the gate cookie (`sharing/sharing.go`, `sharing/gate.go`); `models.CreateShareLink` owns the "gated needs secret / only gated carries secret" rules (`share_link.go:57`); the handler owns form-level validation like "gated requires a secret field" and max-uses/expiry parsing (`handlers/shares.go:178`-`208`). The secret-required rule is therefore asserted twice (handler and model), which is safe (defense-in-depth) but means the canonical location of a share invariant is ambiguous.

- **Low — Gate throttling is coarse but spec-aligned.** `GateMaxFailures = 10` locks a link until manual revoke/recreate (`sharing/sharing.go:29`, `share_link.go:152`). There is no time-decay, so a legitimate user who fatfingers 10 times permanently bricks the link. Matches the spec's "throttle via failed_count" (§7) but a decay window would be friendlier.

## ID design

- **Good.** `internal/ids` is a clean leaf: crypto/rand, URL-safe base64 without padding, distinct entropy classes (128-bit public_id, 256-bit session/CSRF, 160-bit public_long via `Random(20)`) (`ids/ids.go:14`, `sharing/sharing.go:32`). This satisfies the spec's ≥128-bit public_id and ≥160-bit public_long requirements (§6, §7). Internal integer ids never leave `models`/DB, and every external route resolves `public_id → id` before `can()` (verified across handlers). The `uid`/`etag` are minted via `ids.Random(16)` (`contact.go:99`), preserving the CardDAV on-ramp.

- **Low — `uid`/`etag` are random tokens, not content-derived.** `etag` is a random 16-byte value regenerated on every edit (`contact.go:141`) rather than a content hash. This is spec-compliant ("bumps on every edit") and fine for CardDAV, but note the *photo* etag is a content hash (`contact.go:377`) while the *contact* etag is random — a minor inconsistency in etag semantics across the two.

## Spec ↔ implementation consistency

Strong alignment overall. Verified matches to the non-negotiables (§18): CGO-free driver, SQLite-only with BLOB photos, server-side `can()` on every route with 404-not-403, `public_id`-only exposure, gated-requires-secret and high-entropy public_long, `VACUUM INTO` backups with pre-migration snapshot, and preserved `vcard_raw`/`uid`/`etag`/`updated_at`. Security headers, CSP, and `/s/*` route-pattern logging are wired in `server.go:130`, `server.go:144`.

- **Low — Spec lists `sessions` cleanup implicitly; implementation has none.** Covered above (no session GC); the spec's monitoring section leans on it (§13) but the schema/app do not implement pruning.

## Extensibility for the roadmap

- **Multi-book (future §2, Option B):** the current single-FK `contacts.address_book_id` with `UNIQUE(address_book_id, uid)` (`0001_init.sql:29`, `:40`) is exactly what the roadmap flags as needing a table rebuild. The free-function persistence layer means the `can()` rework and every contact list query change ripple by hand. **Medium** friction — anticipated by the docs, but the missing repository seam makes it larger than it needs to be.
- **FTS5 search (§3):** additive; the `LIKE` search in `ListContacts` (`contact.go:227`) can be swapped for a `MATCH` query behind the same function signature with low risk.
- **Birthdays (§9):** requires a denormalized `birthday` column + backfill, mirroring the existing `primary_email` denormalization pattern (`contact.go:116`) — the architecture supports it cleanly.
- **CSV import (§6):** the pipeline is already format-agnostic — `import_uploads.format` exists (`0002_import_uploads.sql:8`) and `Analyze`/`ImportContacts` work on normalized records (`analyze.go:21`, `import.go:118`) — so a new parser slots in without touching the write path, as the spec's §8 pipeline promised. Good.
- **Theming / i18n / a11y (§10–§13):** presentation-layer only; no data-model impact.

The write-serialization design (`db.Write`/`ExecWrite` behind `writeMu`, `db.go:122`) and single-writer discipline are correctly implemented and match the spec's §3 caveat that this keeps the app single-node — an acknowledged, documented ceiling rather than a defect.
