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

## Documentation & commit messages

- **Do not hard-wrap prose at a fixed column.** In Markdown, config-file comments, code comments, and commit message bodies, break lines only where it is semantically meaningful (paragraphs, list items, distinct clauses). `gofmt` does not reflow comments, so Go comments are single long lines too.
- Keep docs and the README in step with the code as phases land.
