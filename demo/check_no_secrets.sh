#!/usr/bin/env bash
set -euo pipefail
shopt -s globstar nullglob

# Guardrail scan for obvious secret leaks in public docs and demo artifacts.
# Focuses on tracked markdown/text/json in repo paths used for demo reporting.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

TARGETS=(
  "demo/*.md"
  "demo/*.json"
  "demo/**/*.md"
  "demo/**/*.json"
  "docs/*.md"
  "skills/**/*.md"
  "skills/**/*.json"
  "README.md"
)
MARKDOWN_TARGETS=(
  "demo/*.md"
  "demo/**/*.md"
  "docs/*.md"
  "skills/**/*.md"
  "README.md"
)
EXPANDED_TARGETS=()
EXPANDED_MARKDOWN_TARGETS=()

# High-signal patterns:
# - explicit key assignments with 32-byte hex material
# - known env key names followed by long hex token
PAT='(PRIVATE_KEY|WORKER_KEY|ARBITRATOR_KEY|VERIFIER_KEY|BACKUP(_WORKER)?_KEY)[^\n=:\"\x27]*[=:][[:space:]]*[\"\x27]?0x[a-fA-F0-9]{64}[\"\x27]?'
TMPFILE=$(mktemp)
trap 'rm -f "$TMPFILE"' EXIT

echo "Scanning for high-risk secret patterns..."
if ! command -v rg >/dev/null 2>&1; then
  echo "ERROR: ripgrep (rg) is not installed; secret scan cannot run" >&2
  exit 1
fi
for pattern in "${TARGETS[@]}"; do
  matches=()
  mapfile -t matches < <(compgen -G "$pattern" || true)
  if [ ${#matches[@]} -gt 0 ]; then
    EXPANDED_TARGETS+=("${matches[@]}")
  fi
done
for pattern in "${MARKDOWN_TARGETS[@]}"; do
  matches=()
  mapfile -t matches < <(compgen -G "$pattern" || true)
  if [ ${#matches[@]} -gt 0 ]; then
    EXPANDED_MARKDOWN_TARGETS+=("${matches[@]}")
  fi
done

printf 'High-risk scan targets (%d files):\n' "${#EXPANDED_TARGETS[@]}"
printf '  %s\n' "${EXPANDED_TARGETS[@]}"
printf 'Markdown broad-scan targets (%d files):\n' "${#EXPANDED_MARKDOWN_TARGETS[@]}"
printf '  %s\n' "${EXPANDED_MARKDOWN_TARGETS[@]}"

if [ ${#EXPANDED_TARGETS[@]} -eq 0 ]; then
  echo "Info: no files matched the high-risk scan patterns."
elif rg -n --pcre2 "$PAT" "${EXPANDED_TARGETS[@]}" >"$TMPFILE" 2>/dev/null; then
  echo "ERROR: possible secret leak detected:" >&2
  cat "$TMPFILE" >&2
  exit 1
fi

# Optional warning-only broad hex scan in markdown (can include tx hashes)
BROAD='0x[a-fA-F0-9]{64}'
if [ ${#EXPANDED_MARKDOWN_TARGETS[@]} -eq 0 ]; then
  COUNT=0
else
  COUNT=$( (rg -n --pcre2 "$BROAD" "${EXPANDED_MARKDOWN_TARGETS[@]}" 2>/dev/null || true) | wc -l | tr -d ' ' )
fi
echo "OK: no explicit private-key assignments found."
echo "Info: markdown contains $COUNT hex-64 values (expected for tx hashes/nonces)."
