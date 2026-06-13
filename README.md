# Skrá

**Skrá** (Old Norse / Icelandic for "register, list, record") is a self-hosted application for storing, managing, and sharing contacts. It is a **single Go binary** that serves both the backend and a server-rendered frontend, storing **all data in one SQLite file** (photos included). It targets a single instance with many users — not multi-tenant SaaS — and runs behind a TLS-terminating reverse proxy.

> ASCII `skra` is used everywhere in code, config, URLs, and the binary name; **Skrá** is the display name.

## Status

Early development.

**Phase 0 (skeleton)** — single static CGO-free binary; SQLite open with mandated pragmas (`WAL`, `busy_timeout`, `foreign_keys`, `synchronous=NORMAL`) and `INCREMENTAL auto_vacuum` on a fresh database; embedded migration runner (`schema_migrations`-tracked); `chi` router with `log/slog` logging, graceful shutdown, and `GET /healthz`; `skra serve`.

**Phase 1 (data & auth)** — argon2id password hashing (PHC-encoded, with lazy rehash on login); server-side sessions with HttpOnly/SameSite cookies whose `Secure` flag is config-driven; double-submit CSRF on the auth forms; the `can(user, book, action)` RBAC resolver with 404-not-403 semantics; random `public_id` generation; serialized SQLite writes; a contact-photo endpoint with strong ETag and conditional-GET (304); and `skra create-admin` first-run bootstrap.

See [`docs/00_skra-baseline-spec.md`](docs/00_skra-baseline-spec.md) for the full specification and roadmap.

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

`skra create-admin` additionally reads `SKRA_ADMIN_USERNAME`, `SKRA_ADMIN_EMAIL`, and `SKRA_ADMIN_PASSWORD` (used once, then removable). Additional `SKRA_*` variables (external URL, session signing key, trusted proxies, etc.) arrive in later phases.

## Docker

```sh
docker build -t skra .
docker run --rm -p 3000:3000 \
  -e SKRA_LISTEN=0.0.0.0:3000 \
  -e SKRA_DB_PATH=/var/lib/skra/skra.db \
  -e SKRA_COOKIE_SECURE=false \
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
    ├── models/             # users, grants, contact photos (data access)
    ├── testutil/           # shared test helpers
    └── web/                # chi router, server lifecycle
        ├── handlers/       # HTTP handlers (healthz, login/logout, photo)
        └── templates/      # embedded html/template
```

## License

[MIT](LICENSE.md). Dependencies are permissively licensed and MIT-compatible: `go-chi/chi` (MIT) and `modernc.org/sqlite` (BSD-3-Clause), plus their BSD/MIT-licensed transitive dependencies.
