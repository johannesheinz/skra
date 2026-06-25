# Skrá

**Skrá** (Old Norse / Icelandic for "register, list, record") is a self-hosted application for storing, managing, and sharing contacts. It is a **single Go binary** that serves both the backend and a server-rendered frontend, storing **all data in one SQLite file** (photos included). It targets a single instance with many users — not multi-tenant SaaS — and runs behind a TLS-terminating reverse proxy.

> ASCII `skra` is used everywhere in code, config, URLs, and the binary name; **Skrá** is the display name.

## Status

**Version 1.1.0** — feature-complete against the baseline specification and ready for self-hosting. Everything below is shipped and covered by tests.

**Phase 0 (skeleton)** — single static CGO-free binary; SQLite open with mandated pragmas (`WAL`, `busy_timeout`, `foreign_keys`, `synchronous=NORMAL`) and `INCREMENTAL auto_vacuum` on a fresh database; embedded migration runner (`schema_migrations`-tracked); `chi` router with `log/slog` logging, graceful shutdown, and `GET /healthz`; `skra serve`.

**Phase 1 (data & auth)** — argon2id password hashing (PHC-encoded, with lazy rehash on login); server-side sessions with HttpOnly/SameSite cookies whose `Secure` flag is config-driven; double-submit CSRF on the auth forms; the `can(user, book, action)` RBAC resolver with 404-not-403 semantics; random `public_id` generation; serialized SQLite writes; a contact-photo endpoint with strong ETag and conditional-GET (304); and `skra create-admin` first-run bootstrap.

**Phase 2 (contact management)** — self-hosted asset foundation (embedded, content-hashed CSS/JS/fonts, semantic component CSS, self-only CSP); address book CRUD with per-book authorization; contact list (search + pagination), detail, and CRUD via the hybrid write path (structured columns + regenerated `vcard_raw` + bumped `etag`); and a photo ingest pipeline (EXIF orientation, metadata stripping, downscale, JPEG re-encode).

**Phase 3 (presentation & export)** — a permission-gated contact directory (card grid, structured for reuse by public sharing); vCard export (whole book or single contact, embedding base64 photos) and CSV export with formula-injection sanitization.

**Phase 4 (public sharing)** — share links for a book or contact in three modes (`authenticated`, `public_long`, `gated_short`); manager-only creation with mode validation, listing, and revocation; public `/s/{token}` serving that reuses the directory/contact presentation, with an HMAC-signed gate (and failure throttling) for gated links.

**Phase 5a (user management)** — admin account management (`/admin/users`: create/edit/reset-password/delete with self-delete and last-admin guards); per-book membership management for managers (grant existing users by username, or create a new scoped account — never an admin); self-service password change. `create-admin` is only the first admin; the rest are made in the UI.

**Phase 5b (import)** — vCard (.vcf) import into a book (manager only): parses concatenated cards with per-card error isolation, extracts embedded photos, and runs upload → dry-run preview → transactional commit with UID/email de-duplication (skip or import-as-new). CSV import is a planned follow-up.

**Rich contact fields** — contacts hold first/last name, multiple typed emails/phones, multiple postal addresses, links, organization, title, birthday, and note. The edit form has repeatable rows (htmx); everything round-trips through `vcard_raw` (the source of truth) with the structured columns kept as a listing/search cache, so editing no longer drops imported multi-value data.

**Phase 5c (backups & ops)** — `skra backup --out <path>` writes a consistent `VACUUM INTO` snapshot; startup auto-snapshots an existing database before applying pending migrations; `GET /readyz` adds a DB readiness check next to `/healthz`. Rotation/encryption/offsite/systemd/monitoring are documented in [`docs/02_skra-operations.md`](docs/02_skra-operations.md).

**Beyond the baseline** — a refined semantic UI with an inlined icon set and a per-user theming system (light/dark/system × flavor × accent); a fully localized interface (`en-US` / `de-DE` / `en-DK`) with locale-aware number/date/address formatting; an accessibility pass (accessible names, focus management, `prefers-reduced-motion`, a high-contrast mode both auto and opt-in, and a CI invariant test); responsive and print stylesheets; an upcoming-birthdays dashboard; map link-outs for addresses; persisted per-user list page-size and sort preferences; admin create-and-import of a vCard into a new address book; and Renovate for dependency and vendored-asset updates.

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

CI publishes an image to the GitHub Container Registry on every push to `main` (tagged `latest` and the commit SHA) and on release tags (tagged with the version, e.g. `1.1.0` and `1.1`). Pull it instead of building:

```sh
docker pull ghcr.io/<owner>/skra:latest   # or a version tag, e.g. :1.1.0
```

Or build locally:

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

## Local development

One command builds the binary, resets and seeds a demo database, prints the credentials, and starts the server:

```sh
./scripts/dev.sh
```

Then open http://127.0.0.1:3000 and sign in as `admin` (or `alice`/`bob`/`carol`/`dave`), password `demo-password-123`. Each run resets the demo data; override any setting by exporting it first (e.g. `SKRA_LISTEN=127.0.0.1:4000 SKRA_DEMO_CONTACTS=300 ./scripts/dev.sh`). Dev-only — cookies aren't Secure and the session key is a well-known value.

To seed a database without running the server:

```sh
go run ./scripts/seed --db skra-demo.db
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

Frontend assets are self-hosted and embedded in the binary — no CDNs, no third-party origins (see [`docs/01_skra-development-principles.md`](docs/01_skra-development-principles.md)). They are vendored under `internal/web/static/` (and `internal/web/icons/` for the icon set); there is no `package.json` (adding one purely for htmx would reintroduce the npm surface we avoid). The binary files are refreshed by hand, but the recorded versions are watched automatically — [`renovate.json`](renovate.json) has regex custom-managers that read this table and open a PR bumping the version string when a new upstream release appears (htmx and Space Grotesk via `github-tags`; Lucide via the `lucide-static` npm package, which is where its version comes from). Each such PR carries a note with the exact repo path of the files to replace.

| Asset | Version | Source |
|---|---|---|
| htmx | 2.0.9 | https://github.com/bigskysoftware/htmx |
| Space Grotesk | 2.0.0 | https://github.com/floriankarsten/space-grotesk |
| Lucide icons | 1.22.0 | https://github.com/lucide-icons/lucide (subset, inlined) |

To update: bump the version in this table (a Renovate PR does this step and links the paths), replace the corresponding files — htmx under `internal/web/static/js/`, the font under `internal/web/static/fonts/`, the Lucide subset under `internal/web/icons/svg/` (also bump `Version` in `internal/web/icons/icons.go`) — and re-run the tests.

## Dependency & asset updates (Renovate)

[`renovate.json`](renovate.json) extends `config:recommended` (weekly, with a dependency dashboard) and watches **everything with a version in the repo**:

- **Go modules** — `go.mod`/`go.sum`, grouped into one "go dependencies" PR.
- **GitHub Actions** — the SHA-pinned `uses:` in `.github/workflows/ci.yml`; Renovate bumps both the pinned commit SHA and its `# vN` comment. (Actions are pinned to SHAs, not mutable tags, as a supply-chain measure — don't reintroduce `@vN` refs.)
- **Dockerfile base images** — the `FROM` tags. The versioned builder (`golang:1.26-bookworm`) gets update PRs; the runtime `distroless/...:nonroot` is a rolling, version-less tag, so it only moves when the image is rebuilt (tracking it would require digest pinning, deliberately not enabled).
- **Vendored front-end assets** — the "Vendored assets" table above, via regex custom-managers (htmx + Space Grotesk from `github-tags`, Lucide from the `lucide-static` npm package). These bump only the recorded version string; the binary/font/SVG files are refreshed by hand, and each PR names the exact path to update.

Renovate is not automatic: enable it once (install the Renovate GitHub App or run the self-hosted action) — see [`docs/03_skra-github-setup.md`](docs/03_skra-github-setup.md).

## License

[MIT](LICENSE.md). Dependencies are permissively licensed and MIT-compatible: `go-chi/chi` (MIT) and `modernc.org/sqlite` (BSD-3-Clause), plus their BSD/MIT-licensed transitive dependencies.
