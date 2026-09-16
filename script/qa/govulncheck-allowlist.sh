#!/usr/bin/env bash
set -euo pipefail

# Temporary release waiver tracked by https://github.com/RandomCodeSpace/unified-agent-manager/issues/68.
# Remove this script when the project can move beyond Go 1.26.5 (remaining IDs are fixed in Go 1.26.6).
readonly expected_go_version="go1.26.5"
readonly -a allowed_vulnerabilities=(
  GO-2026-5026
  GO-2026-5972
  GO-2026-6090
  GO-2026-6218
)

actual_go_version="$(go env GOVERSION)"
if [[ "$actual_go_version" != "$expected_go_version" ]]; then
  echo "govulncheck waiver is valid only for $expected_go_version; found $actual_go_version" >&2
  exit 1
fi

report="$(mktemp)"
found="$(mktemp)"
allowed="$(mktemp)"
trap 'rm -f "$report" "$found" "$allowed"' EXIT

if govulncheck ./... >"$report" 2>&1; then
  cat "$report"
  exit 0
else
  status=$?
fi

cat "$report"
if [[ "$status" -ne 3 ]]; then
  echo "govulncheck failed with exit code $status" >&2
  exit "$status"
fi

grep -oE 'GO-[0-9]{4}-[0-9]+' "$report" | sort -u >"$found" || true
printf '%s\n' "${allowed_vulnerabilities[@]}" >"$allowed"

if [[ ! -s "$found" ]]; then
  echo "govulncheck reported vulnerabilities without parseable IDs" >&2
  exit 1
fi

unexpected="$(comm -13 "$allowed" "$found")"
if [[ -n "$unexpected" ]]; then
  echo "govulncheck found vulnerabilities outside the Go 1.26.5 waiver:" >&2
  echo "$unexpected" >&2
  exit 1
fi

while IFS= read -r vulnerability; do
  echo "::warning title=Accepted Go 1.26.5 vulnerability::$vulnerability is temporarily allowed by issue #68"
done <"$found"
