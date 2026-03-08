#!/usr/bin/env bash
set -euo pipefail

# Preflight checks for HTTP/CLI/MCP/UCP parity demos.
# This script validates environment, tooling, signer addresses, balances,
# and API connectivity without executing any demo transactions.

BASE_URL="${BASE_URL:-http://localhost:8080}"
RPC_URL="${RPC_URL:-https://sepolia.base.org}"
USDC="${USDC_ADDRESS:-0x036CbD53842c5426634e7929541eC2318f3dCF7e}"

# Tunable thresholds (wei for ETH, 6-decimals for USDC)
MIN_BUYER_ETH_WEI="${MIN_BUYER_ETH_WEI:-300000000000000}"   # 0.0003 ETH
MIN_WORKER_ETH_WEI="${MIN_WORKER_ETH_WEI:-100000000000000}" # 0.0001 ETH
MIN_BUYER_USDC="${MIN_BUYER_USDC:-600000}"                  # 0.60 USDC
MIN_WORKER_USDC="${MIN_WORKER_USDC:-150000}"                # 0.15 USDC
MIN_BACKUP_USDC="${MIN_BACKUP_USDC:-50000}"                 # 0.05 USDC

REQUIRE_USDC=0
if [[ "${1:-}" == "--require-usdc" ]]; then
  REQUIRE_USDC=1
fi

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

need_env() {
  local n="$1"
  [[ -n "${!n:-}" ]] || fail "missing required env var: $n"
}

print_section() {
  echo
  echo "== $1 =="
}

is_uint() {
  [[ "$1" =~ ^[0-9]+$ ]]
}

cmp_ge() {
  local got="$1"
  local need="$2"
  [[ "$(printf '%s\n%s\n' "$got" "$need" | sort -n | head -n1)" == "$need" ]]
}

extract_uint() {
  awk '{print $1}'
}

print_section "Tooling"
need_cmd cast
need_cmd curl
need_cmd python3
need_cmd jq
command -v escrow-cli >/dev/null 2>&1 || echo "WARN: escrow-cli not found (CLI lane will be blocked)"
echo "OK: required base tools present"

print_section "Environment"
need_env PRIVATE_KEY
need_env WORKER_KEY
need_env VERIFIER_KEY
need_env ARBITRATOR_KEY
BACKUP_KEY_RESOLVED="${BACKUP_KEY:-${BACKUP_WORKER_KEY:-}}"
[[ -n "$BACKUP_KEY_RESOLVED" ]] || fail "missing BACKUP_KEY (or BACKUP_WORKER_KEY)"

BUYER_ADDR="$(cast wallet address --private-key "$PRIVATE_KEY")"
WORKER_ADDR="$(cast wallet address --private-key "$WORKER_KEY")"
VERIFIER_ADDR="$(cast wallet address --private-key "$VERIFIER_KEY")"
ARBITRATOR_ADDR="$(cast wallet address --private-key "$ARBITRATOR_KEY")"
BACKUP_ADDR="$(cast wallet address --private-key "$BACKUP_KEY_RESOLVED")"

declare -A ROLE_BY_ADDR=()
for role in BUYER WORKER VERIFIER ARBITRATOR BACKUP; do
  addr_var="${role}_ADDR"
  addr="${!addr_var}"
  prior_role="${ROLE_BY_ADDR[$addr]:-}"
  if [[ -n "$prior_role" ]]; then
    fail "role addresses must be distinct: $prior_role and $role both resolve to $addr"
  fi
  ROLE_BY_ADDR["$addr"]="$role"
done

echo "BUYER=$BUYER_ADDR"
echo "WORKER=$WORKER_ADDR"
echo "VERIFIER=$VERIFIER_ADDR"
echo "ARBITRATOR=$ARBITRATOR_ADDR"
echo "BACKUP=$BACKUP_ADDR"

print_section "API health"
HEALTH_JSON="$(curl -sf "$BASE_URL/api/v1/health")" || fail "health check failed: $BASE_URL/api/v1/health"
CHAIN_ID="$(echo "$HEALTH_JSON" | jq -r '.chain.chain_id // empty')"
BLOCK_NO="$(echo "$HEALTH_JSON" | jq -r '.chain.block_number // empty')"
STATUS="$(echo "$HEALTH_JSON" | jq -r '.status // empty')"
[[ "$STATUS" == "ok" ]] || fail "api health status is not ok"
echo "OK: API status=$STATUS chain_id=${CHAIN_ID:-unknown} block=${BLOCK_NO:-unknown}"

print_section "ETH balances"
BUYER_ETH_WEI="$(cast balance "$BUYER_ADDR" --rpc-url "$RPC_URL" | extract_uint)"
WORKER_ETH_WEI="$(cast balance "$WORKER_ADDR" --rpc-url "$RPC_URL" | extract_uint)"
is_uint "$BUYER_ETH_WEI" || fail "could not parse buyer ETH balance"
is_uint "$WORKER_ETH_WEI" || fail "could not parse worker ETH balance"
echo "Buyer ETH (wei):  $BUYER_ETH_WEI"
echo "Worker ETH (wei): $WORKER_ETH_WEI"
cmp_ge "$BUYER_ETH_WEI" "$MIN_BUYER_ETH_WEI" || fail "buyer ETH below threshold ($MIN_BUYER_ETH_WEI)"
cmp_ge "$WORKER_ETH_WEI" "$MIN_WORKER_ETH_WEI" || fail "worker ETH below threshold ($MIN_WORKER_ETH_WEI)"

if [[ "$REQUIRE_USDC" -eq 1 ]]; then
  print_section "USDC balances"
  BUYER_USDC="$(cast call "$USDC" 'balanceOf(address)(uint256)' "$BUYER_ADDR" --rpc-url "$RPC_URL" | extract_uint)"
  WORKER_USDC="$(cast call "$USDC" 'balanceOf(address)(uint256)' "$WORKER_ADDR" --rpc-url "$RPC_URL" | extract_uint)"
  BACKUP_USDC="$(cast call "$USDC" 'balanceOf(address)(uint256)' "$BACKUP_ADDR" --rpc-url "$RPC_URL" | extract_uint)"
  is_uint "$BUYER_USDC" || fail "could not parse buyer USDC balance"
  is_uint "$WORKER_USDC" || fail "could not parse worker USDC balance"
  is_uint "$BACKUP_USDC" || fail "could not parse backup USDC balance"
  echo "Buyer USDC:  $BUYER_USDC"
  echo "Worker USDC: $WORKER_USDC"
  echo "Backup USDC: $BACKUP_USDC"
  cmp_ge "$BUYER_USDC" "$MIN_BUYER_USDC" || fail "buyer USDC below threshold ($MIN_BUYER_USDC)"
  cmp_ge "$WORKER_USDC" "$MIN_WORKER_USDC" || fail "worker USDC below threshold ($MIN_WORKER_USDC)"
  cmp_ge "$BACKUP_USDC" "$MIN_BACKUP_USDC" || fail "backup USDC below threshold ($MIN_BACKUP_USDC)"
fi

print_section "Summary"
echo "Preflight passed."
echo "BASE_URL=$BASE_URL"
echo "RPC_URL=$RPC_URL"
echo "USDC checks: $([[ "$REQUIRE_USDC" -eq 1 ]] && echo enabled || echo skipped)"
