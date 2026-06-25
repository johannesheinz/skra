# Skrá — Development Principles

Shared conventions for working on Skrá. These complement the architecture rules in [`00_skra-baseline-spec.md`](00_skra-baseline-spec.md) (§18 non-negotiables) — read that for the data/security architecture; this file is about how we write and structure the code, assets, and docs.

## Code

- **Go 1.26+.** Use modern language/stdlib features where they improve clarity, performance, or stability; call them out briefly when introduced.
- **No silent defaults.** If a situation is unclear (missing config, ambiguous input), return an error or fail fast — never fall back to a guessed default. Configuration is validated at startup and reports every problem at once.
- **Prefer speaking names over comments.** Comment only what the names cannot express (intent, non-obvious trade-offs, gotchas). Keep comment density low.
- **Security and performance are default concerns,** not afterthoughts: constant-time comparisons for secrets, parameterized SQL, serialized SQLite writes, bounded resource use.
- **Password hashing is argon2id (PHC-encoded), no pepper.** argon2id already salts per password; a server-side pepper is intentionally *not* used because Skrá ships as a single binary beside its SQLite file on the same host, so a compromise that reads the DB almost certainly reads the pepper too — it adds key-management/rehash burden for little real gain. Revisit only if secrets ever live in a separate KMS/HSM.
- **Tests land with the code,** not after. New behavior is covered where it makes sense; the suite runs under `-race`. `gofmt` and `go vet` are clean before commit.

## Configuration

- All runtime configuration comes from `SKRA_*` environment variables; there are no defaults, so a missing or malformed value is a startup error.

## Frontend & assets

- **Local-first, always.** Serve every asset (fonts, CSS, JS) from the binary via `embed.FS`. No CDNs and no third-party hosts — this removes external runtime dependencies, third-party tracking, and supply-chain attack vectors, and keeps the CSP at `self`.
- **CSS: hand-written semantic/component CSS.** Style components (e.g. `.btn-primary`) with CSS custom properties as design tokens; a few layout-glue utilities are fine. No Tailwind and no Node/npm build step — a build-time dependency tree is itself a supply-chain surface we choose to avoid.
- **Stylesheet is an external embedded `.css` file,** not inline `<style>`, so CSP stays `style-src 'self'` without `'unsafe-inline'`.
- **Fonts: self-hosted Space Grotesk (OFL), WOFF2, subset and committed pre-built** (no font tooling in the build). Single weight unless more is genuinely needed; `font-display: swap` with a system-font fallback stack.
- **htmx is self-hosted** (embedded), never loaded from a CDN.
- Assets get content-hashed filenames and long immutable cache headers.
- **Responsive by default — it has to look good and be usable on a mobile phone.** Every page must be comfortable on a small touch screen, not merely not-broken: no horizontal page scroll (wide content like tables scrolls inside its own container), tap targets are finger-sized on coarse pointers, inputs use `font-size: 16px` so iOS does not zoom, and the nav collapses gracefully. Breakpoints are plain token-driven `@media` rules — no framework. Verify layouts down to a ~360px-wide viewport when changing any page.
- **Address maps are link-outs, never embedded tiles.** Each address links out to OpenStreetMap / Google Maps / directions (`rel="noopener noreferrer"`, opened in a new tab). Embedding a map or tiles would load third-party resources on every view, breach the self-only CSP, and enable tracking, so it is deliberately avoided; a map would only ever be added opt-in with a self-hosted tile server.
- **Print stylesheet.** A `@media print` block keeps the record printable: it strips the app chrome (nav, toolbars, pagers, action buttons, forms, decorative links) and renders black-on-white. The address-book/contact overview and the contact detail page in particular must produce a clean printout / PDF.
- **Localized by default — no hardcoded user-facing strings.** Every string a user can see or a screen reader can read goes through the translation pipeline (see [Internationalization](#internationalization-i18n)); that includes invisible text (`aria-label`, `title`, `alt`, `<title>`). Numbers, dates, and addresses are formatted for the locale, never with a fixed format. Contact data is user content and is never translated. When you add or change UI text, add the key to **every** catalog — the build fails otherwise.
- **Accessibility is guarded, not hoped for.** Semantic elements and landmarks; every control (including icon-only ones) has an accessible name; validation errors move focus to the `role="alert"`; htmx-added rows move focus and announce via a polite live region; `prefers-reduced-motion` and `prefers-contrast` are honoured. This is enforced by an automated invariant test (`internal/web/a11y_test.go`) that renders every page and fails CI if any page lacks a non-empty `lang`, exactly one `<h1>`, a `<title>`, `alt` on images, an accessible name on buttons/links, or a label on a form control. It is a dependency-free Go check by design: a real browser-based audit (axe/Lighthouse/pa11y) is a Node toolchain and is deliberately kept out of CI to honour the no-npm principle — the static check covers structure, and a manual axe/screen-reader spot-check stays a periodic complement for the computed ARIA tree and contrast.

## Internationalization (i18n)

Skrá is translatable. This section is the practical guide to the pipeline.

### Model

A **locale is a language × region** pair (a BCP-47 tag such as `de-DE` or `en-DK`). The **language** subtag selects the message catalog; the **full tag** drives number/date/address formatting. So `en-DK` shows English words with European formats. The registry lives in `internal/i18n` (`locales` slice), default first (`en-US`). Shipped: `en-US`, `de-DE`, `en-DK`.

### Where translations live

Per-language JSON catalogs under `internal/i18n/locales/` (`en.json`, `de.json`). `en.json` is the **source of truth**. A value is either a plain string or a plural object:

```json
{
  "book.new_contact": "New contact",
  "contacts.count": { "one": "{n} contact", "other": "{n} contacts" }
}
```

Plain strings may contain printf verbs (`%s`, `%d`); plural forms use `{n}` for the count. `TestGermanCatalogComplete` fails the build if any locale is missing an English key. A missing key renders as the key itself (visible, never blank).

### Using it in templates

Templates are parsed once per locale with locale-bound funcs, so just call them:

| Func | Use | Example |
|---|---|---|
| `t "key"` | plain string | `{{t "common.save"}}` |
| `tf "key" args…` | printf message, locale numbers | `{{tf "home.welcome" .User.Username}}` |
| `tn "key" n` | plural (picks form, substitutes `{n}`) | `{{tn "contacts.count" .ContactCount}}` |
| `num n` | locale-grouped integer | `{{num .Total}}` |
| `monthday m d` | month+day, no year | `{{monthday .Month .Day}}` |
| `isodate s` | format a stored `YYYY-MM-DD` | `{{isodate .Details.Birthday}}` |
| `typelabel s` | translate a home/work/… tag | `{{typelabel .Type}}` |
| `postalBeforeCity` | address line order | `{{if postalBeforeCity}}…{{end}}` |

Dynamic keys are fine: `{{t (printf "sort.%s" .Key)}}`.

### Using it in handlers

For flash/error strings, translate with the request's locale via `h.tr(r)`:

```go
h.renderAccount(w, r, http.StatusUnprocessableEntity,
    map[string]any{"ProfileError": h.tr(r).T("msg.email_required")})
```

Never pass an English literal into template data. The locale is resolved once by the `ResolveLocale` middleware (saved `UIPreferences.Locale` wins, else `Accept-Language`) and stored on the context; `render` reads it to set `<html lang>`/`dir` and to select the localized template set. `RenderFragment` takes the locale code from `i18n.FromContext`.

### Adding a UI string

1. Add the key to `en.json` **and** every other catalog (`de.json`).
2. Reference it with `t`/`tf`/`tn` in the template, or `h.tr(r).T(...)` in a handler.
3. Run the tests — the coverage guard catches a forgotten locale.

### Adding a locale

1. Add a `Locale` to the `locales` registry in `internal/i18n/i18n.go` (tag, display name, catalog language, date layouts, address order).
2. If it introduces a new **language**, add `locales/<lang>.json` with every key. A new **region** for an existing language (e.g. `en-IE`) needs no new catalog — only the registry entry and its format fields.
3. `x/text` handles `Accept-Language` matching and plural categories automatically.

**RTL is not supported** (deliberate). All locales are left-to-right, so there is no `dir` attribute. The CSS uses logical properties, so adding RTL later means reintroducing a per-locale direction plus `<html dir>` — don't carry that scaffolding until an RTL locale actually ships.

## Releases

Skrá follows semantic versioning. The version lives in one place — `const Version` in `main.go` — and is printed by `skra version` and logged at startup.

**Versioning convention.** A self-contained fix or small change is a **patch** bump (1.0.0 → 1.0.1), committed *without* a tag. When a planned batch of work is complete (e.g. a review's remediation plan), bump the **minor** version and tag it. Breaking changes to config, storage, or the CLI would be a **major** bump. Commit the version bump as part of the change it describes; do not tag every patch.

**Cutting a release.**

1. Bump `Version` in `main.go` and commit.
2. Tag it: `git tag -a vX.Y.Z -m "Skrá X.Y.Z"`.
3. Push the tag: `git push origin vX.Y.Z` (or `git push --tags`).

**What a `v*` tag triggers** (`.github/workflows/ci.yml`, fully automated — no manual GitHub Release step):

- `test` runs (fmt, vet, golangci-lint, race tests + real coverage, govulncheck, build).
- `docker` builds and pushes the image to GHCR, tagged `X.Y.Z`, `X.Y`, `latest`, and `sha-…`.
- `release` cross-compiles static CGO-free binaries (linux/darwin × amd64/arm64), writes `SHA256SUMS`, and publishes a GitHub Release whose notes lead with the matching `docker pull` command.

Preconditions: the tag must match `v*`, the `test` and `docker` jobs must pass, and the workflow must exist **at the tagged commit** (tagging an older commit won't run the current release job). First-time repository setup (Actions permissions, image visibility, Renovate) is in [`03_skra-github-setup.md`](03_skra-github-setup.md).

**Supply chain.** Every action in the workflow is pinned to a full commit SHA with a `# vN` comment; Renovate's `github-actions` manager keeps those SHAs current. Do not reintroduce mutable `@vN` action refs.

## Documentation & commit messages

- **Do not hard-wrap prose at a fixed column.** In Markdown, config-file comments, code comments, and commit message bodies, break lines only where it is semantically meaningful (paragraphs, list items, distinct clauses). `gofmt` does not reflow comments, so Go comments are single long lines too.
- Keep docs and the README in step with the code as phases land.
