# Skrá

**Skrá** (Old Norse / Icelandic for "register, list, record") is a self-hosted application for storing, managing, and sharing contacts. It is a **single Go binary** that serves both the backend and a server-rendered frontend, storing **all data in one SQLite file** (photos included). It targets a single instance with many users — not multi-tenant SaaS — and runs behind a TLS-terminating reverse proxy.

> ASCII `skra` is used everywhere in code, config, URLs, and the binary name; **Skrá** is the display name.

## Status

Early development.

**Phase 0 (skeleton)** — single static CGO-free binary; SQLite open with mandated pragmas (`WAL`, `busy_timeout`, `foreign_keys`, `synchronous=NORMAL`) and `INCREMENTAL auto_vacuum` on a fresh database; embedded migration runner (`schema_migrations`-tracked); `chi` router with `log/slog` logging, graceful shutdown, and `GET /healthz`; `skra serve`.

**Phase 1 (data & auth)** — argon2id password hashing (PHC-encoded, with lazy rehash on login); server-side sessions with HttpOnly/SameSite cookies whose `Secure` flag is config-driven; double-submit CSRF on the auth forms; the `can(user, book, action)` RBAC resolver with 404-not-403 semantics; random `public_id` generation; serialized SQLite writes; a contact-photo endpoint with strong ETag and conditional-GET (304); and `skra create-admin` first-run bootstrap.

**Phase 2 (contact management)** — self-hosted asset foundation (embedded, content-hashed CSS/JS/fonts, semantic component CSS, self-only CSP); address book CRUD with per-book authorization; contact list (search + pagination), detail, and CRUD via the hybrid write path (structured columns + regenerated `vcard_raw` + bumped `etag`); and a photo ingest pipeline (EXIF orientation, metadata stripping, downscale, JPEG re-encode).

**Phase 3 (presentation & export)** — a permission-gated contact directory (card grid, structured for reuse by public sharing); vCard export (whole book or single contact, embedding base64 photos) and CSV export with formula-injection sanitization.

**Phase 4 (public sharing)** — share links for a book or contact in three modes (`authenticated`, `public_long`, `gated_short`); manager-only creation with mode validation, listing, and revocation; public `/s/{token}` serving that reuses the directory/contact presentation, with an HMAC-signed gate (and failure throttling) for gated links.

**Phase 5a (user management)** — admin account management (`/admin/users`: create/edit/reset-password/delete with self-delete and last-admin guards); per-book membership management for managers (grant existing users by username, or create a new scoped account — never an admin); self-service password change. `create-admin` is only the first admin; the rest are made in the UI.

**Phase 5b (import)** — vCard (.vcf) import into a book (manager only): parses concatenated cards with per-card error isolation, extracts embedded photos, and runs upload → dry-run preview → transactional commit with UID/email de-duplication (skip or import-as-new). CSV import is a planned follow-up.

**Rich contact fields** — contacts hold first/last name, multiple typed emails/phones, multiple postal addresses, links, organization, title, birthday, and note. The edit form has repeatable rows (htmx); everything round-trips through `vcard_raw` (the source of truth) with the structured columns kept as a listing/search cache, so editing no longer drops imported multi-value data.

See [`docs/00_skra-baseline-spec.md`](docs/00_skra-baseline-spec.md) for the full specification and roadmap, and [`docs/01_skra-development-principles.md`](docs/01_skra-development-principles.md) for conventions.

## Requirements

- **Go 1.26+** (only to build). No C toolchain is needed — the build is CGO-free.
- Optional: Docker, and the `sqlite3` CLI for inspecting the database.

## Build & run

```sh
# Build a static binary.
CGO_ENABLED=0 go build -o skra .

# Configure (no defaults — every variable is required).
cp skra.env.example skra.env
set -a && . ./skra.env && set +a

# Create the initial admin account (first run only; refuses if users exist).
./skra create-admin

# Run.
./skra serve

# Liveness check.
curl http://127.0.0.1:3000/healthz
```

Then open `/login` and sign in with the bootstrapped admin credentials.

## Configuration

All configuration comes from the environment; there are no fallback defaults, so a missing or empty value is a startup error.

| Variable | Example | Purpose |
|---|---|---|
| `SKRA_LISTEN` | `127.0.0.1:3000` | Internal bind address (localhost-only behind a proxy) |
| `SKRA_DB_PATH` | `/var/lib/skra/skra.db` | SQLite file location (created on first run) |
| `SKRA_COOKIE_SECURE` | `true` | `Secure` cookie flag; drive from the external scheme, must be `true`/`false` |
| `SKRA_EXTERNAL_URL` | `https://contacts.example.com` | Public origin for absolute share links (`http(s)://`, no trailing slash) |
| `SKRA_SESSION_KEY` | (random, ≥32 chars) | Secret key signing share-gate cookies; keep stable and secret |

`skra create-admin` additionally reads `SKRA_ADMIN_USERNAME`, `SKRA_ADMIN_EMAIL`, and `SKRA_ADMIN_PASSWORD` (used once, then removable). Remaining `SKRA_*` variables (trusted proxies, etc.) arrive in later phases.

## Docker

```sh
docker build -t skra .
docker run --rm -p 3000:3000 \
  -e SKRA_LISTEN=0.0.0.0:3000 \
  -e SKRA_DB_PATH=/var/lib/skra/skra.db \
  -e SKRA_COOKIE_SECURE=false \
  -e SKRA_EXTERNAL_URL=http://localhost:3000 \
  -e SKRA_SESSION_KEY=change-me-to-a-long-random-secret-value \
  -v skra-data:/var/lib/skra \
  skra
```

The image is a non-root `distroless/static` runtime containing only the binary.

## Tests

```sh
go test ./...
```

## Project layout

```
skra/
├── main.go                 # CLI dispatch (serve, create-admin)
├── Dockerfile
└── internal/
    ├── config/             # SKRA_* env loading + validation
    ├── ids/                # public_id / session / token generation
    ├── db/                 # open, pragmas, auto_vacuum, migrations, write serialization
    │   └── migrations/     # *.sql applied by the runner
    ├── auth/               # argon2id, sessions, cookies, CSRF, middleware
    ├── rbac/               # can(user, book, action) resolver
    ├── models/             # users, grants, address books, contacts, photos
    ├── images/             # photo ingest pipeline (orient, strip, downscale, re-encode)
    ├── testutil/           # shared test helpers
    └── web/                # chi router, server lifecycle
        ├── handlers/       # HTTP handlers (auth, books, contacts, photo)
        ├── static/         # embedded assets (CSS/JS/fonts/img) + serving
        └── templates/      # embedded html/template
```

## Vendored assets

Frontend assets are self-hosted and embedded in the binary — no CDNs, no third-party origins (see [`docs/01_skra-development-principles.md`](docs/01_skra-development-principles.md)). They are vendored under `internal/web/static/` and updated manually; Dependabot cannot track them (it only covers manifest ecosystems, and adding `package.json` purely for htmx would reintroduce the npm surface we avoid).

| Asset | Version | Source |
|---|---|---|
| htmx | 2.0.9 | https://github.com/bigskysoftware/htmx |
| Space Grotesk | 2.0.0 | https://github.com/floriankarsten/space-grotesk |

To update: replace the files under `internal/web/static/{js,fonts}/`, bump the version here, and re-run the tests.

## License

[MIT](LICENSE.md). Dependencies are permissively licensed and MIT-compatible: `go-chi/chi` (MIT) and `modernc.org/sqlite` (BSD-3-Clause), plus their BSD/MIT-licensed transitive dependencies.
