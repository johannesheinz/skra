# Skrá Security Review

Scope: authentication, authorization/IDOR, injection, secrets/config, session/CSRF middleware, and general web-app hardening for the Skrá contacts app (Go 1.26, chi, modernc.org/sqlite, htmx). Review only — no code was modified. All line references are to the state of the repository at review time.

Overall the codebase is unusually careful: argon2id hashing, CSPRNG opaque identifiers, parameterized SQL everywhere, `html/template` autoescaping, a restrictive CSP, per-book RBAC funneled through a single `rbac.Can`, and a double-submit CSRF token enforced on state-changing routes. The findings below are mostly Medium/Low and defense-in-depth; there are no confirmed Critical remote-exploitable vulnerabilities.

## Top findings

- Medium — No login rate limiting / account lockout; password auth is brute-forceable at app layer (AuthN).
- Medium — Bootstrap admin password (`create-admin`) has no strength/length check, unlike every other password path (AuthN).
- Medium — Photo upload is decoded fully in memory with no pixel-count cap; a decompression-bomb image can spike memory (DoS).
- Low — CSRF cookie is not `__Host-` prefixed and shares scope with the gate cookie; double-submit relies on cookie integrity the app does not additionally enforce (AuthN/CSRF).
- Low — Gate lockout counter (`failed_count`) is never reset and is per-link only; combined with a ~72-bit short token this is adequate, but the gate secret has no minimum strength (AuthZ/sharing).
- Informational — Committed `skra.env` placeholder secrets, verbose internal logging, session id not rotated on login (no fixation risk given current design).

---

## AuthN (authentication, sessions, CSRF)

### M1 — No rate limiting or lockout on login — Medium
`internal/web/handlers/auth.go:24` (`Login`) verifies credentials with no per-account or per-IP throttling. `internal/web/server.go:66-67` mounts `/login` with no limiter middleware. Argon2id (64 MiB, t=3) makes each guess expensive (`internal/auth/password.go:36-42`), which is a meaningful natural throttle, but there is no lockout after repeated failures and no IP throttle.

- Attack: online password guessing against a known/guessed username. The constant-work dummy-hash path (`auth.go:39-40`) correctly defeats user enumeration but does nothing against brute force.
- Impact: account takeover for weak passwords; also a CPU-DoS vector since each attempt forces a 64 MiB argon2 hash.
- Fix: add a small in-process limiter keyed by username+IP (e.g. token bucket, N attempts / window), returning 429 before hashing. Sketch:

```go
// in Login, before VerifyPassword
if !h.loginLimiter.Allow(username, realIP(r)) {
    h.renderLogin(w, r, http.StatusTooManyRequests, username, "Too many attempts. Try again later.")
    return
}
```

Distinction: confirmed gap; severity is Medium (not High) because of the expensive hash and single-tenant/self-hosted deployment model.

### M2 — Bootstrap admin password has no strength check — Medium
`main.go:115-160` (`createAdmin`) accepts `SKRA_ADMIN_PASSWORD` and hashes it with no minimum length, while every in-app path enforces `MinPasswordLen = 8` (`internal/web/handlers/handlers.go:14`, admin/account/member handlers). The most privileged account can be created with a 1-character password.

- Impact: a weak initial admin credential undermines the whole instance; the admin bypasses all RBAC (`rbac.go:37-38`).
- Fix: enforce the same minimum in `createAdmin`:

```go
if len([]rune(password)) < handlers.MinPasswordLen {
    return fmt.Errorf("SKRA_ADMIN_PASSWORD must be at least %d characters", handlers.MinPasswordLen)
}
```

Distinction: confirmed inconsistency.

### L1 — CSRF cookie hardening (double-submit integrity) — Low
`internal/auth/cookie.go:32-42` sets `HttpOnly`, `Secure` (config-driven), `SameSite=Lax`, `Path=/`. The double-submit check (`internal/auth/csrf.go:32-45`) compares the cookie value to the form field in constant time. This is sound, but:
- The cookie is not `__Host-` prefixed, so a network-adjacent attacker able to write cookies for the domain (e.g. via a sibling subdomain over plain HTTP) could plant a known CSRF cookie/value pair. `SameSite=Lax` plus the token still blunts the classic CSRF, so this is defense-in-depth.
- `Path=/` is fine; the gate cookie correctly scopes to `/s/{token}` (`public.go:221`).

Fix (defense-in-depth): rename to `__Host-skra_csrf` (requires `Secure`, `Path=/`, no `Domain`) when `CookieSecure` is true. Same optional hardening for the session cookie.

### L2 — CSRF token re-issued on every render — Low / Informational
`internal/web/handlers/handlers.go:58` issues a fresh CSRF cookie on every page render. This is functionally correct for double-submit (the form and cookie are always consistent within a response), but it means the token is not stable across concurrent tabs; a form submitted from a stale tab after another render still validates because the token in that form matches the cookie only until the next render overwrites the cookie. In practice Lax+token is fine; noting for awareness, not a vuln.

### Informational — Session id handling
- Session ids are 256-bit CSPRNG values (`internal/ids/ids.go:16,27`), stored server-side; the cookie carries only the opaque id (`session.go:24-48`). Good.
- No session-fixation risk: an anonymous request has no session cookie the app honors; a new random id is minted at login (`auth.go:55-61`). There is no pre-auth session to fixate. Rotating the id on privilege change is moot here.
- Logout deletes the server-side row and clears the cookie (`auth.go:66-82`). Good.
- Sessions are not invalidated on password change (`account.go`, `admin_users.go` password reset). A stolen session survives a password reset. Low; consider deleting the user's other sessions on password change.

### Informational — Password hashing
`internal/auth/password.go`: argon2id, m=64 MiB, t=3, p=NumCPU, 16-byte salt, 32-byte key, PHC-encoded, constant-time compare, version pinned, transparent rehash policy (`NeedsRehash`). This is best practice. Minor: `decode` uses `fmt.Sscanf` on attacker-influenced strings only for stored hashes (not user input), so no concern.

---

## AuthZ / IDOR

### Confirmed sound — external identifiers and the RBAC funnel
- All externally referenced rows use 128-bit CSPRNG `public_id`s (`ids.go:14-24`); internal integer ids never appear in routes. Enumeration of another user's book/contact by guessing ids is infeasible.
- Every book/contact/photo/export/share/member/import handler resolves the resource and calls `rbac.Can` via `authorizeBook` (`books.go:161-191`) or `authorizeContact` (`contacts.go:250-287`), which correctly return 404 when not visible and 403 when visible-but-forbidden (`rbac.go:36-51`). Admin short-circuits to full access (`rbac.go:37-38,56-58`).
- `revokeShare` re-checks that the numeric `shareID` belongs to the authorized target before revoking (`shares.go:149-167`), closing the one place a raw integer id is accepted from the client.
- `resolveShareContact` verifies the contact belongs to the shared book (`public.go:281-285`), preventing cross-book contact access via a book share link.

No IDOR was found. The one caveat below is share-scope, not IDOR.

### L3 — Share links have no per-link authorization re-check after creation — Low (by design)
Public share routes (`server.go:130-136`) are intentionally outside `RequireAuth`; access rests entirely on the capability token and (for gated) the secret. This is the documented design. Points worth noting:
- `ModeAuthenticated` shares require only *any* logged-in user (`public.go:252-257`), not a user with a grant on the book. That is the intended semantics ("any logged-in user with the link") but means the link is a bearer capability for the whole authenticated user base. Confirm this matches intent for sensitive books.
- `ModePublicLong` relies solely on a 160-bit token (`sharing.go:32,50-54`) — adequate.
- `ModeGated` short token is ~72-bit (`sharing.go:33`); brute-forcing the token itself is infeasible, and the secret is gated by `GateMaxFailures = 10` (`sharing.go:29`, enforced in `Usable`, `share_link.go:152-154`).

### L4 — Gate secret has no minimum strength; failure counter never resets — Low
`shares.go:183-184` only checks the gated secret is non-empty; a one-character secret is allowed. `failed_count` is incremented on wrong secret (`public.go:210-214`) and permanently locks the link at 10 failures with no reset path except revoke/recreate. The lockout is good, but a trivial secret plus the 10-attempt budget is guessable for very weak secrets.
- Fix: enforce a minimum secret length in `parseShareForm`; optionally reset `failed_count` on a successful gate.

### Informational — Admin privilege escalation surface
`admin_users.go`: create/update/delete/password handlers are all behind `RequireAdmin` (`server.go:116-125`), which returns 404 to non-admins (`middleware.go:68-77`, avoids surface disclosure). Guards prevent demoting/deleting the last admin (`admin_users.go:100-106,158-161`), self-delete (`154-157`), and orphaning owned books (`162-171`). `BookMemberCreate` hard-codes `RoleUser` so a book manager cannot mint admins (`members.go:98`). Role is validated against an allowlist on create/update (`user.go:39,121`). No escalation path found.

---

## Injection

### Confirmed sound — SQL
Every query in `internal/models/*`, `internal/db`, `internal/auth/session.go`, and the handlers uses `?` placeholders with args; no string concatenation of user input into SQL. `ListContacts` builds the `WHERE` clause by appending fixed fragments and passing the `LIKE` pattern as a bound arg (`contact.go:224-242`) — safe. `vacuumIntoSQL` interpolates a path but single-quotes it and the path is operator-supplied, not user input (`db.go:116-120`). No SQL injection found.

### Confirmed sound — output escaping / XSS
- Templates use `html/template` with autoescaping (`templates.go`); no `template.HTML`/`template.JS`/`template.URL` or custom "safe" helpers exist (grep confirmed none).
- User-controlled contact URLs are rendered as `<a href="{{.}}">` in `contact_show.html:27`. `html/template` applies URL-context escaping and neutralizes dangerous schemes (`javascript:`, `data:` in href are rewritten to `#ZgotmplZ`), so this is not a stored-XSS vector. Same for `mailto:`/`tel:` values.
- CSP is restrictive: `default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'` (`server.go:145-146`), plus `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`. No inline event handlers or inline scripts in templates. This is a strong XSS backstop.

### Confirmed sound — CSV formula injection
`export/csv.go:48-57` prefixes `= + - @ \t \r` leading fields with `'`, and `encoding/csv` handles quoting. Correct.

### L5 — vCard import trusts embedded content; large/nested input handled but photo bomb applies — Low
`importing/vcard.go` parses concatenated vCards with per-card error isolation and an 8 MiB scanner line cap (`vcard.go:155`). PHOTO is decoded from base64 (`extractPhoto`, `vcard.go:119-141`) and only remote-URI photos are skipped (never fetched — no SSRF). Decoded photo bytes go through `images.Process` (`import.go:169-173`), which is the same decode path as direct upload — see M3 for the decompression-bomb concern. vCard text fields are stored and later re-parsed/rendered through autoescaped templates, so no injection. No path traversal: filenames from uploads are never used for storage (photos/imports are BLOBs keyed by id/token).

### Informational — path traversal
Download filenames are sanitized to `[A-Za-z0-9-_]` (`export.go:140-155`). Static assets are served from an embedded FS (`static.Handler()`), not the OS filesystem. No user input reaches a filesystem path. `SKRA_DB_PATH` is operator config, not request input.

---

## DoS / resource limits

### M3 — Image decode has no pixel/dimension cap (decompression bomb) — Medium
`images.Process` (`internal/images/process.go:28-42`) does `image.Decode` on the full upload into memory, then allocates RGBA buffers for orientation/downscale. The request body is capped at 10 MiB (`contacts.go:54`, `import.go:47`), but a 10 MiB PNG/JPEG can decode to a very large pixel surface (e.g. tens of thousands of pixels per side → gigabytes of RGBA). `flipH`/`rotate*` allocate full `image.NewRGBA` buffers (`process.go:95-155`) before downscaling.
- Attack: authenticated (manager) user uploads a crafted small-but-huge-dimensions image, or a vCard import with many such embedded photos, spiking memory and potentially OOM-killing the process.
- Impact: memory-exhaustion DoS. Requires write access to a book (not anonymous), which limits blast radius, but a single manager account can take the instance down.
- Fix: bound decoded dimensions before allocating. Use `image.DecodeConfig` first and reject oversized inputs:

```go
cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
if err != nil { return nil, fmt.Errorf("images: decode config: %w", err) }
const maxPixels = 40_000_000 // ~40 MP
if cfg.Width*cfg.Height > maxPixels || cfg.Width > 20000 || cfg.Height > 20000 {
    return nil, fmt.Errorf("images: image too large")
}
```

Distinction: confirmed missing control; exploitable only by an authenticated writer.

### Informational — other resource limits
- `ReadHeaderTimeout: 10s` is set (`server.go:37`); no `ReadTimeout`/`WriteTimeout`/`IdleTimeout`. Behind a proxy this is usually fine; consider adding them.
- Import uploads are staged in SQLite as BLOBs and purged after 1 hour (`import.go:45`, `import.go:64-71`); staleness cleanup runs on each upload. Multipart memory spills to temp at 1 MiB (`contacts.go:55`). Reasonable.
- Share directory export is capped at 500 contacts (`public.go:18,47`).

---

## Secrets & Config

### Informational — committed `skra.env` with placeholder secrets
`skra.env` exists in the working tree with `SKRA_SESSION_KEY=change-me-...` and `SKRA_ADMIN_PASSWORD=change-me`. It is **not** tracked by git (`.gitignore` ignores `skra.env` and `*.env` except `skra.env.example`; `git ls-files` confirms only `skra.env.example` is tracked). So no secret is committed. Note, though, that the local `skra.env` and `skra.db`/`skra-demo.db` sit in the repo dir — ensure these are never force-added and that the demo DB contains no real credentials.
- The `minSessionKeyLen = 32` check (`config/config.go:13,74-75`) validates length but not entropy; a 32-char low-entropy key weakens the gate-cookie HMAC (`sharing/gate.go`). Document that operators must use a random value.

### Confirmed sound — config validation and cookie Secure derivation
`config.Load` (`config/config.go:44-89`) fails closed: every required var must be present and well-formed, `SKRA_COOKIE_SECURE` must be exactly `true`/`false` (`parseBool`, `93-104`), and `CookieSecure` is correctly derived from the *external* scheme rather than the internal connection (the app is designed to run behind a TLS-terminating proxy). No silent defaults, matching project policy.

### Confirmed sound — Dockerfile
`Dockerfile`: distroless static nonroot runtime (uid 65532), `CGO_ENABLED=0`, `-trimpath -ldflags="-s -w"`, no secrets baked in, data dir owned by nonroot. Good hardening.

### Informational — logging
Handlers log errors with `slog` and never log passwords, hashes, session ids, or raw share tokens (share routes are logged by chi with the *pattern*, per the comment at `server.go:127-129`). `LoadUser` logs only non-`ErrSessionNotFound` lookup errors (`middleware.go:37-40`). Error responses to clients are generic ("internal server error", "forbidden", "invalid username or password"), avoiding info leaks. Good.

---

## Middleware / routing coverage

### Confirmed sound — CSRF applied to all state-changing routes
Every `POST` handler calls `checkForm` (parses form + `VerifyCSRF`) or `VerifyCSRF` directly:
- Auth: `Login`, `Logout` (`auth.go:29,71`).
- Books/contacts/shares/members/account: all POST handlers via `checkForm` (`books.go`, `contacts.go`, `shares.go`, `members.go`, `account.go`).
- Multipart uploads verify CSRF after `ParseMultipartForm` (`contacts.go:180`, `import.go:52`) — correct ordering (form must be parsed before the token field is readable).
- Public gate submit uses `checkForm` (`public.go:206`).
No state-changing route was found without CSRF enforcement. `GET` handlers are side-effect-free except `ShareEntry` incrementing a use counter (`public.go:27`), which is acceptable for a capability link.

### Confirmed sound — auth guards
`RequireAuth` wraps the entire authenticated route group (`server.go:70-112`); `RequireAdmin` wraps admin routes (`116-125`); public share routes are deliberately outside both (`127-136`). `middleware.Recoverer` prevents panics from leaking stack traces. No open redirect: all `http.Redirect` targets are static internal paths (`/`, `/login`, `/books/...`), never user-controlled.

---

## Summary table

| ID | Category | Severity | Status |
|----|----------|----------|--------|
| M1 | AuthN | Medium | Confirmed — no login rate limiting |
| M2 | AuthN | Medium | Confirmed — bootstrap admin password unvalidated |
| M3 | DoS | Medium | Confirmed — no image pixel cap (auth'd writer) |
| L1 | AuthN/CSRF | Low | Defense-in-depth — `__Host-` cookie prefix |
| L3 | AuthZ/sharing | Low | By design — authenticated share = any user |
| L4 | AuthZ/sharing | Low | Confirmed — no gate-secret min length |
| L5 | Injection | Low | Mostly sound — photo bomb via import (see M3) |
| — | Sessions/Secrets/SQL/XSS/CSV/config/Docker/routing | Info | Reviewed, no issue |
