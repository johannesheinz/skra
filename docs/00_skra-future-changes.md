# Skrá — Future Changes & Extensions

Candidate changes beyond the baseline specification (`skra-baseline-spec.md`).
Unlike the baseline, these are **options with recommendations**, not fixed
decisions. Topics: password-hash salting/peppering, allowing a contact
to exist in multiple address books, full-text contact search,
dependency-update tooling, contact de-duplication, CSV import,
OpenStreetMap links for addresses, a responsive/mobile layout,
upcoming birthdays on the landing page, light/dark theming, richer
user profile pages, accessibility, and internationalization.

---

## 1. Password hashing: salt and pepper — won't fix

Decision: **won't fix.** Salting (§1a) is already handled correctly by argon2id via the stored PHC string, so there is nothing to add there. The optional pepper (§1b) and scheme versioning (§1c) are deliberately declined: a pepper's value hinges on keeping a secret out of the database, but this app ships as a single binary beside its SQLite file on the same host, so a compromise that reads the DB almost certainly reads the pepper too — it adds key-management burden and a rehash/rollout path for little real gain here. Revisit only if the deployment model changes (e.g. secret stored in an HSM/KMS separate from the DB).

Original analysis retained below for reference.

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

## 4. Dependency updates: migrate Dependabot → Renovate — implemented

Shipped: `.github/dependabot.yml` is replaced by [`renovate.json`](../renovate.json). It extends `config:recommended` on a weekly schedule with a dependency dashboard, keeping the gomod / github-actions / docker coverage, and adds regex custom-managers that read the README "Vendored assets" table and bump the recorded versions: htmx and Space Grotesk from `github-tags`, and Lucide from the `lucide-static` npm package (which is where its recorded version originates, per `internal/web/icons/icons.go`). Renovate only edits the version string; the vendored binaries, the font subset, and the inlined icon SVGs are still refreshed by hand — each PR carries a `prBodyNotes` warning naming the exact repo path to update. Enabling it is an ops step: install the Renovate GitHub App on the repo, or run the self-hosted `renovatebot/github-action`.

Original notes retained below for reference.

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

## 7. Map links for addresses — implemented

Shipped: each address on the contact detail page carries three outbound link-outs, built from `Address.SingleLine()` (a comma-joined single line of the non-empty components) and percent-encoded by `html/template` in the query position:

- **OpenStreetMap** — `https://www.openstreetmap.org/search?query=<address>`
- **Google Maps** — `https://www.google.com/maps/search/?api=1&query=<address>`
- **Directions** — `https://www.google.com/maps/dir/?api=1&destination=<address>` (origin omitted, so Google uses the visitor's current location)

All open in a new tab with `rel="noopener noreferrer"`; combined with the app-wide `Referrer-Policy: no-referrer`, no page URL (and thus no `public_id`) leaks. Nothing is embedded — these are plain navigations, so the self-only CSP is untouched. Privacy note: following a link discloses the address to that provider; the Google links disclose to Google, which is more tracking-prone than OSM, so the links are rendered small and unobtrusive.

The `Region` field was dropped from the address model entirely (struct, form, vCard round-trip, display) per product decision; addresses are now street / city / postal / country.

Tile embedding is evaluated but not implemented — see the constraint below (kept from the original note): an embed loads third-party resources on every view and would breach the CSP / local-first stance. If ever wanted it must be opt-in with a self-hosted tile server or an explicit per-deployment CSP relaxation.

Original notes retained below for reference.

Now that contacts carry structured postal addresses (the rich-fields work), each address on the detail page could offer a "View on map" action linking to OpenStreetMap, e.g. `https://www.openstreetmap.org/search?query=<url-encoded address>` opened in a new tab. A `geo:` URI (`geo:0,0?q=<address>`) is a nice companion for mobile, handing off to the device's map app.

Important constraint — keep it a link, not an embed:

- An **outbound link** is a plain navigation the user chooses to follow. It does **not** load any third-party resource into Skrá's pages, so it does not breach the self-only CSP or the local-first/no-tracking stance. Use `rel="noopener noreferrer"`; the existing `Referrer-Policy: no-referrer` already prevents leaking the page URL (which may carry a `public_id`) to OpenStreetMap.
- **Do not embed map tiles or an `<iframe>` map** by default — that would load third-party resources on every view, defeat the CSP, and enable tracking. If an embedded map is ever wanted, it requires either an explicit per-deployment CSP relaxation or a self-hosted tile server, and should be opt-in.

Geocoding to real coordinates is out of scope; the link relies on OpenStreetMap's free-text search. Effort/risk: very low — a template link built from the address fields, no schema change. Make it unobtrusive since clicking it does disclose the address to OpenStreetMap.

## 8. Responsive / mobile layout — implemented

Shipped a deliberate small-screen pass on top of the existing fluid base:

- **App bar:** the nav collapses to icons under 40rem (`.nav-label` hidden; every link/button carries an `aria-label`/`title`), and the bar wraps cleanly.
- **Tables:** the user, members, shares, import-preview, membership, and books tables are wrapped in `.table-wrap` (`overflow-x: auto`), so wide tables scroll within their own box instead of pushing the page sideways.
- **Forms:** `.field-row`/address inputs already stack at the breakpoint; inputs are `font-size: 16px` (no iOS zoom).
- **Touch targets:** a `@media (pointer: coarse)` rule raises buttons, selects, file inputs, and nav controls to ~44px.
- **Toolbar:** search takes a full row on phones with the input growing to fill it.

Not done: a full "tables → stacked label/value cards" restyle (kept the scroll-wrapper approach, which is simpler and preserves the tabular reading), and a hamburger menu (icon collapse suffices for the small nav).

Original notes retained below for reference.

The current CSS is already fluid in places (a max-width container, the contact card grid auto-fills, the app bar and toolbars use flex-wrap, the viewport meta tag is set), but there has been no deliberate small-screen pass. A focused responsive phase would make Skrá comfortable on phones.

Work:

- **App bar:** collapse the nav into a compact menu on narrow viewports rather than wrapping; keep the brand and sign-out reachable.
- **Tables → cards:** the user list, members, and share-link tables overflow on phones. Either let them scroll inside an `overflow-x: auto` wrapper or restyle them as stacked rows (label/value) below a breakpoint. (The contact directory already uses cards, so it adapts.)
- **Forms:** the contact edit form's repeatable rows (`.field-row`) and the address row's six inputs should stack cleanly; ensure inputs hit the ~44px touch-target size and `font-size: 16px` so iOS doesn't zoom on focus.
- **Photos/avatars:** cap sizes with relative units; the directory grid's `minmax` already reflows.
- **Breakpoints via tokens:** add a small set of `@media` rules driven by the existing design tokens — no framework, consistent with the hand-written CSS approach.

This is pure CSS plus minor template structure (e.g. wrapping wide tables); no Go, no schema, no new dependency. Effort/risk: low–medium, mostly iteration against real screens. A good companion to the eventual design/colors pass.

## 9. Upcoming birthdays on the landing page — implemented

Shipped: the dashboard shows the next 5 upcoming birthdays across the books the signed-in user may see, with the age each person turns when the birth year is known. Implementation notes:

- Migration `0004` adds a denormalized `birthday` column (indexed on `(address_book_id, birthday)`), stored normalized as `YYYY-MM-DD` with year `0000` for year-less vCard birthdays; empty string means "no birthday", NULL means "not yet backfilled".
- Populated on create/update/import (`models.NormalizeBirthday`); existing rows are backfilled once from `vcard_raw` at startup via the idempotent `models.BackfillBirthdays` (a no-op on later boots).
- Ordering is a scoped SQL query computing the next calendar occurrence (this year, else next); age is derived in Go. Year-less birthdays sort by month-day and show no age. Leap-day is handled loosely (string ordering; no special Feb 29 rule).

Original notes retained below for reference.

The landing page (`/`) is currently a near-empty welcome. It could show the next ~5 contacts with an upcoming birthday, with each person's age, across the books the signed-in user can see — a small, friendly dashboard.

Data-access wrinkle: birthdays live in `vcard_raw` (BDAY), not a structured column, so "soonest birthday across all my contacts" can't be queried efficiently today. Add a denormalized `birthday` column to `contacts` (populated from `Details` on create/update/import, like `primary_email`), ideally with a stored month-day for ordering, so the dashboard query is a simple indexed scan over the user's accessible books rather than parsing every card.

Details to handle:

- **Next occurrence:** order by the next time each birthday falls on/after today (this year, else next year); take the top 5. Optionally bound to a window (e.g. next 30 days) and show nothing when empty.
- **Age:** vCard birthdays may omit the year (`--MMDD`). Show the age only when a year is present; otherwise show just the date / "turning —".
- **Authorization:** include only contacts in books the user may read; reuse the existing access scoping.
- **Leap day:** decide a rule for Feb 29 in non-leap years (treat as Mar 1 or Feb 28).

Effort/risk: medium — a migration plus denormalization (and a backfill of existing rows from `vcard_raw`), one scoped query, and a small home-page widget. No new dependency.

## 10. Theming (light / dark) — implemented

Shipped, and broader than the original sketch: a **Mode / Flavor / Accent** triplet stored per user in the `preferences` JSON blob (`UIPreferences.Theme`). Mode is light / dark / system; Flavor adds a Solarized palette; Accent is a preset from a curated neon/pastel set. The server stamps `data-theme` / `data-flavor` / `data-accent` on `<html>` from the saved preference (no flash, no schema migration beyond the existing `preferences` column). Logged-out visitors get a per-device choice via a self-hosted `theme.js` + `localStorage` (never inline, so `script-src 'self'` holds); logged-in pages are DB-authoritative (`data-theme-managed`) and the header toggle POSTs to persist. Appearance is editable on the account page; the header toggle is a compact contrast-icon button. Default accents are set explicitly per mode/flavor.

Original notes retained below for reference.

The stylesheet already defines both palettes via design tokens and switches on `prefers-color-scheme`, so the app follows the OS today. The addition is an explicit user choice — light / dark / system — that overrides the OS setting.

Approach that keeps the self-only CSP and avoids a flash of the wrong theme:

- Store the preference in a cookie and have the server stamp `data-theme="light|dark"` on the `<html>` element when rendering; the CSS keys off `[data-theme="dark"]` in addition to the existing media query. No JavaScript and no flash, because the correct theme is in the first byte of HTML.
- The toggle is a small control that POSTs to set the cookie (or, for instant switching without a reload, a tiny self-hosted `theme.js` — never inline, to keep `script-src 'self'`).
- Decide cookie-only vs per-user: a cookie is zero-schema and per-device; persisting on the account (a small `theme` column / preference) makes it follow the user across devices but needs a migration. Cookie-first is the low-effort start.

Effort/risk: low. Tokens exist; it's a cookie, a server-set attribute, a couple of CSS rules, and a toggle.

## 11. Richer user profile pages — implemented

Shipped: the account page (`/account`) now has Profile (edit own email; username shown but immutable, with a not-allowed cursor), Appearance (the theming triplet from §10), Memberships (the address books you belong to and your access level, via `ListMembershipsForUser`), and Security (password change). Role stays admin-only. The same membership context is available to admins on the user-edit page. No schema change beyond the existing `email`/`preferences` columns.

Original notes retained below for reference.

Self-service today is limited to changing your own password (`/account/password`). Two gaps:

- **Show memberships/roles.** A user's profile should list the address books they belong to and at what access level (viewer/manager), so people can see what they can reach. The same memberships view belongs on the admin's user-edit page (`/admin/users/{id}/edit`) — answering "what does this user manage?" at a glance. This needs a per-user "books + my access level" query (the data is in `address_book_members`; today only the admin global role and the per-book Members page expose it).
- **Edit more than the password.** Let a user edit their own editable profile info — at least email — on an account page, not only their password. Username stays immutable and role stays admin-only (a user must not promote themselves); admins continue to manage roles via `/admin/users`.

Effort/risk: low–medium — a memberships query plus a small profile form and page; no schema change (the `email` column already exists). Pairs naturally with the per-user theme preference (§10) if profile settings grow.

## 12. Accessibility (a11y)

The server-rendered HTML is a good starting point — semantic elements, `<label>`-wrapped inputs, `role="alert"` on errors, `role="search"`, landmark `header`/`main`/`nav`, `lang="en"`, and visible focus outlines — but there has been no deliberate accessibility pass.

Work:

- **Labels & descriptions:** confirm every control has a programmatic label; associate validation messages with their field via `aria-describedby` rather than only a separate alert paragraph.
- **Keyboard & focus:** ensure a logical tab order, that the htmx "Add row" buttons and all actions are keyboard-operable, and add a "skip to content" link.
- **Dynamic updates:** when an htmx row is appended, move focus to the new input (and/or announce via an `aria-live` region) so it is not silently inserted.
- **Images:** give contact photos meaningful `alt` (the contact's name) instead of empty alt where the image is the only representation.
- **Contrast:** verify the light and dark token palettes meet WCAG AA contrast — naturally paired with the theming/design work (§10).
- **Verify:** run axe/Lighthouse and a screen-reader smoke test; keep it regression-checked.

Effort/risk: low–medium — mostly template and CSS refinements; no new dependency.

## 13. Internationalization (i18n / l10n) — implemented

Shipped. A locale is a **language × region** pair; the language subtag picks the message catalog, the full BCP-47 tag drives formatting.

- **Registry**: `en-US`, `de-DE`, `en-DK` (English text with European formats) — extensible via `internal/i18n`.
- **Package `internal/i18n`**: embedded per-language JSON catalogs (`locales/en.json` is the source of truth, `de.json` fully translated), a `Translator` with `T`/`Tf`/`Plural`/`Num`/`Date`/`ISODate`/`MonthDay`/`TypeLabel`, `Accept-Language` matching and plural categories via `golang.org/x/text` (`language`, `feature/plural`, `message`).
- **Selection**: saved per-user in `UIPreferences.Locale` (no migration); otherwise `Accept-Language`. Resolved once per request by middleware into the context; `<html lang>`/`dir` are set from it. A language selector on the account page auto-submits like the theme control.
- **Templates**: one parsed set per locale with locale-bound `t`/`num`/… funcs (no per-request parsing). All 21 templates + handler flash messages are localized — visible and invisible text (`aria-label`, `title`, `alt`, `<title>`).
- **Formatting**: numbers grouped per locale (`1,500` vs `1.500`), dates per locale (`Jul 1, 2026` / `01.07.2026` / `1 Jul 2026`), and address line order (US `City 12345` vs European `12345 City`).
- **Coverage guard**: a test fails the build if German is missing any English key.

Admin/members/shares handler flash strings are the only bits still English (low-traffic admin surfaces); they degrade to English gracefully. RTL is scaffolded (`dir`) but no RTL locale ships yet.

Original notes retained below for reference.

Every user-facing string is currently hardcoded English in templates and handlers, dates are rendered in a fixed format, and `lang` is always `en`. Making Skrá translatable is a cross-cutting but mechanical change.

Work:

- **Externalize strings:** extract UI text into per-locale message catalogs and look them up by key in templates/handlers (a translation function exposed to `html/template`).
- **Locale selection:** detect from `Accept-Language`, overridable by a user/cookie preference (pairs with the profile/theming settings); set `<html lang>` accordingly.
- **Plurals & formatting:** handle plural rules and localized date/number formatting — `golang.org/x/text` (official) provides catalogs, plural selection, and formatting; weigh it against a small hand-rolled catalog to keep the dependency surface minimal.
- **RTL:** support right-to-left locales via `dir="rtl"`; the CSS already leans on logical properties (`margin-inline`, etc.), which helps. 
- **Scope note:** contact data is user content and is not translated; this covers the chrome (labels, buttons, messages, validation, emails).

Effort/risk: medium–high — it touches every template and adds locale plumbing; best done before the string surface grows much larger.

## 14. Consistency with baseline constraints

When implementing either topic, keep the baseline's non-negotiables intact:

- Only `public_id` is exposed externally — new duplicate/linked contacts and any new endpoints must mint and use `public_id`, never internal ids.
- Authorization stays server-side via the (now-updated) `can()`; preserve 404-not-403 for resources a user may not know exist.
- Preserve `vcard_raw`, `uid`, `etag`, `updated_at` — Option B specifically depends on `uid`/`etag` being per-contact for the CardDAV on-ramp.
- Photos remain BLOBs in SQLite; Option B's single-blob-per-contact is the preferred end state.
- Any password-scheme change migrates lazily on login; never force a mass reset, never store the pepper in the database.

---

## 15. Summary

| Change | Schema impact | Effort / risk | Recommendation |
|---|---|---|---|
| Password salt/pepper (§1) | none | — | 🚫 won't fix — argon2id already salts; pepper adds little on a single-host deploy |
| Multi-book via move/duplicate (A) | none | low | Fine for occasional cross-filing |
| Multi-book via M:N join (B) | join table + contacts rebuild | medium | Choose for true shared contacts; do it early |
| Full-text search via FTS5 (§3) | new virtual table + triggers | low–medium | Add when `LIKE` search becomes a limitation |
| Dependabot → Renovate (§4) | ✅ implemented | low | `renovate.json` also tracks the vendored htmx/font versions |
| Contact de-duplication (§5) | none (within-book) | medium | Add after rich fields; pairs with the multi-book change |
| CSV import (§6) | none (staging exists) | medium | Follow-up to the vCard import; needs a parser + mapping UI |
| Map address links (§7) | ✅ implemented | very low | OSM + Google Maps + directions link-outs (no embed); Region field dropped |
| Responsive / mobile layout (§8) | ✅ implemented | low–medium | Icon-collapse nav, scrollable tables, coarse-pointer tap targets |
| Upcoming birthdays on landing (§9) | denormalized birthday column + backfill | medium | Needs a queryable birthday; then a small dashboard widget |
| Theming light/dark (§10) | ✅ implemented | low | Per-user Mode/Flavor/Accent in preferences; server-set data-* attrs, no flash |
| Richer user profiles (§11) | ✅ implemented | low–medium | Account page: email edit, memberships, appearance, password |
| Accessibility (§12) | none (templates/CSS) | low–medium | a11y pass: labels, focus, aria-live, contrast; verify with axe |
| Internationalization (§13) | ✅ implemented | medium–high | en-US/de-DE/en-DK; x/text formatting; per-user locale; language × region |
