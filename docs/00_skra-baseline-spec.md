# Skrá — Baseline Specification

A self-hosted application for storing, managing, and sharing/presenting
contacts. This document fixes every decision from the planning phase and is the
authoritative baseline for implementation. It is self-contained; no other
documents are required.

---

## 1. Overview

**Skrá** (Old Norse / Icelandic for "register, list, record") is a contacts
application inspired by Forgejo: a **single Go binary** that serves both the
backend and the server-rendered frontend, storing **all data in one SQLite
file** (photos included). It targets a **single instance with many users** — not
multi-tenant SaaS — and runs on a plain Linux host behind a reverse proxy.

Fixed top-level decisions:

| Aspect | Decision |
|---|---|
| Name | Display: **Skrá**. ASCII everywhere in code/config/URLs/binary: **`skra`** |
| Language | **Go**, compiled to a single static binary |
| HTTP/router | `net/http` with **chi** |
| Frontend | Server-rendered `html/template` + **htmx**, assets embedded via `embed.FS` |
| Database | **SQLite**, single file, accessed via **`modernc.org/sqlite`** (pure Go, CGO-free) |
| Photos | Stored as **BLOBs inside SQLite** (single source of truth), in a dedicated table |
| Deployment | **Single instance, multi-user** (Forgejo-style); behind a TLS-terminating reverse proxy |
| Auth | Local accounts, **argon2id** hashing, server-side sessions, CSRF |
| Access control | Global role (`admin`/`user`) + **per–address-book grants** (`viewer`/`manager`) |
| External IDs | Random, unguessable `public_id` per externally referenced row |
| CardDAV | **Deferred** to a later phase; schema is pre-shaped to make it cheap to add |
| Font | **Space Grotesk** (Medium / 500) — assumed available |
| Logo assets | `skra-lockup.svg`, `skra-icon.svg`, `skra-favicon.svg`, `skra-wordmark.svg` — assumed available |

Build the static binary with:

```sh
CGO_ENABLED=0 go build -o skra .
```

---

## 2. Technology stack

- **Go** + **chi** router.
- **`modernc.org/sqlite`** — pure-Go SQLite driver; this is the load-bearing choice that keeps the binary static and cross-compilable. Do not switch to a CGO driver.
- **`html/template` + htmx**, CSS (Tailwind compiled at build time or plain CSS). All templates, CSS, JS, and the font files embedded via `embed.FS`.
- **`log/slog`** for structured logging (JSON in production).
- **argon2id** for password hashing.
- Embedded SQL migrations run by a startup migration runner.
- Suggested libraries: `emersion/go-vcard` for vCard parsing; a standard image library for the ingest pipeline.

---

## 3. Data storage

One SQLite file is the only data source, including photo binaries.

### Connection pragmas (apply on every open)

```sql
PRAGMA journal_mode = WAL;      -- concurrent reads with a single writer
PRAGMA busy_timeout = 5000;     -- wait rather than error on brief locks
PRAGMA foreign_keys = ON;       -- enforce FKs (off by default in SQLite)
PRAGMA synchronous = NORMAL;    -- safe and fast under WAL
```

`auto_vacuum` must be set **on the brand-new empty database, before any schema is
created** (it cannot be changed later without a full `VACUUM`):

```sql
PRAGMA auto_vacuum = INCREMENTAL;
```

Reclaim freed pages (e.g. after photo deletes) periodically with
`PRAGMA incremental_vacuum;`.

### Writer discipline

SQLite is single-writer. Serialize writes (a single write connection or a write
mutex) and rely on WAL + `busy_timeout` to avoid `database is locked`. Write
volume for a contacts app is low, so this is sufficient; it does, however, mean
the app is single-node and does not scale horizontally. If the product ever
pivots to true multi-tenant SaaS at scale, that is the point to reconsider
PostgreSQL.

---

## 4. Database schema

Consolidated initial schema (`internal/db/migrations/0001_init.sql`). Integer
`id` columns are internal and never exposed; `public_id` columns are what appear
in URLs and API responses.

```sql
CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    public_id     TEXT NOT NULL UNIQUE,                 -- random >=128-bit base64url
    username      TEXT NOT NULL UNIQUE,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,                        -- argon2id
    role          TEXT NOT NULL CHECK (role IN ('admin','user')),
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE address_books (
    id          INTEGER PRIMARY KEY,
    public_id   TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL,
    description TEXT,
    owner_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_address_books_owner ON address_books(owner_id);

CREATE TABLE contacts (
    id              INTEGER PRIMARY KEY,
    public_id       TEXT NOT NULL UNIQUE,
    address_book_id INTEGER NOT NULL REFERENCES address_books(id) ON DELETE CASCADE,
    full_name       TEXT NOT NULL,
    org             TEXT,
    primary_email   TEXT,
    primary_phone   TEXT,
    has_photo       INTEGER NOT NULL DEFAULT 0,   -- keeps list queries off the photo table
    vcard_raw       TEXT NOT NULL,                -- canonical fidelity + future CardDAV
    uid             TEXT NOT NULL,                -- stable resource id (CardDAV-ready)
    etag            TEXT NOT NULL,                -- bumps on every edit (CardDAV sync)
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (address_book_id, uid)
);
CREATE INDEX idx_contacts_book ON contacts(address_book_id);
CREATE INDEX idx_contacts_name ON contacts(address_book_id, full_name);

-- Photos isolated so the hot contacts table stays lean.
CREATE TABLE contact_photos (
    contact_id INTEGER PRIMARY KEY REFERENCES contacts(id) ON DELETE CASCADE,
    mime_type  TEXT NOT NULL,
    bytes      BLOB NOT NULL,
    etag       TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Per-book access grants.
CREATE TABLE address_book_members (
    address_book_id INTEGER NOT NULL REFERENCES address_books(id) ON DELETE CASCADE,
    user_id         INTEGER NOT NULL REFERENCES users(id)          ON DELETE CASCADE,
    access_level    TEXT NOT NULL CHECK (access_level IN ('viewer','manager')),
    granted_by      INTEGER REFERENCES users(id),
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (address_book_id, user_id)
);
CREATE INDEX idx_abm_user ON address_book_members(user_id);

-- Share links with three modes.
CREATE TABLE share_links (
    id           INTEGER PRIMARY KEY,
    token        TEXT NOT NULL UNIQUE,          -- path component
    mode         TEXT NOT NULL CHECK (mode IN ('authenticated','public_long','gated_short')),
    scope        TEXT NOT NULL CHECK (scope IN ('book','contact')),
    target_id    INTEGER NOT NULL,              -- internal id of book/contact
    secret_hash  TEXT,                          -- argon2id; REQUIRED when mode='gated_short'
    max_uses     INTEGER,
    use_count    INTEGER NOT NULL DEFAULT 0,
    failed_count INTEGER NOT NULL DEFAULT 0,    -- gate brute-force throttling
    expires_at   TEXT,
    revoked      INTEGER NOT NULL DEFAULT 0,
    created_by   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_share_links_token ON share_links(token);

CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,               -- random session id
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_sessions_user ON sessions(user_id);
```

### Contact storage model (hybrid)

Each contact keeps **structured columns** (name, org, primary email/phone) for
fast listing and search, **and** a canonical **`vcard_raw`** blob holding the
full record (multiple emails/phones/addresses, vendor `X-` fields, etc.). Edits
update the structured columns and regenerate `vcard_raw` + `etag`. `uid`, `etag`,
and `updated_at` exist from day one specifically so CardDAV can be added later
without a migration.

---

## 5. User model & access control

### Roles and grants

- **Global role** (`users.role`): `admin` | `user`.
  - `admin` — superuser: manages users, and has implicit `manager` access to every address book.
  - `user` — normal account; all book access derives from grants.
- **Per-book grant** (`address_book_members.access_level`): `viewer` | `manager`.
  - `viewer` — read + download/export for that book.
  - `manager` — full CRUD on the book, plus manage its share links and its members.

This expresses the required scenario directly: a "manager" is a user holding
`manager` grants on their books (e.g. ~10), while a limited user holds `viewer`
grants on only the books they may see (e.g. 2) and sees nothing else.

### Enforcement

Route every access through one resolver:

```
can(user, book, action):
    if user.role == "admin": return ALLOW
    grant = address_book_members[user, book]
    if grant is None: return DENY_AS_NOT_FOUND     # see §6
    if action == read:  return grant in {viewer, manager}
    if action == write: return grant == manager
```

Contacts, photos, and exports inherit their book's permission — never check them
in isolation. Authorization is enforced **server-side on every route**, including
photo and export endpoints (a photo is PII). UI gating is not a substitute.

---

## 6. Identifiers & PII protection

- **Two-tier IDs:** internal integer `id` (never exposed) + random `public_id` (≥128-bit, base64url) used in all URLs, responses, templates, and error messages. Do not expose sequential IDs; do not use ULIDs externally (their ordering leaks counts/sequence).
- **No IDOR:** every external reference resolves `public_id → id` and runs `can(...)` before returning anything.
- **404, not 403, for resources the user may not know exist.** Returning 403 confirms existence. Use 403 only for an action denied *within* a book the user can already see.
- **Keep PII out of URLs** (reference by `public_id`, never name/email) so it never lands in proxy logs, history, or `Referer`.
- **Constant-time comparison** for tokens/secrets; lookups indexed so found-vs-not timing does not leak.
- **Scrub PII from logs**; never log contact records, emails, or share tokens.
- On any shareable page: `noindex`, `Referrer-Policy: no-referrer`, a restrictive CSP, and no third-party resource loads.

---

## 7. Sharing & presenting

Capabilities: public read-only links, a polished directory/presentation view, and
export/download (vCard + CSV).

### Three share modes

| Mode | URL | Who may view | Protected by |
|---|---|---|---|
| `authenticated` | short token | logged-in users (optionally restricted to grants) | the user's session |
| `public_long` | **long** random token (≥160-bit) | anyone on the internet | the unguessable URL (capability URL) |
| `gated_short` | short slug | anyone who passes a gate | a required PIN/password (or OTP) |

Creation rules (enforced):
- `gated_short` **must** have `secret_hash` set — reject creation otherwise (a short slug without a secret is guessable).
- `public_long` token must be high-entropy (≥160-bit).
- All modes support `expires_at`, `max_uses`, and `revoked`.

Verification flow:
1. Look up by `token` (indexed; constant-time on the secret). Reject if revoked/expired/uses-exhausted.
2. `authenticated` → require a valid session (and matching grant if configured).
3. `public_long` → serve anonymously.
4. `gated_short` → show a gate; verify PIN/OTP (argon2id, constant-time); on success set a short-lived signed cookie scoped to that share. Throttle failed attempts via `failed_count` (proxy rate limiting is too coarse to stop slow per-link guessing).

Presentation view: a single shared template powers both the authenticated browse
and the public share, with edit controls toggled by permission.

**Operational caveat:** for `public_long`, the secret token is in the URL path,
so the reverse proxy's access log will record it. Configure the proxy to omit
path/query logging for share routes (`/s/*`), keep short log retention, and rely
on expiry + revocation.

---

## 8. Import

### Formats

- **vCard (.vcf)** — versions 2.1 / 3.0 / 4.0; files may contain many concatenated cards.
- **CSV** — Google Contacts, Outlook, and generic each use different headers.
- **LDIF** — optional.

### Pipeline

A parser per format yields a **normalized contact struct**; one unified writer
maps that struct to `vcard_raw` + structured columns. Adding a format later means
writing one parser, not touching the write path.

- **vCard:** parse all versions (e.g. `emersion/go-vcard`); normalize to a canonical version but preserve `vcard_raw` for fidelity.
- **CSV:** detect known schemas by header signature; for unknown CSVs, present a column-mapping step. Escape leading `= + - @` on both import and export (CSV injection).
- **Encoding:** handle UTF-8 + BOM, Latin-1, quoted-printable (vCard 2.1).
- **De-duplication/merge:** match on email/phone/source UID; offer skip / overwrite / merge / create-new; preserve source `UID` when present, else generate one.
- **Robustness:** isolate per-record failures (one bad card never aborts the batch); run a **dry-run** showing new/duplicate/error counts, then commit in a transaction.
- Import targets a chosen address book and requires a `manager` grant on it.
- Embedded photos are extracted and pushed through the image pipeline (§9).

---

## 9. Images

Single source of truth: photo bytes live in `contact_photos` as BLOBs.

### Ingest normalization (always)

1. Decode and fix orientation from EXIF.
2. **Strip all metadata** (size reduction and removes GPS/PII).
3. Downscale to a cap (e.g. 512×512 avatar; optional 1024 master).
4. Re-encode at quality ~80.

This is what keeps BLOB storage small; never store originals.

### Format

Store a **single normalized JPEG master**. JPEG serves directly to the browser
*and* embeds directly into a vCard `PHOTO` on export — no transcoding anywhere,
which is the most convenient path. (If storage size later becomes a concern, a
WebP master with JPEG-on-export is the fallback, since some clients reject WebP
in vCard `PHOTO`.)

### Serving & export

- Serve bytes with correct `Content-Type`, a strong `ETag` (`contact_photos.etag`), and `Cache-Control`; honor `If-None-Match` → `304`.
- `contacts.has_photo` lets list/detail views avoid joining the photo table.
- Optional small thumbnail column for large list views.
- Export: embed the master as base64 in vCard `PHOTO`. **Do not double-store** — the bytes live once in `contact_photos`; the `PHOTO` line is synthesized on export.

### HEIC caveat

iPhone HEIC decoding generally needs libheif via CGO, which conflicts with the
CGO-free binary. Decide deliberately: either reject HEIC with a clear message, or
shell out to an optional external converter if present.

---

## 10. CardDAV (deferred)

Not in the initial scope. The schema is already shaped for it: stable `uid`,
per-contact `etag`, `updated_at`, and canonical `vcard_raw`. When added, each
address book becomes a CardDAV collection and the implementation adds the WebDAV
verbs (`PROPFIND`, `REPORT`, `GET`, `PUT`, `DELETE`) plus a sync-token over the
existing data. Native clients (Thunderbird, iOS/macOS Contacts, DAVx⁵) then sync
directly.

---

## 11. Configuration & reverse proxy

The app runs behind a reverse proxy that terminates TLS and does coarse rate
limiting and HSTS. The app binds internally and is told its external identity.

| Env var | Example | Purpose |
|---|---|---|
| `SKRA_LISTEN` | `127.0.0.1:3000` | Bind to localhost so only the proxy reaches it |
| `SKRA_EXTERNAL_URL` | `https://contacts.example.com` | Public origin — builds absolute share links, OTP/email links, redirects |
| `SKRA_TRUSTED_PROXIES` | `127.0.0.1/32,10.0.0.0/8` | Only honor `X-Forwarded-*` from these sources |
| `SKRA_COOKIE_SECURE` | `true` | Force `Secure` cookies |
| `SKRA_SESSION_KEY` | (random) | Sign/encrypt session + share-gate cookies |
| `SKRA_DB_PATH` | `/var/lib/skra/skra.db` | SQLite file location |

Rules:
- All generated absolute URLs use `SKRA_EXTERNAL_URL`, never the internal `host:port`.
- **Drive the `Secure` cookie flag (and cookie domain) from `SKRA_EXTERNAL_URL`/`SKRA_COOKIE_SECURE`, not the internal HTTP connection.** The app sees plain HTTP internally; naive code would set non-`Secure` cookies on an HTTPS site.
- Honor `X-Forwarded-Proto`/`-Host`/`-For` **only** from `SKRA_TRUSTED_PROXIES`.
- Division of responsibility — Proxy: TLS, coarse rate limiting, HSTS. App: authorization, CSP, `Referrer-Policy`, per-share throttling, secure-cookie flags, and the `/s/*` log-hygiene guidance.

---

## 12. Backups

The SQLite file (with its BLOBs) is the entire dataset. Never naively `cp` a live
WAL database.

Baseline (always):
- **Consistent snapshot via `VACUUM INTO`**, exposed as `skra backup --out <path>` — safe on a live DB, single compacted file.
- **Auto-snapshot before migrations** run on startup.
- **GFS rotation** (e.g. hourly→24h, daily→30d, weekly→1y), timestamped.
- **Encrypt at rest** before leaving the host (PII).
- **3-2-1 offsite** to S3-compatible storage or another host.
- **Verify**: periodic `PRAGMA integrity_check` on a backup + real restore tests.

Enhanced (optional, for low RPO / point-in-time): **Litestream** sidecar
streaming WAL changes to object storage, in addition to periodic snapshots.

---

## 13. Operations (plain Linux, no Kubernetes)

### systemd

```ini
# /etc/systemd/system/skra.service
[Unit]
Description=Skra
After=network.target

[Service]
Type=simple                 # "notify" if implementing sd_notify/watchdog
User=skra
Group=skra
WorkingDirectory=/opt/skra
ExecStart=/opt/skra/skra serve
EnvironmentFile=/etc/skra/skra.env
Restart=on-failure
RestartSec=5
OnFailure=notify-failure@%n.service
# WatchdogSec=30            # with Type=notify
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/skra /var/log/skra

[Install]
WantedBy=multi-user.target
```

> `ProtectSystem=strict` makes the filesystem read-only — the SQLite data dir and
> log dir **must** appear in `ReadWritePaths` or writes and backups fail.

### Logging & rotation

Use `log/slog` with JSON in production. Log request id, method, **route pattern**
(e.g. `/s/:token`, never the raw path), status, latency, user id. Never log
contact PII or share tokens.

Choose one rotation approach:

- **journald (simplest):** log to stdout; configure `/etc/systemd/journald.conf` (`SystemMaxUse=500M`, `MaxRetentionSec=2week`). Read with `journalctl -u skra`.
- **file + logrotate:**

```
# /etc/logrotate.d/skra
/var/log/skra/*.log {
    daily
    rotate 14
    missingok
    notifempty
    compress
    delaycompress
    create 0640 skra skra
    sharedscripts
    postrotate
        systemctl kill -s HUP skra.service 2>/dev/null || true
    endscript
}
```

> The `postrotate` SIGHUP assumes the app reopens its log on SIGHUP. If it
> doesn't, use `copytruncate` and drop `postrotate` — but note `copytruncate`
> has a small line-loss race.

### Monitoring

- Build `/healthz` (liveness) and `/readyz` (readiness — cheap DB check, e.g. `SELECT 1`).
- External uptime monitor on `/healthz` (Uptime Kuma, UptimeRobot, or healthchecks.io).
- **Backup dead-man's-switch**: the backup job pings a healthcheck URL on success; silence triggers an alert. This is the highest-value monitor because it catches the failure mode that loses data.
- Optional Prometheus `/metrics`.

Signals that matter most for this app:
- **Disk space (#1)** — SQLite + photo BLOBs grow; alert at ~80%.
- Backup freshness; DB/WAL size trend.
- 5xx rate and p99 latency.
- Spikes in failed logins and failed share-gate attempts (brute-force signal).
- CPU/memory (image transcoding spikes).

Alerting can be as light as **monit**, cron + mail, or healthchecks.io built-ins.

---

## 14. CLI

- `skra serve` — run the HTTP server.
- `skra backup --out <path>` — consistent `VACUUM INTO` snapshot.
- `skra import <file>` — import vCard/CSV (with dry-run support).
- First-run **admin bootstrap**: on an empty database, create the initial admin account.

---

## 15. Branding & assets

- Display name **Skrá**; ASCII `skra` for binary, repo, module path, config keys, URLs.
- UI/wordmark font: **Space Grotesk** (Medium / 500) — self-host the WOFF2 files (served by the binary) for UI text.
- Logo files (provided, monochrome, `currentColor`, theme-adaptive):
  - `skra-lockup.svg` — primary horizontal logo (icon + wordmark; wordmark is outlined, no font dependency).
  - `skra-icon.svg` — contact-card icon with a waving avatar (the wave is the wordmark's acute accent).
  - `skra-favicon.svg` — rounded square + waving figure only.
  - `skra-wordmark.svg` — outlined wordmark alone.
- For crisp 16px rendering, a solid-square favicon variant may be added later.

---

## 16. Suggested project layout

```
skra/
├── main.go
├── go.mod
├── Dockerfile
└── internal/
    ├── config/        # env loading; SKRA_* keys
    ├── db/
    │   ├── db.go      # open, pragmas, auto_vacuum on fresh DB, run migrations, write serialization
    │   └── migrations/0001_init.sql   # embedded
    ├── models/        # users, address books, contacts, photos, share links
    ├── auth/          # argon2id, sessions, login middleware, CSRF
    ├── rbac/          # can(user, book, action); role/grant middleware
    ├── importing/     # per-format parsers + unified writer
    ├── images/        # ingest pipeline (orient, strip, downscale, re-encode), serving
    ├── sharing/       # token generation, modes, gate
    └── web/
        ├── handlers/
        ├── templates/ # embedded html/template
        └── static/    # embedded CSS/JS/htmx + fonts
```

---

## 17. Implementation roadmap

- **Phase 0 — Skeleton.** Module + chi + `modernc.org/sqlite` + `embed.FS`; `db.go` (open, `auto_vacuum` on fresh DB, pragmas, migration runner); config loader; `/healthz`; Dockerfile; static build.
- **Phase 1 — Data & auth.** Apply `0001_init.sql`; argon2id; login/logout; session middleware; secure cookies; admin bootstrap; `can(...)` resolver and RBAC middleware; `public_id` generation; photo endpoint scaffold with ETag/`304`.
- **Phase 2 — Contact management.** Address book CRUD; contact list (pagination + search), detail, create/edit/delete (manager+); photo upload through the ingest pipeline; hybrid write path (columns + `vcard_raw` + `etag`).
- **Phase 3 — Presentation & export.** Shared directory/presentation template; vCard and CSV export (injection-safe).
- **Phase 4 — Public sharing.** Share-link creation with mode validation; public/gated routes reusing the presentation view; gate + per-share throttling; `noindex`/`Referrer-Policy`/CSP.
- **Phase 5 — Hardening & ops.** CSRF, security headers; admin user management; backup CLI + rotation + encryption + offsite; import (vCard/CSV) with dry-run; optional audit log; systemd/logging/monitoring wiring.
- **Phase 6 — CardDAV (later).** WebDAV verbs + sync-token over the existing data model.

---

## 18. Non-negotiable constraints (do not regress)

1. CGO-free build (`modernc.org/sqlite`) — keep the single static binary.
2. SQLite is the only datastore; photos stay as BLOBs (single source of truth).
3. Authorization enforced server-side on every route via `can(...)`; 404-not-403 for unknown-to-user resources.
4. Only `public_id` is ever exposed externally.
5. `gated_short` share links require a secret; `public_long` tokens must be high-entropy.
6. Never log share tokens or PII; never put PII in URLs.
7. Secure-cookie flag and absolute URLs derive from `SKRA_EXTERNAL_URL`, not the internal connection.
8. Backups via `VACUUM INTO` (never naive copy); encrypt before offsite.
9. Preserve `vcard_raw`, `uid`, `etag`, `updated_at` — they are the CardDAV on-ramp.
