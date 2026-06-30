# Skrá — Ideas & Backlog

Candidate changes that are not yet built. These are options with recommendations, not commitments. For what already ships, see [`features.md`](features.md).

---

## 1. Contacts in multiple address books

### Current model

A contact belongs to exactly one book: `contacts.address_book_id` is a single FK, with `UNIQUE(address_book_id, uid)`. A contact cannot appear in two books.

Two ways to lift this, in increasing order of effort and correctness.

### Option A — Operational only (no schema change): move & duplicate

Add manager/admin actions without touching the data model.

**Move** — reassign a contact to a different book:
- Change `address_book_id` to the target book.
- Requires `manager` on **both** source and target books (or `admin`).
- The contact leaves the source book entirely (this is a move, not sharing).

**Duplicate / copy** — place a copy in another book:
- Create a new `contacts` row: new `id`, new `public_id`, **new `uid`** (uid must be unique per book and is a CardDAV resource id), copying `full_name`/`org`/`vcard_raw`/etc., and copying the photo bytes into a new `contact_photos` row.
- Requires read on the source and `manager` on the target.

Properties and trade-offs:
- Simple, fully reversible, no migration, lowest risk.
- But duplicates are **independent copies** — edits to one do not propagate; there is no single source of truth.
- **Photo bytes are duplicated** (the baseline keys `contact_photos` one-to-one by `contact_id`), increasing DB size.
- "In multiple books" is only approximated.

Use Option A if cross-filing is occasional and divergence is acceptable.

### Option B — Data-model change (recommended for true shared contacts): many-to-many

Make membership a relationship. A contact has a **home/owner book** (for permissions and lifecycle) and may additionally appear in other books via a join table. This is the hybrid M:N model, which is cleaner than a pure free-floating M:N because every contact still has a clear owner for authorization and deletion.

**Schema changes:**

```sql
-- Join table: which books a contact appears in.
CREATE TABLE contact_books (
    contact_id      INTEGER NOT NULL REFERENCES contacts(id)       ON DELETE CASCADE,
    address_book_id INTEGER NOT NULL REFERENCES address_books(id)  ON DELETE CASCADE,
    added_by        INTEGER REFERENCES users(id),
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (contact_id, address_book_id)
);
CREATE INDEX idx_contact_books_book ON contact_books(address_book_id);

-- contacts.address_book_id is renamed to owner_book_id (the contact's home).
-- The old UNIQUE(address_book_id, uid) is replaced by a global UNIQUE(uid):
--   a contact appears at most once per book (the join PK guarantees this),
--   and each contact has exactly one uid, so per-collection uid uniqueness holds.
```

**Migration (SQLite specifics):** adding the join table and `INSERT INTO contact_books SELECT id, address_book_id, NULL, datetime('now') FROM contacts;` is straightforward. Renaming `address_book_id`→`owner_book_id` and changing the UNIQUE constraint requires the standard SQLite **table-rebuild** pattern (create new `contacts` table with the new shape, copy rows, drop old, rename) inside a transaction with `PRAGMA foreign_keys=OFF` during the rebuild. `modernc.org/sqlite` bundles a recent SQLite, so `RENAME COLUMN`/`DROP COLUMN` exist, but a UNIQUE change still needs the rebuild. **Snapshot the DB before running it** (the pre-migration backup covers this).

**Authorization changes (`can()` must be updated):**

```
can_read(user, contact):
    if admin: ALLOW
    return any( grant(user, b) in {viewer, manager}
                for b in contact_books[contact] )

can_write(user, contact):           # edit fields / photo / vcard
    if admin: ALLOW
    return grant(user, contact.owner_book_id) == manager

remove_from_book(user, contact, book):   # detach one membership
    requires grant(user, book) == manager

delete_contact(user, contact):           # remove entirely
    requires admin OR grant(user, contact.owner_book_id) == manager
```

Read access becomes the **union** over the contact's book memberships; write/edit is anchored to the **owner book** (one clear authority for edits avoids ambiguity).

**Deletion semantics:** distinguish two actions — *remove from this book* (delete the `contact_books` row) versus *delete the contact* (remove all memberships + the row + its photo). Rule: a contact must belong to ≥1 book; removing it from its last book deletes the contact (or reassigns to an owner's "unfiled" book if you add one).

**Benefits that fall out of B:**
- One contact = one `vcard_raw` and **one photo blob** — no duplication, true single source of truth (resolves Option A's photo bloat).
- Import de-duplication can now **link** an existing contact into another book instead of copying it.

**CardDAV implications (still deferred, but design now):**
- CardDAV already permits the same vCard `UID` to appear in multiple collections, so the M:N model maps cleanly: each book is a collection; a shared contact appears as a resource in each book it belongs to, with the same `uid` and the same per-contact `etag`.
- Sync tokens are per-collection, so editing a shared contact bumps sync state in every book it belongs to — correct, just more bookkeeping. Keeping `etag` per-contact (not per-membership) is the right choice.

**Trade-offs:**
- Correct shared-contact semantics, no duplication, better import behavior.
- Higher effort and risk: touches the schema (rebuild migration), the `can()` resolver, deletion flows, list queries (join via `contact_books`), and the future CardDAV mapping.

### Recommendation

- If the requirement is genuinely "the same contact lives in several books and edits should stay in sync," choose **Option B** — and do it **before** there is much production data, since the rebuild migration is cheaper on a small database and the duplication habits of Option A are awkward to reconcile later.
- If cross-filing is rare and copies-that-diverge are acceptable, **Option A** ships immediately with near-zero risk and can be added now; you can still migrate to B later.
- Avoid a half-measure where Option A duplicates are manually kept in sync — that is the worst of both.

---

## 2. Full-text contact search (SQLite FTS5)

Search today is a simple `LIKE` filter over the structured columns (`full_name`, `org`, `primary_email`, `primary_phone`), backed by the existing indexes. That is adequate for small books and exact substring matching, but it does not rank results, does not tokenize, and `LIKE '%term%'` cannot use an index (full scan per query).

When search quality or volume warrants it, add an **FTS5 virtual table** as an external-content index over `contacts`:

- Create `contacts_fts USING fts5(full_name, org, primary_email, primary_phone, content='contacts', content_rowid='id')`. External-content mode stores only the index, not a copy of the data — the contact row stays the single source of truth.
- Keep it in sync with `AFTER INSERT/UPDATE/DELETE` triggers on `contacts` (the standard FTS5 external-content trigger pattern).
- Query with `MATCH` and order by `bm25(contacts_fts)`; support prefix queries (`term*`) for type-ahead.
- `modernc.org/sqlite` bundles FTS5, so no build changes are needed.

Effort/risk: low–medium. It is additive (new virtual table + triggers in a migration, swap the search query), needs no schema change to `contacts`, and can be introduced whenever the simple search becomes a limitation. Consider adding it together with the multiple-address-books change so the search join accounts for `contact_books` membership.

---

## 3. Contact de-duplication

Import already de-duplicates at ingest time (by UID, then email), but duplicates still accumulate from manual entry, merges of address books, and imports run with the "import everything as new" option. A maintenance feature would find and merge existing duplicates within a book (and, once contacts can span books, across the books a user manages).

Shape:

- **Detection:** group candidates by strong keys first (exact email, exact normalized phone, exact `UID`), then optionally surface weaker matches (case/diacritic-normalized full name, or name + organization). Present candidate groups for review rather than auto-merging.
- **Merge UI:** for a chosen group, pick the surviving contact and combine fields — union the emails/phones/addresses/URLs, keep one photo, preserve the longest/most-complete `NOTE` — then rewrite the survivor's `vcard_raw` + denormalized columns, bump its `etag`, move or revoke the losers' share links, and delete the losers.
- **Safety:** dry-run preview of what merges into what; everything in one transaction; never merge across address books the user cannot manage.

This pairs naturally with the multi-value contact fields (a merge needs the full model to union correctly) and with the multiple-address-books change (cross-book de-duplication becomes "link the same contact into multiple books" rather than copy). Effort/risk: medium — detection heuristics plus a careful merge transaction; no schema change for the within-book case.

---

## 4. CSV import

vCard import ships; CSV is the natural follow-up. The staging/preview/commit pipeline is already format-agnostic — `import_uploads.format` exists, and `Analyze`/`ImportContacts` work on normalized records — so CSV only needs a parser that yields the same records.

Shape:

- **Header detection:** recognize the common exporters by their header signature — Google Contacts (`Name`, `Given Name`, `E-mail 1 - Value`, …), Outlook (`First Name`, `Last Name`, `E-mail Address`, …), and a generic fallback. Map known headers to the rich `vcardio.Details` fields (multiple emails/phones become multiple typed values).
- **Column mapping:** for an unrecognized header row, present a mapping step (each CSV column → a Skrá field or "ignore") before the preview. The mapping is part of the staged upload so the commit re-applies it.
- **Encoding:** handle UTF-8 + BOM, and Latin-1; detect the delimiter (comma/semicolon/tab).
- **Injection safety:** values are read as literal text; the export sanitizer (leading `= + - @`) already protects round-trips.
- **De-duplication:** reuse the existing UID/email matching and the skip / import-as-new actions.

Effort/risk: medium — the parser and header maps plus a mapping UI; no schema change (the staging table and commit path already exist).

---

## 5. Accessibility auditing in CI (axe / Lighthouse)

The accessibility work is guarded by a dependency-free Go invariant test (`internal/web/a11y_test.go`) that runs in the normal `go test` pass — it checks the structural affordances (labels, `alt`, accessible names, one `<h1>`, `lang`, `<title>`). What a static parse cannot judge is the *computed* ARIA tree, colour-contrast ratios, and performance — the things a real browser-based audit (axe-core, Lighthouse) measures.

Adding such an audit to CI is deliberately **deferred** because it conflicts with the local-first / no-npm principle ([`development.md`](development.md)): axe/Lighthouse/pa11y are Node tools requiring a headless Chrome and an npm dependency tree in CI.

If adopted later, keep it isolated from the build:

- A **separate** GitHub Actions job (never the release build) that: builds the `skra` binary, seeds a demo DB, starts the server, and runs **pa11y-ci** (bundles axe-core + HTML_CodeSniffer) or **Lighthouse CI** against a fixed URL list.
- Authenticated pages need a login step first — pa11y `actions` (type credentials, submit) or a pre-seeded session cookie.
- Treat it as an explicit, documented exception to the no-npm rule, and consider running it on a schedule rather than every push to limit the Node surface.
- Value: catches contrast regressions, ARIA/role mistakes, and performance budgets the Go check can't.

---

## 6. Secondary hardening (carried over from the 1.0 code review)

A five-part code review of 1.0 found **no confirmed critical/remotely-exploitable vulnerability**; its nine High-severity findings were remediated and shipped as **1.1.0** (image pixel cap, background maintenance ticker, streamed exports, real CI coverage + golangci-lint + govulncheck, tests for the privileged/public paths, full server timeouts + body cap, bounded connection pool, rejected unknown import action, and share-link cleanup on delete). These lower-severity items were deferred and are worth scheduling:

- **Login rate limiting / lockout** — `/login` has no throttle, so online brute force is possible and every attempt runs the expensive argon2 hash (a CPU-DoS lever). Add per-IP + per-username throttling and temporary lockout (`internal/web/handlers/auth.go`).
- **Bootstrap-admin password strength** — `skra create-admin` accepts any length while every in-app path enforces ≥8 characters; apply the same minimum (`main.go`, `createAdmin`).
- **Defined-type enums (anemic models)** — roles, access levels, and share modes are bare strings validated procedurally, so an invalid value like `User{Role:"root"}` is representable. Consider named types with validating constructors.
- **Repository/service seam** — `models` is free functions over a concrete `*db.DB`, so handlers can't be tested against fakes and a transaction can't span multiple model calls. This is the main friction against the multiple-address-books change.
- **Text timestamps** — `ShareLink.Usable` parses a hardcoded time layout; a stored value with different precision would silently brick the link (`internal/models/share_link.go`). Store/compare timestamps as a consistent type.
- **Duplicated helpers/constants** — two `nullString`/`nullableString` variants with divergent trim semantics, and duplicated time-format constants across `auth` and `models` that would break session/expiry logic if they desynced. Consolidate.
- **Index on `share_links(scope, target_id)`** — the shares-management lookup full-scans the table; negligible today, cheap to add when it grows.
- **Operational observability** — no container `HEALTHCHECK`, no `/metrics`, a fixed log level, and no access log (`/healthz` + `/readyz` already exist). Add as operational maturity grows.
