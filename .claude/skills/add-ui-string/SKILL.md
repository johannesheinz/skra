---
name: add-ui-string
description: Add or change a user-facing UI string in Skrá the localized way — key in every locale catalog, {{t}} in templates, Translator.T in Go. Use whenever introducing or editing user-visible text, including aria-label, title, alt, and placeholder attributes.
---

Skrá is localized by default: no hardcoded user-facing strings anywhere. Every UI string is a key resolved through `internal/i18n`. Follow these steps for each new or changed string.

## Steps

1. **Pick the key.** Dot-namespaced by feature area, matching existing patterns in `internal/i18n/locales/en.json`: `login.title`, `common.save`, `nav.sign_out`, `a11y.row_added`. Reuse an existing key (`common.*`) before minting a new one.
2. **Add the key to every catalog** under `internal/i18n/locales/` — currently `en.json` and `de.json`. Place it in the same neighborhood/section in both files. The catalogs are flat JSON objects; `i18n.MissingKeys` is tested in CI, so a key present in one catalog but not the other fails the build. Write a real German translation, not a copy of the English text.
3. **Use the key.**
   - In templates: `{{t "the.key"}}` — including inside `aria-label="{{t "..."}}"`, `title`, `alt`, and `placeholder` attributes.
   - In Go code: resolve the request's `*i18n.Translator` and call `.T("the.key")`.
4. **Verify:** `go test ./internal/i18n/... ./internal/web/...` — this catches catalog mismatches and template errors.

## Gotchas

- A missing key renders as the key itself at runtime, so a literal `login.title` on screen means a catalog or typo problem.
- Strings assembled from fragments don't localize (word order differs across languages) — prefer one key per complete phrase.
- Removing a string means removing its key from every catalog, or the unused key lingers.
