# Skrá — Future Changes & Extensions

Candidate changes beyond the baseline specification (`skra-baseline-spec.md`).
Unlike the baseline, these are **options with recommendations**, not fixed
decisions. Topics: password-hash salting/peppering, allowing a contact
to exist in multiple address books, full-text contact search,
dependency-update tooling, contact de-duplication, and CSV import.

---

## 1. Password hashing: salt and pepper

### Important clarification first

The baseline already uses **argon2id**, which **generates a unique random salt
per password automatically** and embeds it in the encoded hash. If the
implementation stores the standard PHC-format string, salting is already handled
correctly — there is no separate salt to "add." A PHC string looks like:

```
$argon2id$v=19$m=65536,t=3,p=4$<base64-salt>$<base64-hash>
```

It encodes the algorithm, version, parameters, **salt**, and derived hash in one
string stored in `users.password_hash`.

So the real work under this heading is (a) making sure salting is done right, and
(b) optionally adding a **pepper** for defense-in-depth.

### 1a. Verify salting is correct (no schema change)

Requirements for the implementation:
- Generate the salt from a CSPRNG, **≥16 bytes**, **unique per password**, never reused.
- Store the **full PHC-encoded string** in `password_hash` — never a bare digest, never a hand-rolled `hash(salt + password)`.
- Choose argon2id parameters deliberately (e.g. memory 64 MB, time 3, parallelism set to cores) and tune for the target hardware.
- On successful login, if the stored parameters are weaker than current policy, transparently **rehash and update** (see migration notes).

If these hold, per-user salting is complete. An explicit `password_salt` column
is **not recommended** — it duplicates what the PHC string already carries and
invites bare-hash mistakes. (Only introduce one if you switch to a scheme whose
output does not embed the salt, which argon2id does not require.)

### 1b. Optional: add a server-side pepper (defense-in-depth)

A **pepper** is a secret key that is *not* stored in the database — it lives in
config/secret storage. It means a database-only leak is insufficient to mount
offline cracking, because the attacker also needs the pepper.

Practical approach with the common `golang.org/x/crypto/argon2` API (which has no
secret-key parameter): pre-hash the password with an HMAC keyed by the pepper,
then argon2id the result.

```
peppered = HMAC_SHA256(key = SKRA_PASSWORD_PEPPER, msg = password)
stored   = argon2id(peppered, random_salt, params)   // PHC-encoded
```

- Keep `SKRA_PASSWORD_PEPPER` in env/secret (e.g. `EnvironmentFile`), **never in the DB**, and back it up separately — losing it invalidates all passwords.
- Add a new config key: `SKRA_PASSWORD_PEPPER`.

### 1c. Scheme versioning & migration

Password storage can't be re-derived without the plaintext, so any change to the
scheme (new params, adding a pepper, changing algorithm) can only be applied
**at next successful login**.

Plan for it by versioning the stored value:
- Add a small scheme tag so verification knows how to check an old credential and whether to upgrade it. Either a dedicated column `password_scheme INTEGER NOT NULL DEFAULT 1`, or a short prefix on `password_hash` (e.g. `v2$...`).
- On login: detect the stored scheme → verify with the matching method → on success, if scheme < current, rehash with the current scheme and overwrite. Gradual, zero-downtime migration; no mass reset.

Schema delta (optional, if using a column):

```sql
ALTER TABLE users ADD COLUMN password_scheme INTEGER NOT NULL DEFAULT 1;
```

**Effort/risk:** low. 1a is verification only; 1b/1c are additive and migrate
lazily on login.

---

## 2. Contacts in multiple address books

### Current model (baseline)

A contact belongs to exactly one book: `contacts.address_book_id` is a single FK,
with `UNIQUE(address_book_id, uid)`. A contact cannot appear in two books.

Two ways to lift this, in increasing order of effort and correctness.

---

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

---

### Option B — Data-model change (recommended for true shared contacts): many-to-many

Make membership a relationship. A contact has a **home/owner book** (for
permissions and lifecycle) and may additionally appear in other books via a join
table. This is the hybrid M:N model, which is cleaner than a pure free-floating
M:N because every contact still has a clear owner for authorization and deletion.

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

**Migration (SQLite specifics):** adding the join table and `INSERT INTO
contact_books SELECT id, address_book_id, NULL, datetime('now') FROM contacts;`
is straightforward. Renaming `address_book_id`→`owner_book_id` and changing the
UNIQUE constraint requires the standard SQLite **table-rebuild** pattern (create
new `contacts` table with the new shape, copy rows, drop old, rename) inside a
transaction with `PRAGMA foreign_keys=OFF` during the rebuild. `modernc.org/sqlite`
bundles a recent SQLite, so `RENAME COLUMN`/`DROP COLUMN` exist, but a UNIQUE
change still needs the rebuild. **Snapshot the DB before running it** (the
baseline's pre-migration backup covers this).

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

Read access becomes the **union** over the contact's book memberships; write/edit
is anchored to the **owner book** (one clear authority for edits avoids
ambiguity).

**Deletion semantics:** distinguish two actions — *remove from this book*
(delete the `contact_books` row) versus *delete the contact* (remove all
memberships + the row + its photo). Rule: a contact must belong to ≥1 book;
removing it from its last book deletes the contact (or reassigns to an owner's
"unfiled" book if you add one).

**Benefits that fall out of B:**
- One contact = one `vcard_raw` and **one photo blob** — no duplication, true single source of truth (resolves Option A's photo bloat).
- Import de-duplication can now **link** an existing contact into another book instead of copying it.

**CardDAV implications (still deferred, but design now):**
- CardDAV already permits the same vCard `UID` to appear in multiple collections, so the M:N model maps cleanly: each book is a collection; a shared contact appears as a resource in each book it belongs to, with the same `uid` and the same per-contact `etag`.
- Sync tokens are per-collection, so editing a shared contact bumps sync state in every book it belongs to — correct, just more bookkeeping. Keeping `etag` per-contact (not per-membership) is the right choice.

**Trade-offs:**
- Correct shared-contact semantics, no duplication, better import behavior.
- Higher effort and risk: touches the schema (rebuild migration), the `can()` resolver, deletion flows, list queries (join via `contact_books`), and the future CardDAV mapping.

---

### Recommendation

- If the requirement is genuinely "the same contact lives in several books and edits should stay in sync," choose **Option B** — and do it **before** there is much production data, since the rebuild migration is cheaper on a small database and the duplication habits of Option A are awkward to reconcile later.
- If cross-filing is rare and copies-that-diverge are acceptable, **Option A** ships immediately with near-zero risk and can be added now; you can still migrate to B later.
- Avoid a half-measure where Option A duplicates are manually kept in sync — that is the worst of both.

---

## 3. Full-text contact search (SQLite FTS5)

The baseline search is a simple `LIKE` filter over the structured columns (`full_name`, `org`, `primary_email`, `primary_phone`), backed by the existing indexes. That is adequate for small books and exact substring matching, but it does not rank results, does not tokenize, and `LIKE '%term%'` cannot use an index (full scan per query).

When search quality or volume warrants it, add an **FTS5 virtual table** as an external-content index over `contacts`:

- Create `contacts_fts USING fts5(full_name, org, primary_email, primary_phone, content='contacts', content_rowid='id')`. External-content mode stores only the index, not a copy of the data — the contact row stays the single source of truth.
- Keep it in sync with `AFTER INSERT/UPDATE/DELETE` triggers on `contacts` (the standard FTS5 external-content trigger pattern).
- Query with `MATCH` and order by `bm25(contacts_fts)`; support prefix queries (`term*`) for type-ahead.
- `modernc.org/sqlite` bundles FTS5, so no build changes are needed.

Effort/risk: low–medium. It is additive (new virtual table + triggers in a migration, swap the search query), needs no schema change to `contacts`, and can be introduced whenever the simple search becomes a limitation. Consider adding it together with the multi-book change (§2) so the search join accounts for `contact_books` membership.

## 4. Dependency updates: migrate Dependabot → Renovate

Dependabot covers the manifest ecosystems (`gomod`, `github-actions`, `docker`) but cannot track the hand-vendored frontend assets — `htmx.min.js` and the Space Grotesk WOFF2 files — because they have no package manifest, and adding a `package.json` purely to satisfy it would reintroduce the npm supply-chain surface we deliberately avoid. Those assets are therefore updated by hand today (see the README "Vendored assets" section).

Renovate closes that gap: its **custom/regex manager** plus **custom datasources** can watch arbitrary files and version strings, so one tool can cover everything in this repo.

- Keep the Go modules, GitHub Actions, and Dockerfile coverage Dependabot already provides.
- Add a regex manager that matches the vendored versions where they live (the version table in `README.md`, the version comment in `internal/web/static/static.go`, and/or the versioned filenames) and resolves the latest upstream via the `github-releases` datasource (`bigskysoftware/htmx`, `floriankarsten/space-grotesk`). Renovate then opens a PR when a new release appears.
- Note that Renovate only bumps the recorded version string; actually replacing the vendored binary files (and re-subsetting the font) still needs the manual step the README documents, unless paired with a small CI job that downloads and commits the new files. Decide whether the version-bump PR alone is enough as a notification, or whether to automate the file refresh.

Replace `.github/dependabot.yml` with `renovate.json` (or run the Renovate GitHub App / a self-hosted `renovate` action). Effort/risk: low — configuration only, no application code.

## 5. Contact de-duplication

Import already de-duplicates at ingest time (by UID, then email), but duplicates still accumulate from manual entry, merges of address books, and imports run with the "import everything as new" option. A maintenance feature would find and merge existing duplicates within a book (and, once contacts can span books, across the books a user manages).

Shape:

- **Detection:** group candidates by strong keys first (exact email, exact normalized phone, exact `UID`), then optionally surface weaker matches (case/diacritic-normalized full name, or name + organization). Present candidate groups for review rather than auto-merging.
- **Merge UI:** for a chosen group, pick the surviving contact and combine fields — union the emails/phones/addresses/URLs, keep one photo, preserve the longest/most-complete `NOTE` — then rewrite the survivor's `vcard_raw` + denormalized columns, bump its `etag`, move or revoke the losers' share links, and delete the losers.
- **Safety:** dry-run preview of what merges into what; everything in one transaction; never merge across address books the user cannot manage.

This pairs naturally with the rich-contact-fields work (a merge needs the full multi-value field model to union correctly) and with the multi-book change in §2 (cross-book de-duplication becomes "link the same contact into multiple books" rather than copy). Effort/risk: medium — detection heuristics plus a careful merge transaction; no schema change for the within-book case.

## 6. CSV import

Phase 5b shipped vCard import; CSV is the natural follow-up (baseline spec §8). The staging/preview/commit pipeline is already format-agnostic — `import_uploads.format` exists, and `Analyze`/`ImportContacts` work on normalized records — so CSV only needs a parser that yields the same records.

Shape:

- **Header detection:** recognize the common exporters by their header signature — Google Contacts (`Name`, `Given Name`, `E-mail 1 - Value`, …), Outlook (`First Name`, `Last Name`, `E-mail Address`, …), and a generic fallback. Map known headers to the rich `vcardio.Details` fields (multiple emails/phones become multiple typed values).
- **Column mapping:** for an unrecognized header row, present a mapping step (each CSV column → a Skrá field or "ignore") before the preview. The mapping is part of the staged upload so the commit re-applies it.
- **Encoding:** handle UTF-8 + BOM, and Latin-1; detect the delimiter (comma/semicolon/tab).
- **Injection safety:** values are read as literal text; the export sanitizer (leading `= + - @`) already protects round-trips.
- **De-duplication:** reuse the existing UID/email matching and the skip / import-as-new actions.

Effort/risk: medium — the parser and header maps plus a mapping UI; no schema change (the staging table and commit path already exist).

## 7. Consistency with baseline constraints

When implementing either topic, keep the baseline's non-negotiables intact:

- Only `public_id` is exposed externally — new duplicate/linked contacts and any new endpoints must mint and use `public_id`, never internal ids.
- Authorization stays server-side via the (now-updated) `can()`; preserve 404-not-403 for resources a user may not know exist.
- Preserve `vcard_raw`, `uid`, `etag`, `updated_at` — Option B specifically depends on `uid`/`etag` being per-contact for the CardDAV on-ramp.
- Photos remain BLOBs in SQLite; Option B's single-blob-per-contact is the preferred end state.
- Any password-scheme change migrates lazily on login; never force a mass reset, never store the pepper in the database.

---

## 8. Summary

| Change | Schema impact | Effort / risk | Recommendation |
|---|---|---|---|
| Verify argon2id salting (1a) | none | very low | Do it — it's verification |
| Add pepper + scheme versioning (1b/1c) | optional 1 column | low, lazy migration | Add if a DB-leak threat model warrants it |
| Multi-book via move/duplicate (A) | none | low | Fine for occasional cross-filing |
| Multi-book via M:N join (B) | join table + contacts rebuild | medium | Choose for true shared contacts; do it early |
| Full-text search via FTS5 (§3) | new virtual table + triggers | low–medium | Add when `LIKE` search becomes a limitation |
| Dependabot → Renovate (§4) | config only (no app code) | low | Do it to also track the vendored htmx/font versions |
| Contact de-duplication (§5) | none (within-book) | medium | Add after rich fields; pairs with the multi-book change |
| CSV import (§6) | none (staging exists) | medium | Follow-up to the vCard import; needs a parser + mapping UI |
