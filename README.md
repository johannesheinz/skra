# Skrá

**Skrá** (Old Norse / Icelandic for "register, list, record") is a self-hosted application for storing, managing, and sharing contacts. It is a **single Go binary** that serves both the backend and a server-rendered frontend, storing **all data in one SQLite file** (photos included). It targets a single instance with many users — not multi-tenant SaaS — and runs behind a TLS-terminating reverse proxy.

> ASCII `skra` is used everywhere in code, config, URLs, and the binary name; **Skrá** is the display name.

## Status

Early development. **Phase 0 (skeleton)** is implemented:

- Single static, CGO-free binary (`modernc.org/sqlite`, pure Go).
- SQLite open with mandated pragmas (`WAL`, `busy_timeout`, `foreign_keys`, `synchronous=NORMAL`) and `INCREMENTAL auto_vacuum` set on a fresh database.
- Embedded migration runner (`schema_migrations`-tracked) applying the initial schema.
- `chi` HTTP router with structured `log/slog` logging, graceful shutdown, and a `GET /healthz` liveness endpoint.
- `skra serve` CLI command.

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

# Run.
./skra serve

# Liveness check.
curl http://127.0.0.1:3000/healthz
```

## Configuration

All configuration comes from the environment; there are no fallback defaults, so a missing or empty value is a startup error.

| Variable | Example | Purpose |
|---|---|---|
| `SKRA_LISTEN` | `127.0.0.1:3000` | Internal bind address (localhost-only behind a proxy) |
| `SKRA_DB_PATH` | `/var/lib/skra/skra.db` | SQLite file location (created on first run) |

Additional `SKRA_*` variables (external URL, session key, trusted proxies, etc.) arrive in later phases.

## Docker

```sh
docker build -t skra .
docker run --rm -p 3000:3000 \
  -e SKRA_LISTEN=0.0.0.0:3000 \
  -e SKRA_DB_PATH=/var/lib/skra/skra.db \
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
├── main.go                 # CLI dispatch (`skra serve`)
├── Dockerfile
└── internal/
    ├── config/             # SKRA_* env loading + validation
    ├── db/                 # open, pragmas, auto_vacuum, embedded migrations
    │   └── migrations/     # *.sql applied by the runner
    └── web/                # chi router, server lifecycle
        └── handlers/       # HTTP handlers (/healthz)
```

## License

[MIT](LICENSE.md). Dependencies are permissively licensed and MIT-compatible: `go-chi/chi` (MIT) and `modernc.org/sqlite` (BSD-3-Clause), plus their BSD/MIT-licensed transitive dependencies.
