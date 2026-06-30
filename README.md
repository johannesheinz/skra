<p align="center">
  <img src="docs/assets/skra-logo.svg" alt="Skrá" width="300">
</p>

**Skrá** (Old Norse / Icelandic for "register, list, record") is a self-hosted app for storing, managing, and sharing contacts.
Run it on your own server and keep your address book — and your contacts' personal data — off third-party services.

It is a **single Go binary** that serves both the API and a server-rendered web UI, with **all data in one SQLite file** (photos included).
It is built for a single instance with many users — not multi-tenant SaaS — and runs behind a TLS-terminating reverse proxy.

> ASCII `skra` is used everywhere in code, config, URLs, and the binary name; **Skrá** is the display name.

## What it does

- Multi-user contacts with a global admin/user role plus per–address-book grants (viewer/manager).
- Rich contacts: multiple typed emails, phones, and postal addresses, links, organization, title, birthday, note, and a photo.
- Import and export as vCard (`.vcf`), with CSV export as well; vCard import has a dry-run preview and duplicate detection.
- Share a book or a single contact via links, including public and PIN-gated modes for people without an account.
- Per-user customization: light/dark theming, locale (`en-US` / `de-DE` / `en-DK`) with locale-aware date, number, and address formatting, and list preferences.
- Built with security and accessibility in mind: argon2id, CSRF, a self-only Content-Security-Policy, no personal data in URLs or logs; accessible names, keyboard focus management, and a high-contrast mode.

## Getting started

The easiest path is a prebuilt Docker image or a pre-compiled binary — no Go toolchain required.

### 1. Get the software

**Docker** — an image is published to the GitHub Container Registry:

```sh
docker pull ghcr.io/johannesheinz/skra:latest
```

**Or a pre-compiled binary** — download the archive for your OS/architecture from the [releases page](https://github.com/johannesheinz/skra/releases) and extract the `skra` binary.

### 2. Configure

All configuration comes from the environment; there are no fallback defaults, so a missing or empty value is a startup error.
Copy the example file and edit it:

```sh
cp skra.env.example skra.env
```

| Variable | Example | Purpose |
|---|---|---|
| `SKRA_LISTEN` | `127.0.0.1:3000` | Internal bind address (localhost-only behind a proxy) |
| `SKRA_DB_PATH` | `/var/lib/skra/skra.db` | SQLite file location (created on first run) |
| `SKRA_COOKIE_SECURE` | `true` | `Secure` cookie flag; set from the external scheme, must be `true`/`false` |
| `SKRA_EXTERNAL_URL` | `https://contacts.example.com` | Public origin for absolute share links (`http(s)://`, no trailing slash) |
| `SKRA_SESSION_KEY` | (random, ≥32 chars) | Secret key signing share-gate cookies; keep stable and secret |

The SQLite database at `SKRA_DB_PATH` is created automatically on first run.

### 3. Create the first admin (once)

`create-admin` bootstraps the first account and refuses to run once any user exists — every other account is created later in the web UI.
It reads `SKRA_ADMIN_USERNAME`, `SKRA_ADMIN_EMAIL`, and `SKRA_ADMIN_PASSWORD`, which are needed only for this step and can then be removed.

With the binary:

```sh
set -a && . ./skra.env && set +a
./skra create-admin
./skra serve
```

With Docker (the container runs `serve` by default; run `create-admin` once first).
Add the three `SKRA_ADMIN_*` variables to `skra.env` for the bootstrap run, then remove them:

```sh
# One-time bootstrap.
docker run --rm --env-file skra.env -v skra-data:/var/lib/skra \
  ghcr.io/johannesheinz/skra:latest create-admin

# Run.
docker run --rm -p 3000:3000 --env-file skra.env -v skra-data:/var/lib/skra \
  ghcr.io/johannesheinz/skra:latest
```

Then open `/login` and sign in with the admin credentials.
The image is a non-root `distroless/static` runtime containing only the binary, so `SKRA_LISTEN` must bind `0.0.0.0` inside the container (e.g. `0.0.0.0:3000`).

## Development

Building from source needs **Go 1.26+** and no C toolchain — the build is CGO-free.

```sh
# Build a static binary.
CGO_ENABLED=0 go build -o skra .

# Run the tests.
go test ./...
```

One command builds the binary, resets and seeds a demo database, prints the credentials, and starts the server:

```sh
./scripts/dev.sh
```

Then open http://127.0.0.1:3000 and sign in as `admin` (or `alice`/`bob`/`carol`/`dave`), password `demo-password-123`.
Each run resets the demo data; override any setting by exporting it first (e.g. `SKRA_LISTEN=127.0.0.1:4000 SKRA_DEMO_CONTACTS=300 ./scripts/dev.sh`).
This is dev-only — cookies aren't Secure and the session key is a well-known value.

### Project layout

```
skra/
├── main.go                 # CLI dispatch (serve, create-admin, backup, version)
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
    ├── i18n/               # locale catalogs, matching, and formatters
    └── web/                # chi router, server lifecycle
        ├── handlers/       # HTTP handlers
        ├── static/         # embedded assets (CSS/JS/fonts/img) + serving
        ├── icons/          # inlined Lucide subset
        └── templates/      # embedded html/template
```

### Vendored assets and dependency updates

Frontend assets are self-hosted and embedded — no CDNs, no third-party origins (see [`docs/development.md`](docs/development.md)).
They live under `internal/web/static/` (and `internal/web/icons/` for the icon set); there is no `package.json`.
The binary files are refreshed by hand, but the recorded versions are watched automatically: [`renovate.json`](renovate.json) opens a PR bumping the version string when a new upstream release appears, and each PR names the exact path to update.

| Asset | Version | Source |
|---|---|---|
| htmx | 2.0.9 | https://github.com/bigskysoftware/htmx |
| Space Grotesk | 2.0.0 | https://github.com/floriankarsten/space-grotesk |
| Lucide icons | 1.22.0 | https://github.com/lucide-icons/lucide (subset, inlined) |

Renovate also watches Go modules, the SHA-pinned GitHub Actions, and the Dockerfile builder image.
It is not automatic — enable it once (see [`docs/github-setup.md`](docs/github-setup.md)).

## Operations

- **Backups.** `skra backup --out <path>` writes a consistent `VACUUM INTO` snapshot while the server is running.
  On startup, an existing database is snapshotted automatically before any pending migrations are applied.
- **Health.** `GET /healthz` is a liveness check; `GET /readyz` adds a database-readiness check.

Rotation, encryption, offsite copies, `systemd`, and monitoring are covered in [`docs/operations.md`](docs/operations.md).

## Documentation

- [`docs/features.md`](docs/features.md) — user guide: what the app does and how to use it.
- [`docs/architecture.md`](docs/architecture.md) — data model, security model, and design rationale.
- [`docs/development.md`](docs/development.md) — coding conventions, the i18n pipeline, accessibility, and the release process.
- [`docs/operations.md`](docs/operations.md) — running Skrá in production.
- [`docs/github-setup.md`](docs/github-setup.md) — one-time GitHub setup (Renovate, CI, releases).
- [`docs/ideas.md`](docs/ideas.md) — backlog of not-yet-built changes.

## License

[MIT](LICENSE.md).
Dependencies are permissively licensed and MIT-compatible: `go-chi/chi` (MIT) and `modernc.org/sqlite` (BSD-3-Clause), plus their BSD/MIT-licensed transitive dependencies.
