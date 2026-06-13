# Skrá — Operations runbook

How to run Skrá in production. The authoritative detail lives in the baseline spec ([`00_skra-baseline-spec.md`](00_skra-baseline-spec.md)) §11–13; this file is the practical pointer plus what the binary now does for you.

## Health & readiness

- `GET /healthz` — liveness; always 200 while the process is up. Point your process supervisor / uptime monitor here.
- `GET /readyz` — readiness; runs a cheap `SELECT 1` and returns 200 when the database is reachable, 503 otherwise. Use it for load-balancer readiness gating.

## Backups

The single SQLite file (BLOBs included) is the entire dataset. **Never `cp` a live WAL database.**

- **On-demand snapshot:** `skra backup --out /path/to/skra-YYYYMMDD.db` — a consistent, compacted copy via `VACUUM INTO` (safe on a live database). Refuses to overwrite an existing file. Needs only `SKRA_DB_PATH`.
- **Automatic pre-migration snapshot:** on startup, when an existing database has migrations to apply, Skrá writes `‹db›.pre-migrate-‹unixtime›.bak` beside the database before applying them, so a regretted migration is always recoverable.

What the binary does **not** do (operate these around it — see baseline §12):

- **Rotation** (GFS: hourly→24h, daily→30d, weekly→1y): drive `skra backup` from cron/systemd timers with timestamped names and prune old files.
- **Encrypt at rest** before the backup leaves the host (PII).
- **3-2-1 offsite** to S3-compatible storage or another host.
- **Verify**: periodic `PRAGMA integrity_check` on a copy plus real restore tests.
- **Low RPO / point-in-time:** add a Litestream sidecar streaming the WAL to object storage, alongside periodic snapshots.

## Service, logging, monitoring

These are deployment concerns; the baseline spec gives ready-to-use configurations (baseline §13):

- **systemd** unit (`Type=simple`, `Restart=on-failure`, `ProtectSystem=strict` with the data and log dirs in `ReadWritePaths`).
- **Logging:** `log/slog` JSON to stdout; capture via journald (`SystemMaxUse`, `MaxRetentionSec`) or files + logrotate. Never log PII or share tokens; log the route pattern, not raw paths.
- **Monitoring signals that matter most:** disk space (#1 — SQLite + photo BLOBs grow; alert ~80%), backup freshness (a dead-man's-switch on the backup job is the highest-value monitor), 5xx rate and p99 latency, and spikes in failed logins / failed share-gate attempts.
