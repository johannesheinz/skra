# Skrá — Development Principles

Shared conventions for working on Skrá. These complement the architecture rules in [`00_skra-baseline-spec.md`](00_skra-baseline-spec.md) (§18 non-negotiables) — read that for the data/security architecture; this file is about how we write and structure the code, assets, and docs.

## Code

- **Go 1.26+.** Use modern language/stdlib features where they improve clarity, performance, or stability; call them out briefly when introduced.
- **No silent defaults.** If a situation is unclear (missing config, ambiguous input), return an error or fail fast — never fall back to a guessed default. Configuration is validated at startup and reports every problem at once.
- **Prefer speaking names over comments.** Comment only what the names cannot express (intent, non-obvious trade-offs, gotchas). Keep comment density low.
- **Security and performance are default concerns,** not afterthoughts: constant-time comparisons for secrets, parameterized SQL, serialized SQLite writes, bounded resource use.
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
- **Print stylesheet.** A `@media print` block keeps the record printable: it strips the app chrome (nav, toolbars, pagers, action buttons, forms, decorative links) and renders black-on-white. The address-book/contact overview and the contact detail page in particular must produce a clean printout / PDF.
- **Localized by default — no hardcoded user-facing strings.** Every string a user can see or a screen reader can read goes through the translation pipeline (see [Internationalization](#internationalization-i18n)); that includes invisible text (`aria-label`, `title`, `alt`, `<title>`). Numbers, dates, and addresses are formatted for the locale, never with a fixed format. Contact data is user content and is never translated. When you add or change UI text, add the key to **every** catalog — the build fails otherwise.

## Internationalization (i18n)

Skrá is translatable. This section is the practical guide to the pipeline; the design rationale lives in [`00_skra-future-changes.md`](00_skra-future-changes.md) §13.

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

1. Add a `Locale` to the `locales` registry in `internal/i18n/i18n.go` (tag, display name, catalog language, date layouts, address order, direction).
2. If it introduces a new **language**, add `locales/<lang>.json` with every key. A new **region** for an existing language (e.g. `en-IE`) needs no new catalog — only the registry entry and its format fields.
3. `x/text` handles `Accept-Language` matching and plural categories automatically.

## Documentation & commit messages

- **Do not hard-wrap prose at a fixed column.** In Markdown, config-file comments, code comments, and commit message bodies, break lines only where it is semantically meaningful (paragraphs, list items, distinct clauses). `gofmt` does not reflow comments, so Go comments are single long lines too.
- Keep docs and the README in step with the code as phases land.
