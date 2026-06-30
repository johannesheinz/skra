# Skrá — Operations

How to run Skrá in production.
It binds internally behind a reverse proxy that terminates TLS; the app owns authorization, the CSP, cookie flags, and per-share throttling.
See [`architecture.md`](architecture.md) for why the trust model is split this way.

## Configuration and reverse proxy

All configuration comes from `SKRA_*` environment variables; there are no defaults, so a missing or malformed value is a startup error.

| Env var | Example | Purpose |
|---|---|---|
| `SKRA_LISTEN` | `127.0.0.1:3000` | Bind to localhost so only the proxy reaches it |
| `SKRA_EXTERNAL_URL` | `https://contacts.example.com` | Public origin — builds absolute share links, redirects, and email links |
| `SKRA_COOKIE_SECURE` | `true` | Force the `Secure` cookie flag |
| `SKRA_SESSION_KEY` | (random, ≥32 chars) | Signs session and share-gate cookies; keep stable and secret |
| `SKRA_DB_PATH` | `/var/lib/skra/skra.db` | SQLite file location |
| `SKRA_TRUSTED_PROXIES` | `127.0.0.1/32,10.0.0.0/8` | Only honor `X-Forwarded-*` from these sources |

Rules that matter:

- Absolute URLs and the `Secure` cookie flag derive from `SKRA_EXTERNAL_URL` / `SKRA_COOKIE_SECURE`, **not** the internal HTTP connection — the app sees plain HTTP behind the proxy, so naive code would set non-`Secure` cookies on an HTTPS site.
- Honor `X-Forwarded-Proto` / `-Host` / `-For` only from `SKRA_TRUSTED_PROXIES`.
- Division of responsibility — Proxy: TLS, coarse rate limiting, HSTS.
  App: authorization, CSP, `Referrer-Policy`, per-share throttling, secure-cookie flags.
- For a public share link the secret token is in the URL path, so the proxy's access log records it.
  Configure the proxy to omit path/query logging for `/s/*`, keep short log retention, and rely on expiry and revocation.

## Health and readiness

- `GET /healthz` — liveness; always 200 while the process is up.
  Point your process supervisor / uptime monitor here.
- `GET /readyz` — readiness; runs a cheap `SELECT 1` and returns 200 when the database is reachable, 503 otherwise.
  Use it for load-balancer readiness gating.

## Backups

The single SQLite file (BLOBs included) is the entire dataset. **Never `cp` a live WAL database.**

- **On-demand snapshot:** `skra backup --out /path/to/skra-YYYYMMDD.db` — a consistent, compacted copy via `VACUUM INTO`, safe on a live database.
  Refuses to overwrite an existing file.
  Needs only `SKRA_DB_PATH`.
- **Automatic pre-migration snapshot:** on startup, when an existing database has migrations to apply, Skrá writes `‹db›.pre-migrate-‹unixtime›.bak` beside it before applying them, so a regretted migration is always recoverable.

The binary does not do the following — operate these around it:

- **Rotation** (GFS: hourly→24h, daily→30d, weekly→1y): drive `skra backup` from cron / systemd timers with timestamped names and prune old files.
- **Encrypt at rest** before a backup leaves the host (it contains PII).
- **3-2-1 offsite** to S3-compatible storage or another host.
- **Verify:** periodic `PRAGMA integrity_check` on a copy plus real restore tests.
- **Low RPO / point-in-time:** add a Litestream sidecar streaming the WAL to object storage, alongside periodic snapshots.

## systemd

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

> `ProtectSystem=strict` makes the filesystem read-only — the SQLite data dir and log dir **must** appear in `ReadWritePaths` or writes and backups fail.

## Logging and rotation

Skrá logs via `log/slog` (JSON in production) to stdout.
It logs the request id, method, route pattern (e.g. `/s/:token`, never the raw path), status, latency, and user id.
It never logs contact PII or share tokens.

Choose one rotation approach:

- **journald (simplest):** log to stdout; configure `/etc/systemd/journald.conf` (`SystemMaxUse=500M`, `MaxRetentionSec=2week`).
  Read with `journalctl -u skra`.
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

> The `postrotate` SIGHUP assumes the app reopens its log on SIGHUP.
> If it doesn't, use `copytruncate` and drop `postrotate` — but note `copytruncate` has a small line-loss race.

## Monitoring

- External uptime monitor on `/healthz` (Uptime Kuma, UptimeRobot, or healthchecks.io).
- **Backup dead-man's-switch:** the backup job pings a healthcheck URL on success; silence triggers an alert.
  This is the highest-value monitor — it catches the failure mode that loses data.
- Optional Prometheus `/metrics`.

Signals that matter most for this app:

- **Disk space (#1)** — SQLite + photo BLOBs grow; alert at ~80%.
- Backup freshness; DB/WAL size trend.
- 5xx rate and p99 latency.
- Spikes in failed logins and failed share-gate attempts (a brute-force signal).
- CPU / memory (image processing spikes).

Alerting can be as light as monit, cron + mail, or healthchecks.io built-ins.
