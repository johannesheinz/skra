#!/bin/bash
# PostToolUse hook: after any edit to a locale catalog, verify every catalog has the same key set.
# Exit 2 feeds the mismatch back to Claude immediately instead of waiting for the build to fail.
file=$(jq -r '.tool_input.file_path // empty')
[[ "$file" == */internal/i18n/locales/*.json && -f "$file" ]] || exit 0

dir=$(dirname "$file")

for catalog in "$dir"/*.json; do
  if ! jq -e 'type == "object"' "$catalog" >/dev/null 2>&1; then
    echo "i18n catalog is not valid JSON: $catalog" >&2
    exit 2
  fi
done

reference="$dir/en.json"
reference_keys=$(jq -r 'keys_unsorted[]' "$reference" | sort)
mismatch=""

for catalog in "$dir"/*.json; do
  [[ "$catalog" == "$reference" ]] && continue
  catalog_keys=$(jq -r 'keys_unsorted[]' "$catalog" | sort)
  missing_in_catalog=$(comm -23 <(printf '%s\n' "$reference_keys") <(printf '%s\n' "$catalog_keys"))
  missing_in_reference=$(comm -13 <(printf '%s\n' "$reference_keys") <(printf '%s\n' "$catalog_keys"))
  if [[ -n "$missing_in_catalog" ]]; then
    mismatch+="Keys in en.json but missing from $(basename "$catalog"):"$'\n'"$missing_in_catalog"$'\n'
  fi
  if [[ -n "$missing_in_reference" ]]; then
    mismatch+="Keys in $(basename "$catalog") but missing from en.json:"$'\n'"$missing_in_reference"$'\n'
  fi
done

if [[ -n "$mismatch" ]]; then
  {
    echo "i18n catalogs are out of sync — every key must exist in every catalog or the build fails."
    echo "$mismatch"
  } >&2
  exit 2
fi
