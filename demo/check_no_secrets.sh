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
  "README.md"
)

# High-signal patterns:
# - explicit key assignments with 32-byte hex material
# - known env key names followed by long hex token
PAT='(PRIVATE_KEY|WORKER_KEY|ARBITRATOR_KEY|VERIFIER_KEY|BACKUP(_WORKER)?_KEY)[^\n=:\"\x27]*[=:][[:space:]]*[\"\x27]?0x[a-fA-F0-9]{64}[\"\x27]?'

echo "Scanning for high-risk secret patterns..."
if rg -n --pcre2 "$PAT" ${TARGETS[@]} >/tmp/secret_scan_hits.txt 2>/dev/null; then
  echo "ERROR: possible secret leak detected:" >&2
  cat /tmp/secret_scan_hits.txt >&2
  exit 1
fi

# Optional warning-only broad hex scan in markdown (can include tx hashes)
BROAD='0x[a-fA-F0-9]{64}'
COUNT=$(rg -n --pcre2 "$BROAD" demo/*.md demo/**/*.md docs/*.md README.md 2>/dev/null | wc -l | tr -d ' ')
echo "OK: no explicit private-key assignments found."
echo "Info: markdown contains $COUNT hex-64 values (expected for tx hashes/nonces)."
