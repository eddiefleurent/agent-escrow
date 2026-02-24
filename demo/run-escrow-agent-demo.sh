#!/usr/bin/env bash
# run-escrow-agent-demo.sh — Two-Agent Codex Escrow Demo
#
# Orchestrates a buyer agent and a worker agent running as separate Codex processes.
# The buyer posts an RFQ, the worker bids and submits work, the buyer approves.
# Coordination happens through files in demo/.agent-state/.
#
# Usage:
#   bash demo/run-escrow-agent-demo.sh
#
# Required environment variables (set in .env or exported before running):
#   ESCROW_SERVER_URL     — server base URL (e.g. https://your-server.example.com)
#   BASE_SEPOLIA_RPC      — RPC URL (e.g. https://sepolia.base.org)
#   WORKER_PRIVATE_KEY    — worker's private key (0x...) — NEVER commit this
#
# Optional:
#   CODEX_BIN             — path to codex binary (default: codex)

set -euo pipefail

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

CODEX_BIN="${CODEX_BIN:-codex}"
STATE_DIR="demo/.agent-state"
BUYER_PROMPT="demo/codex-buyer-agent.md"
WORKER_PROMPT="demo/codex-worker-agent.md"
BUYER_PID=""

cleanup_buyer() {
  if [ -n "$BUYER_PID" ] && kill -0 "$BUYER_PID" 2>/dev/null; then
    echo "Cleaning up buyer agent (PID $BUYER_PID)..." >&2
    kill "$BUYER_PID" 2>/dev/null || true
    wait "$BUYER_PID" 2>/dev/null || true
  fi
}

trap cleanup_buyer EXIT

# ---------------------------------------------------------------------------
# Load environment
# ---------------------------------------------------------------------------

if [ -f .env ]; then
  # shellcheck source=/dev/null
  set -a
  source .env
  set +a
fi

# Normalize key name: support both WORKER_PRIVATE_KEY and WORKER_KEY
WORKER_PRIVATE_KEY="${WORKER_PRIVATE_KEY:-${WORKER_KEY:-}}"
export WORKER_PRIVATE_KEY

# Validate required variables
for var in ESCROW_SERVER_URL BASE_SEPOLIA_RPC WORKER_PRIVATE_KEY; do
  if [ -z "${!var:-}" ]; then
    echo "ERROR: $var is not set. Export it or add it to .env." >&2
    exit 1
  fi
done

# ---------------------------------------------------------------------------
# Pre-flight checks
# ---------------------------------------------------------------------------

if ! command -v "$CODEX_BIN" &>/dev/null; then
  echo "ERROR: codex not found (tried: $CODEX_BIN). Install from https://github.com/openai/codex" >&2
  exit 1
fi

if ! command -v cast &>/dev/null; then
  echo "ERROR: cast not found. Install Foundry: https://book.getfoundry.sh/getting-started/installation" >&2
  exit 1
fi

if ! command -v escrow-cli &>/dev/null; then
  echo "ERROR: escrow-cli not found. Run: make go-cli-install" >&2
  exit 1
fi

echo "=== Two-Agent Codex Escrow Demo ==="
echo "Server:  $ESCROW_SERVER_URL"
echo "RPC:     $BASE_SEPOLIA_RPC"
echo "Buyer:   0xA52bd5190B344445d91877c7E1e1a11718A205d1 (server signing key)"
echo "Worker:  0x13c010aC7cf2bd187adAfEAd2D73E52fF48765e2"
echo ""

# ---------------------------------------------------------------------------
# State directory
# ---------------------------------------------------------------------------

mkdir -p "$STATE_DIR"
rm -f "$STATE_DIR"/rfq_id "$STATE_DIR"/escrow \
       "$STATE_DIR"/buyer-done "$STATE_DIR"/worker-done

# ---------------------------------------------------------------------------
# Run buyer agent in background
# ---------------------------------------------------------------------------

echo "--- Starting buyer agent (background) ---"
"$CODEX_BIN" exec \
  --dangerously-bypass-approvals-and-sandbox \
  -C . \
  "$(cat "$BUYER_PROMPT")" \
  > "$STATE_DIR/buyer-agent.log" 2>&1 &
BUYER_PID=$!
echo "Buyer agent PID: $BUYER_PID"
echo ""

# ---------------------------------------------------------------------------
# Wait for buyer to post the RFQ (writes demo/.agent-state/rfq_id)
# ---------------------------------------------------------------------------

echo "--- Waiting for buyer to post RFQ ---"
WAIT=0
MAX_WAIT=300
fail_wait_for_rfq() {
  echo "ERROR: $1" >&2
  echo "Buyer agent log below:" >&2
  cat "$STATE_DIR/buyer-agent.log" >&2
  kill "$BUYER_PID" 2>/dev/null || true
  exit 1
}

until [ -f "$STATE_DIR/rfq_id" ]; do
  if ! kill -0 "$BUYER_PID" 2>/dev/null; then
    fail_wait_for_rfq "Buyer agent exited before writing RFQ."
  fi
  if [ $WAIT -ge $MAX_WAIT ]; then
    fail_wait_for_rfq "Timed out waiting for RFQ."
  fi
  sleep 2
  WAIT=$((WAIT + 2))
done

RFQ_ID=$(cat "$STATE_DIR/rfq_id")
echo "RFQ posted: $RFQ_ID"
echo ""

# ---------------------------------------------------------------------------
# Run worker agent in foreground
# ---------------------------------------------------------------------------

echo "--- Starting worker agent (foreground) ---"
"$CODEX_BIN" exec \
  --dangerously-bypass-approvals-and-sandbox \
  -C . \
  "$(cat "$WORKER_PROMPT")"
echo ""

# ---------------------------------------------------------------------------
# Wait for buyer to finish (approve + settled)
# ---------------------------------------------------------------------------

echo "--- Waiting for buyer agent to complete ---"
if wait "$BUYER_PID"; then
  BUYER_EXIT=0
else
  BUYER_EXIT=$?
fi
BUYER_PID=""

if [ $BUYER_EXIT -ne 0 ]; then
  echo "WARNING: Buyer agent exited with code $BUYER_EXIT" >&2
  echo "Buyer agent log:" >&2
  cat "$STATE_DIR/buyer-agent.log" >&2
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------

echo ""
echo "=== Demo Complete ==="

if [ -f "$STATE_DIR/escrow" ]; then
  echo "Escrow: $(cat "$STATE_DIR/escrow")"
fi

if [ -f "$STATE_DIR/buyer-done" ]; then
  echo "Buyer:  $(cat "$STATE_DIR/buyer-done")"
fi

if [ -f "$STATE_DIR/worker-done" ]; then
  echo "Worker: $(cat "$STATE_DIR/worker-done")"
fi

echo ""
echo "Buyer agent log: $STATE_DIR/buyer-agent.log"
