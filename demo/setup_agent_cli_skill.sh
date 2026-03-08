#!/usr/bin/env bash
set -euo pipefail

# Installs escrow-cli locally and syncs the escrow-cli skill to agent homes.
#
# Usage:
#   bash demo/setup_agent_cli_skill.sh
#   bash demo/setup_agent_cli_skill.sh --skip-cli
#   bash demo/setup_agent_cli_skill.sh --target codex
#   bash demo/setup_agent_cli_skill.sh --target claude

TARGET="all"
SKIP_CLI=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target)
      TARGET="${2:-}"
      shift 2
      ;;
    --skip-cli)
      SKIP_CLI=1
      shift
      ;;
    *)
      echo "Unknown arg: $1" >&2
      exit 1
      ;;
  esac
done

if [[ "$TARGET" != "all" && "$TARGET" != "codex" && "$TARGET" != "claude" ]]; then
  echo "Invalid --target value: $TARGET (expected: all|codex|claude)" >&2
  exit 1
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SKILL_SRC="$REPO_ROOT/skills/escrow-cli/"

if [[ ! -f "${SKILL_SRC}SKILL.md" ]]; then
  echo "Skill source missing: ${SKILL_SRC}SKILL.md" >&2
  exit 1
fi

if [[ "$SKIP_CLI" -eq 0 ]]; then
  echo "Installing escrow-cli to ~/.local/bin ..."
  (cd "$REPO_ROOT" && make go-cli-install)
fi

sync_skill() {
  local dest="$1"
  mkdir -p "$dest"
  rsync -a --delete "$SKILL_SRC" "$dest/"
}

if [[ "$TARGET" == "all" || "$TARGET" == "codex" ]]; then
  echo "Syncing skill to ~/.codex/skills/escrow-cli ..."
  sync_skill "$HOME/.codex/skills/escrow-cli"
fi

if [[ "$TARGET" == "all" || "$TARGET" == "claude" ]]; then
  echo "Syncing skill to ~/.claude/skills/escrow-cli ..."
  sync_skill "$HOME/.claude/skills/escrow-cli"
fi

echo
echo "Done."
echo "escrow-cli binary: $(command -v escrow-cli || echo 'not found in PATH')"
echo "Codex skill path:  $HOME/.codex/skills/escrow-cli/SKILL.md"
echo "Claude skill path: $HOME/.claude/skills/escrow-cli/SKILL.md"
