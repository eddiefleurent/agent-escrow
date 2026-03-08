#!/usr/bin/env bash
set -euo pipefail

# V2 Full Feature Demo Script — ETH Edition
# Exercises all V2 features on Base Sepolia with ETH.
#
# Uses HTTP API for buyer/owner actions (server signs as buyer/owner).
# Uses cast send for worker/verifier/arbitrator actions (different keys).

BASE_URL="${BASE_URL:-http://localhost:8080}"
RPC_URL="https://sepolia.base.org"
ZERO_PROOF_HASH="0x0000000000000000000000000000000000000000000000000000000000000000"

require_env() {
  local name="$1"
  if [ -z "${!name:-}" ]; then
    echo "ERROR: missing required environment variable: ${name}" >&2
    echo "Hint: set -a && source .env && set +a" >&2
    exit 1
  fi
}

# Participant keys
# Buyer actions are submitted via HTTP API (server signs with PRIVATE_KEY).
BUYER_KEY="${PRIVATE_KEY:-}"
WORKER_KEY="${WORKER_KEY:-}"
VERIFIER_KEY="${VERIFIER_KEY:-}"
ARBITRATOR_KEY="${ARBITRATOR_KEY:-}"
BACKUP_KEY="${BACKUP_KEY:-${BACKUP_WORKER_KEY:-}}"

require_env "PRIVATE_KEY"
require_env "WORKER_KEY"
require_env "VERIFIER_KEY"
require_env "ARBITRATOR_KEY"
if [ -z "$BACKUP_KEY" ]; then
  echo "ERROR: missing required environment variable: BACKUP_KEY (or BACKUP_WORKER_KEY for compatibility)" >&2
  echo "Hint: set -a && source .env && set +a" >&2
  exit 1
fi

# Derive participant addresses from keys to avoid key/address drift.
BUYER=$(cast wallet address --private-key "$BUYER_KEY")
WORKER=$(cast wallet address --private-key "$WORKER_KEY")
VERIFIER=$(cast wallet address --private-key "$VERIFIER_KEY")
ARBITRATOR=$(cast wallet address --private-key "$ARBITRATOR_KEY")
BACKUP_WORKER=$(cast wallet address --private-key "$BACKUP_KEY")

RESULTS_FILE="${RESULTS_FILE:-/tmp/v2_demo_results.json}"
echo '{}' > "$RESULTS_FILE"

jq_set() {
  local key="$1" val="$2"
  local tmp
  tmp=$(mktemp)
  jq --arg k "$key" --argjson v "$val" '.[$k] = $v' "$RESULTS_FILE" > "$tmp" && mv "$tmp" "$RESULTS_FILE"
}

api() {
  local method="$1" path="$2"
  shift 2
  if [ "$method" = "GET" ]; then
    curl -sf -X GET "${BASE_URL}${path}" "$@" 2>&1
  else
    curl -sf -X "$method" "${BASE_URL}${path}" -H "Content-Type: application/json" "$@" 2>&1
  fi
}

api_retry() {
  local method="$1" path="$2"
  shift 2
  local max_attempts=5
  for i in $(seq 1 $max_attempts); do
    local resp
    if [ "$method" = "GET" ]; then
      resp=$(curl -s -X GET "${BASE_URL}${path}" "$@" 2>&1)
    else
      resp=$(curl -s -X "$method" "${BASE_URL}${path}" -H "Content-Type: application/json" "$@" 2>&1)
    fi
    if echo "$resp" | python3 -c "import sys,json; d=json.load(sys.stdin); assert 'error' not in d" 2>/dev/null; then
      echo "$resp"
      return 0
    fi
    echo "  Retry $i/$max_attempts (got: $(echo "$resp" | head -c 100))..." >&2
    sleep 3
  done
  echo "$resp"
  return 1
}

wait_indexer() {
  sleep 6
}

wait_tx_mined() {
  local tx_hash="$1"
  local max_attempts=20
  for i in $(seq 1 $max_attempts); do
    local receipt
    receipt=$(cast receipt "$tx_hash" --rpc-url "$RPC_URL" --json 2>/dev/null || echo "")
    if [ -n "$receipt" ]; then
      local status
      status=$(echo "$receipt" | python3 -c "import json,sys; s=json.load(sys.stdin).get('status'); print(int(s, 0) if isinstance(s, str) else int(s))" 2>/dev/null || echo "")
      if [ "$status" = "1" ]; then
        return 0
      fi
      if [ "$status" = "0" ]; then
        echo "WARNING: tx $tx_hash reverted (status=0)" >&2
      else
        echo "WARNING: tx $tx_hash has unexpected receipt status (${status:-unknown})" >&2
      fi
      return 1
    fi
    sleep 2
  done
  echo "WARNING: tx $tx_hash not mined after ${max_attempts} attempts" >&2
  return 1
}

wait_tx_receipt() {
  local tx_hash="$1"
  local max_attempts=20
  for i in $(seq 1 $max_attempts); do
    local receipt
    receipt=$(cast receipt "$tx_hash" --rpc-url "$RPC_URL" --json 2>/dev/null || echo "")
    if [ -n "$receipt" ]; then
      echo "$receipt"
      return 0
    fi
    sleep 2
  done
  return 1
}

ts_plus() {
  echo $(( $(date +%s) + $1 ))
}

section() {
  echo ""
  echo "================================================================"
  echo "  $1"
  echo "================================================================"
  echo ""
}

step() {
  echo "  → $1"
}

extract() {
  python3 -c "import sys,json; print(json.load(sys.stdin)['$1'])"
}

cast_tx() {
  local key="$1" to="$2" sig="$3"
  shift 3
  local result tx_hash
  for attempt in 1 2 3 4 5; do
    result=$(cast send "$to" "$sig" "$@" --private-key "$key" --rpc-url "$RPC_URL" --json 2>&1)
    tx_hash=$(echo "$result" | python3 -c "import sys,json; print(json.load(sys.stdin)['transactionHash'])" 2>/dev/null)
    if [ -n "$tx_hash" ]; then
      echo "$tx_hash"
      return 0
    fi
    echo "  cast_tx retry $attempt/5 ($(echo "$result" | head -c 120))..." >&2
    sleep 4
  done
  echo "cast_tx failed for $to" >&2
  return 1
}

cast_tx_value() {
  local key="$1" to="$2" sig="$3" value="$4"
  shift 4
  local result tx_hash
  for attempt in 1 2 3 4 5; do
    result=$(cast send "$to" "$sig" "$@" --value "$value" --private-key "$key" --rpc-url "$RPC_URL" --json 2>&1)
    tx_hash=$(echo "$result" | python3 -c "import sys,json; print(json.load(sys.stdin)['transactionHash'])" 2>/dev/null)
    if [ -n "$tx_hash" ]; then
      echo "$tx_hash"
      return 0
    fi
    echo "  cast_tx_value retry $attempt/5 ($(echo "$result" | head -c 120))..." >&2
    sleep 4
  done
  echo "cast_tx_value failed for $to" >&2
  return 1
}

########################################################################
section "DEMO C: Worker Stake + Happy Path"
########################################################################

DEADLINE_C=$(ts_plus 7200)

step "Creating escrow with worker_stake=50000000000000 (0.00005 ETH)"
RESP=$(api POST /api/v1/escrows -d "{
  \"title\": \"Demo C: Worker Stake Happy Path\",
  \"description\": \"V2 demo exercising worker stake deposit and return on approval\",
  \"buyer\": \"$BUYER\",
  \"worker\": \"$WORKER\",
  \"verifier\": \"$VERIFIER\",
  \"arbitrator\": \"$ARBITRATOR\",
  \"verifier_panel\": [\"$VERIFIER\"],
  \"quorum_verifier_count\": 1,
  \"quorum_threshold\": 1,
  \"amount\": \"100000000000000\",
  \"worker_stake\": \"50000000000000\",
  \"submission_deadline\": \"$DEADLINE_C\",
  \"review_period_seconds\": \"3600\",
  \"dispute_period_seconds\": \"3600\",
  \"arbitrator_timeout_seconds\": \"7200\"
}")
echo "$RESP" | python3 -m json.tool
C_ID=$(echo "$RESP" | extract escrow_id)
C_ADDR=$(echo "$RESP" | extract escrow_address)
C_TX_CREATE=$(echo "$RESP" | extract tx_hash)
echo "  Escrow ID=$C_ID Addr=$C_ADDR"

step "Waiting for create tx to be mined..."
wait_tx_mined "$C_TX_CREATE"
wait_indexer

step "Funding escrow (buyer sends 0.0001 ETH)"
RESP=$(api_retry POST "/api/v1/escrows/${C_ID}/fund")
C_TX_FUND=$(echo "$RESP" | extract tx_hash)
echo "  Fund tx: $C_TX_FUND"

step "Waiting for fund tx to be mined..."
wait_tx_mined "$C_TX_FUND"
wait_indexer

step "Worker deposits stake (0.00005 ETH via cast)"
C_TX_STAKE=$(cast_tx_value "$WORKER_KEY" "$C_ADDR" "depositStake()" "50000000000000") || exit 1
echo "  Stake tx: $C_TX_STAKE"
wait_tx_mined "$C_TX_STAKE"

wait_indexer

step "Worker submits work (via cast)"
SUB_HASH=$(cast keccak "ipfs://QmDemoC_worker_stake_happy_path")
C_TX_SUBMIT=$(cast_tx "$WORKER_KEY" "$C_ADDR" "submit(bytes32,string,bytes32)" "$SUB_HASH" "ipfs://QmDemoC_worker_stake_happy_path" "$ZERO_PROOF_HASH") || exit 1
echo "  Submit tx: $C_TX_SUBMIT"
wait_tx_mined "$C_TX_SUBMIT"

wait_indexer

step "Buyer approves (via HTTP API)"
RESP=$(api_retry POST "/api/v1/escrows/${C_ID}/approve" -d '{"role": "buyer"}')
C_TX_APPROVE=$(echo "$RESP" | extract tx_hash)
echo "  Approve tx: $C_TX_APPROVE"

wait_indexer

step "Checking final state"
api GET "/api/v1/escrows/${C_ID}" | python3 -c "
import sys,json
d=json.load(sys.stdin)
e=d.get('escrow',d)
print(f\"  Status: {e.get('status','?')}\")
"

jq_set "demo_c" "{
  \"escrow_id\": $C_ID,
  \"escrow_address\": \"$C_ADDR\",
  \"tx_create\": \"$C_TX_CREATE\",
  \"tx_fund\": \"$C_TX_FUND\",
  \"tx_stake\": \"$C_TX_STAKE\",
  \"tx_submit\": \"$C_TX_SUBMIT\",
  \"tx_approve\": \"$C_TX_APPROVE\"
}"
echo "  ✓ Demo C complete"

########################################################################
section "DEMO D: Milestone Escrow — Happy Path (3 milestones)"
########################################################################

D0=$(ts_plus 3600)
D1=$(ts_plus 7200)
D2=$(ts_plus 10800)

step "Creating milestone escrow (3 milestones, total 0.0001 ETH)"
RESP=$(api POST /api/v1/escrows -d "{
  \"title\": \"Demo D: Milestone Happy Path\",
  \"description\": \"3 milestones with sequential submit/approve and partial payouts\",
  \"buyer\": \"$BUYER\",
  \"worker\": \"$WORKER\",
  \"verifier\": \"$VERIFIER\",
  \"arbitrator\": \"$ARBITRATOR\",
  \"verifier_panel\": [\"$VERIFIER\"],
  \"quorum_verifier_count\": 1,
  \"quorum_threshold\": 1,
  \"amount\": \"100000000000000\",
  \"submission_deadline\": \"$D2\",
  \"review_period_seconds\": \"3600\",
  \"dispute_period_seconds\": \"3600\",
  \"arbitrator_timeout_seconds\": \"7200\",
  \"milestones\": [
    {\"amount\": \"30000000000000\", \"submission_deadline\": \"$D0\"},
    {\"amount\": \"30000000000000\", \"submission_deadline\": \"$D1\"},
    {\"amount\": \"40000000000000\", \"submission_deadline\": \"$D2\"}
  ]
}")
echo "$RESP" | python3 -m json.tool
D_ID=$(echo "$RESP" | extract escrow_id)
D_ADDR=$(echo "$RESP" | extract escrow_address)
D_TX_CREATE=$(echo "$RESP" | extract tx_hash)
echo "  Escrow ID=$D_ID Addr=$D_ADDR"

step "Waiting for create tx to be mined..."
wait_tx_mined "$D_TX_CREATE"
wait_indexer

step "Funding (buyer sends 0.0001 ETH)"
RESP=$(api_retry POST "/api/v1/escrows/${D_ID}/fund")
D_TX_FUND=$(echo "$RESP" | extract tx_hash)
echo "  Fund tx: $D_TX_FUND"

wait_indexer

step "M0: Worker submits"
H=$(cast keccak "ipfs://QmDemoD_m0")
D_TX_M0S=$(cast_tx "$WORKER_KEY" "$D_ADDR" "submitMilestone(uint8,bytes32,string,bytes32)" 0 "$H" "ipfs://QmDemoD_m0" "$ZERO_PROOF_HASH") || exit 1
echo "  M0 submit tx: $D_TX_M0S"
wait_tx_mined "$D_TX_M0S"

wait_indexer

step "M0: Buyer approves (partial payout 0.00003 ETH)"
RESP=$(api_retry POST "/api/v1/escrows/${D_ID}/approve" -d '{"role": "buyer", "milestone_index": 0}')
D_TX_M0A=$(echo "$RESP" | extract tx_hash)
echo "  M0 approve tx: $D_TX_M0A"

wait_indexer

step "M1: Worker submits"
H=$(cast keccak "ipfs://QmDemoD_m1")
D_TX_M1S=$(cast_tx "$WORKER_KEY" "$D_ADDR" "submitMilestone(uint8,bytes32,string,bytes32)" 1 "$H" "ipfs://QmDemoD_m1" "$ZERO_PROOF_HASH") || exit 1
echo "  M1 submit tx: $D_TX_M1S"
wait_tx_mined "$D_TX_M1S"

wait_indexer

step "M1: Buyer approves (partial payout 0.00003 ETH)"
RESP=$(api_retry POST "/api/v1/escrows/${D_ID}/approve" -d '{"role": "buyer", "milestone_index": 1}')
D_TX_M1A=$(echo "$RESP" | extract tx_hash)
echo "  M1 approve tx: $D_TX_M1A"

wait_indexer

step "M2: Worker submits"
H=$(cast keccak "ipfs://QmDemoD_m2")
D_TX_M2S=$(cast_tx "$WORKER_KEY" "$D_ADDR" "submitMilestone(uint8,bytes32,string,bytes32)" 2 "$H" "ipfs://QmDemoD_m2" "$ZERO_PROOF_HASH") || exit 1
echo "  M2 submit tx: $D_TX_M2S"
wait_tx_mined "$D_TX_M2S"

wait_indexer

step "M2: Buyer approves (final payout 0.00004 ETH + settle)"
RESP=$(api_retry POST "/api/v1/escrows/${D_ID}/approve" -d '{"role": "buyer", "milestone_index": 2}')
D_TX_M2A=$(echo "$RESP" | extract tx_hash)
echo "  M2 approve tx: $D_TX_M2A"

wait_indexer

step "Checking final state"
api GET "/api/v1/escrows/${D_ID}" | python3 -c "
import sys,json
d=json.load(sys.stdin)
e=d.get('escrow',d)
print(f\"  Status: {e.get('status','?')}\")
"

jq_set "demo_d" "{
  \"escrow_id\": $D_ID,
  \"escrow_address\": \"$D_ADDR\",
  \"tx_create\": \"$D_TX_CREATE\",
  \"tx_fund\": \"$D_TX_FUND\",
  \"tx_m0_submit\": \"$D_TX_M0S\",
  \"tx_m0_approve\": \"$D_TX_M0A\",
  \"tx_m1_submit\": \"$D_TX_M1S\",
  \"tx_m1_approve\": \"$D_TX_M1A\",
  \"tx_m2_submit\": \"$D_TX_M2S\",
  \"tx_m2_approve\": \"$D_TX_M2A\"
}"
echo "  ✓ Demo D complete"

########################################################################
section "DEMO E: Milestone + Dispute + Abort"
########################################################################

E0=$(ts_plus 3600)
E1=$(ts_plus 7200)
E2=$(ts_plus 10800)

step "Creating milestone escrow with worker stake"
RESP=$(api POST /api/v1/escrows -d "{
  \"title\": \"Demo E: Milestone Dispute + Abort\",
  \"description\": \"Approve M0, dispute M1 (50/50), abort M2\",
  \"buyer\": \"$BUYER\",
  \"worker\": \"$WORKER\",
  \"verifier\": \"$VERIFIER\",
  \"arbitrator\": \"$ARBITRATOR\",
  \"verifier_panel\": [\"$VERIFIER\"],
  \"quorum_verifier_count\": 1,
  \"quorum_threshold\": 1,
  \"amount\": \"100000000000000\",
  \"worker_stake\": \"50000000000000\",
  \"submission_deadline\": \"$E2\",
  \"review_period_seconds\": \"3600\",
  \"dispute_period_seconds\": \"3600\",
  \"arbitrator_timeout_seconds\": \"7200\",
  \"milestones\": [
    {\"amount\": \"30000000000000\", \"submission_deadline\": \"$E0\"},
    {\"amount\": \"30000000000000\", \"submission_deadline\": \"$E1\"},
    {\"amount\": \"40000000000000\", \"submission_deadline\": \"$E2\"}
  ]
}")
echo "$RESP" | python3 -m json.tool
E_ID=$(echo "$RESP" | extract escrow_id)
E_ADDR=$(echo "$RESP" | extract escrow_address)
E_TX_CREATE=$(echo "$RESP" | extract tx_hash)
echo "  Escrow ID=$E_ID Addr=$E_ADDR"

step "Waiting for create tx to be mined..."
wait_tx_mined "$E_TX_CREATE"
wait_indexer

step "Funding (buyer sends 0.0001 ETH)"
RESP=$(api_retry POST "/api/v1/escrows/${E_ID}/fund")
E_TX_FUND=$(echo "$RESP" | extract tx_hash)
echo "  Fund tx: $E_TX_FUND"

step "Waiting for fund tx to be mined..."
wait_tx_mined "$E_TX_FUND"
wait_indexer

step "Worker deposits stake (0.00005 ETH)"
E_TX_STAKE=$(cast_tx_value "$WORKER_KEY" "$E_ADDR" "depositStake()" "50000000000000") || exit 1
echo "  Stake tx: $E_TX_STAKE"
wait_tx_mined "$E_TX_STAKE"

wait_indexer

step "M0: Worker submits"
H=$(cast keccak "ipfs://QmDemoE_m0")
E_TX_M0S=$(cast_tx "$WORKER_KEY" "$E_ADDR" "submitMilestone(uint8,bytes32,string,bytes32)" 0 "$H" "ipfs://QmDemoE_m0" "$ZERO_PROOF_HASH") || exit 1
echo "  M0 submit tx: $E_TX_M0S"
wait_tx_mined "$E_TX_M0S"

wait_indexer

step "M0: Buyer approves"
RESP=$(api_retry POST "/api/v1/escrows/${E_ID}/approve" -d '{"role": "buyer", "milestone_index": 0}')
E_TX_M0A=$(echo "$RESP" | extract tx_hash)
echo "  M0 approve tx: $E_TX_M0A"

wait_indexer

step "M1: Worker submits"
H=$(cast keccak "ipfs://QmDemoE_m1")
E_TX_M1S=$(cast_tx "$WORKER_KEY" "$E_ADDR" "submitMilestone(uint8,bytes32,string,bytes32)" 1 "$H" "ipfs://QmDemoE_m1" "$ZERO_PROOF_HASH") || exit 1
echo "  M1 submit tx: $E_TX_M1S"
wait_tx_mined "$E_TX_M1S"

wait_indexer

step "M1: Buyer disputes"
RESP=$(api_retry POST "/api/v1/escrows/${E_ID}/dispute" -d '{"role": "buyer", "reason_uri": "ipfs://QmDemoE_dispute_quality", "milestone_index": 1}')
E_TX_M1D=$(echo "$RESP" | extract tx_hash)
echo "  M1 dispute tx: $E_TX_M1D"

wait_indexer

step "M1: Arbitrator resolves (50/50 split, 5000 bps)"
E_TX_M1R=$(cast_tx "$ARBITRATOR_KEY" "$E_ADDR" "resolveMilestoneDispute(uint8,uint16,string)" 1 5000 "ipfs://QmDemoE_resolution_5050") || exit 1
echo "  M1 resolve tx: $E_TX_M1R"

wait_indexer

step "Buyer aborts remaining milestones (M2 refunded)"
RESP=$(api_retry POST "/api/v1/escrows/${E_ID}/abort-milestones")
E_TX_ABORT=$(echo "$RESP" | extract tx_hash)
echo "  Abort tx: $E_TX_ABORT"

wait_indexer

step "Checking final state"
api GET "/api/v1/escrows/${E_ID}" | python3 -c "
import sys,json
d=json.load(sys.stdin)
e=d.get('escrow',d)
print(f\"  Status: {e.get('status','?')}\")
"

jq_set "demo_e" "{
  \"escrow_id\": $E_ID,
  \"escrow_address\": \"$E_ADDR\",
  \"tx_create\": \"$E_TX_CREATE\",
  \"tx_fund\": \"$E_TX_FUND\",
  \"tx_stake\": \"$E_TX_STAKE\",
  \"tx_m0_submit\": \"$E_TX_M0S\",
  \"tx_m0_approve\": \"$E_TX_M0A\",
  \"tx_m1_submit\": \"$E_TX_M1S\",
  \"tx_m1_dispute\": \"$E_TX_M1D\",
  \"tx_m1_resolve\": \"$E_TX_M1R\",
  \"tx_abort\": \"$E_TX_ABORT\"
}"
echo "  ✓ Demo E complete"

########################################################################
section "DEMO F: Backup Agent Activation"
########################################################################

# Short deadline (30s) so we can trigger backup activation quickly
F_DEADLINE=$(ts_plus 30)

step "Creating escrow with backup worker and short deadline"
RESP=$(api POST /api/v1/escrows -d "{
  \"title\": \"Demo F: Backup Agent Activation\",
  \"description\": \"Primary misses deadline, backup activated, completes task\",
  \"buyer\": \"$BUYER\",
  \"worker\": \"$WORKER\",
  \"verifier\": \"$VERIFIER\",
  \"arbitrator\": \"$ARBITRATOR\",
  \"verifier_panel\": [\"$VERIFIER\"],
  \"quorum_verifier_count\": 1,
  \"quorum_threshold\": 1,
  \"amount\": \"100000000000000\",
  \"worker_stake\": \"50000000000000\",
  \"submission_deadline\": \"$F_DEADLINE\",
  \"review_period_seconds\": \"3600\",
  \"dispute_period_seconds\": \"3600\",
  \"arbitrator_timeout_seconds\": \"7200\",
  \"backup_worker\": \"$BACKUP_WORKER\",
  \"backup_deadline_extension\": \"7200\"
}")
echo "$RESP" | python3 -m json.tool
F_ID=$(echo "$RESP" | extract escrow_id)
F_ADDR=$(echo "$RESP" | extract escrow_address)
F_TX_CREATE=$(echo "$RESP" | extract tx_hash)
echo "  Escrow ID=$F_ID Addr=$F_ADDR"

step "Waiting for create tx to be mined..."
wait_tx_mined "$F_TX_CREATE"
wait_indexer

step "Funding (buyer sends 0.0001 ETH)"
RESP=$(api_retry POST "/api/v1/escrows/${F_ID}/fund")
F_TX_FUND=$(echo "$RESP" | extract tx_hash)
echo "  Fund tx: $F_TX_FUND"

step "Waiting for fund tx to be mined..."
wait_tx_mined "$F_TX_FUND"
wait_indexer

step "Worker deposits stake (0.00005 ETH)"
F_TX_STAKE=$(cast_tx_value "$WORKER_KEY" "$F_ADDR" "depositStake()" "50000000000000") || exit 1
echo "  Stake tx: $F_TX_STAKE"

step "Waiting for primary worker deadline to expire (35s)..."
sleep 35

step "Buyer activates backup worker (via HTTP API)"
RESP=$(api_retry POST "/api/v1/escrows/${F_ID}/activate-backup")
F_TX_BACKUP=$(echo "$RESP" | extract tx_hash)
echo "  Backup activation tx: $F_TX_BACKUP"

wait_indexer

step "Backup worker deposits stake (required after activation)"
F_TX_BSTAKE=$(cast_tx_value "$BACKUP_KEY" "$F_ADDR" "depositStake()" "50000000000000") || exit 1
echo "  Backup stake tx: $F_TX_BSTAKE"
wait_tx_mined "$F_TX_BSTAKE"

wait_indexer

step "Backup worker submits"
H=$(cast keccak "ipfs://QmDemoF_backup_submission")
F_TX_SUBMIT=$(cast_tx "$BACKUP_KEY" "$F_ADDR" "submit(bytes32,string,bytes32)" "$H" "ipfs://QmDemoF_backup_submission" "$ZERO_PROOF_HASH") || exit 1
echo "  Submit tx: $F_TX_SUBMIT"
wait_tx_mined "$F_TX_SUBMIT"

wait_indexer

step "Buyer approves"
RESP=$(api_retry POST "/api/v1/escrows/${F_ID}/approve" -d '{"role": "buyer"}')
F_TX_APPROVE=$(echo "$RESP" | extract tx_hash)
echo "  Approve tx: $F_TX_APPROVE"

wait_indexer

step "Checking final state"
api GET "/api/v1/escrows/${F_ID}" | python3 -c "
import sys,json
d=json.load(sys.stdin)
e=d.get('escrow',d)
print(f\"  Status: {e.get('status','?')}\")
"

jq_set "demo_f" "{
  \"escrow_id\": $F_ID,
  \"escrow_address\": \"$F_ADDR\",
  \"tx_create\": \"$F_TX_CREATE\",
  \"tx_fund\": \"$F_TX_FUND\",
  \"tx_primary_stake\": \"$F_TX_STAKE\",
  \"tx_backup_activation\": \"$F_TX_BACKUP\",
  \"tx_backup_stake\": \"$F_TX_BSTAKE\",
  \"tx_submit\": \"$F_TX_SUBMIT\",
  \"tx_approve\": \"$F_TX_APPROVE\"
}"
echo "  ✓ Demo F complete"

########################################################################
section "DEMO G: Bidding Protocol — RFQ to Escrow"
########################################################################

G_DEADLINE=$(ts_plus 7200)
G_COMMIT_DEADLINE=$(ts_plus 30)
G_REVEAL_DEADLINE=$(ts_plus 60)
G_EXPIRES=$(ts_plus 120)

step "Creating RFQ"
RESP=$(api POST /api/v1/rfqs -d "{
  \"title\": \"Demo G: Smart Contract Audit\",
  \"description\": \"Audit the escrow system for security vulnerabilities\",
  \"buyer\": \"$BUYER\",
  \"budget_min\": \"50000000000000\",
  \"budget_max\": \"150000000000000\",
  \"deadline\": \"$G_DEADLINE\",
  \"commit_deadline\": \"$G_COMMIT_DEADLINE\",
  \"reveal_deadline\": \"$G_REVEAL_DEADLINE\",
  \"review_period_seconds\": \"3600\",
  \"dispute_period_seconds\": \"3600\",
  \"arbitrator_timeout_seconds\": \"7200\",
  \"verifier\": \"$VERIFIER\",
  \"arbitrator\": \"$ARBITRATOR\",
  \"verifier_panel\": [\"$VERIFIER\"],
  \"quorum_verifier_count\": 1,
  \"quorum_threshold\": 1,
  \"requirements_json\": \"{\\\"skills\\\": [\\\"solidity\\\", \\\"security\\\"]}\",
  \"expires_at\": \"$G_EXPIRES\"
}")
echo "$RESP" | python3 -m json.tool
G_RFQ=$(echo "$RESP" | extract id)
echo "  RFQ ID: $G_RFQ"

step "Worker commits sealed bid"
G_BID_EXP=$(ts_plus 3600)
G_BID_NONCE="demo-g-nonce-$(date +%s)"
G_BID_SALT="demo-g-salt-$(date +%s)"
G_BID_MSG="Comprehensive audit within 24 hours"
G_BID_MILESTONES="[]"
G_BID_MILESTONES_HASH=$(cast keccak "$G_BID_MILESTONES")
G_BID_MESSAGE_HASH=$(cast keccak "$G_BID_MSG")
G_BID_STAKE_MANDATE_HASH=$(cast keccak "")
G_BID_PAYLOAD="agent-escrow:sealed-bid:v1|${G_RFQ}|$(echo "$WORKER" | tr '[:upper:]' '[:lower:]')|100000000000000|86400|0|${G_BID_MILESTONES_HASH}|${G_BID_MESSAGE_HASH}|${G_BID_EXP}|${G_BID_STAKE_MANDATE_HASH}|${G_BID_NONCE}|${G_BID_SALT}"
G_BID_COMMITMENT=$(cast keccak "$G_BID_PAYLOAD")
RESP=$(api POST "/api/v1/rfqs/${G_RFQ}/bids/commit" -d "{
  \"bidder\": \"$WORKER\",
  \"commitment\": \"$G_BID_COMMITMENT\",
  \"nonce\": \"$G_BID_NONCE\"
}")
echo "$RESP" | python3 -m json.tool
G_COMMIT_ID=$(echo "$RESP" | extract id)
echo "  Commit ID: $G_COMMIT_ID"

step "Waiting for reveal phase to open"
for _ in $(seq 1 40); do
  NOW_TS=$(date +%s)
  if [ "$NOW_TS" -ge "$G_COMMIT_DEADLINE" ]; then
    break
  fi
  sleep 2
done

step "Worker reveals bid"
RESP=$(api POST "/api/v1/rfqs/${G_RFQ}/bids/reveal" -d "{
  \"bidder\": \"$WORKER\",
  \"amount\": \"100000000000000\",
  \"nonce\": \"$G_BID_NONCE\",
  \"salt\": \"$G_BID_SALT\",
  \"estimated_duration\": 86400,
  \"reputation_bond\": \"0\",
  \"milestones_json\": \"$G_BID_MILESTONES\",
  \"message\": \"$G_BID_MSG\",
  \"expires_at\": \"$G_BID_EXP\"
}")
echo "$RESP" | python3 -m json.tool
G_BID=$(echo "$RESP" | extract id)
echo "  Bid ID: $G_BID"

step "Waiting for reveal phase to end before accept"
for _ in $(seq 1 120); do
  NOW_TS=$(date +%s)
  if [ "$NOW_TS" -gt "$G_REVEAL_DEADLINE" ]; then
    break
  fi
  sleep 2
done

step "Buyer accepts bid (creates escrow on-chain)"
RESP=$(api POST "/api/v1/rfqs/${G_RFQ}/accept" -d "{
  \"bid_id\": $G_BID,
  \"caller\": \"$BUYER\"
}")
echo "$RESP" | python3 -m json.tool
G_ID=$(echo "$RESP" | extract escrow_id)
G_ADDR=$(echo "$RESP" | extract escrow_address)
G_TX_CREATE=$(echo "$RESP" | extract tx_hash)
echo "  Escrow ID=$G_ID Addr=$G_ADDR"

step "Waiting for create tx to be mined..."
wait_tx_mined "$G_TX_CREATE"
wait_indexer

step "Funding (buyer sends 0.0001 ETH)"
RESP=$(api_retry POST "/api/v1/escrows/${G_ID}/fund")
G_TX_FUND=$(echo "$RESP" | extract tx_hash)
echo "  Fund tx: $G_TX_FUND"

wait_indexer

step "Worker submits"
H=$(cast keccak "ipfs://QmDemoG_audit_report")
G_TX_SUBMIT=$(cast_tx "$WORKER_KEY" "$G_ADDR" "submit(bytes32,string,bytes32)" "$H" "ipfs://QmDemoG_audit_report" "$ZERO_PROOF_HASH") || exit 1
echo "  Submit tx: $G_TX_SUBMIT"
wait_tx_mined "$G_TX_SUBMIT"

wait_indexer

step "Buyer approves"
RESP=$(api_retry POST "/api/v1/escrows/${G_ID}/approve" -d '{"role": "buyer"}')
G_TX_APPROVE=$(echo "$RESP" | extract tx_hash)
echo "  Approve tx: $G_TX_APPROVE"

wait_indexer

jq_set "demo_g" "{
  \"rfq_id\": $G_RFQ,
  \"commit_id\": $G_COMMIT_ID,
  \"bid_id\": $G_BID,
  \"escrow_id\": $G_ID,
  \"escrow_address\": \"$G_ADDR\",
  \"tx_create\": \"$G_TX_CREATE\",
  \"tx_fund\": \"$G_TX_FUND\",
  \"tx_submit\": \"$G_TX_SUBMIT\",
  \"tx_approve\": \"$G_TX_APPROVE\"
}"
echo "  ✓ Demo G complete"

########################################################################
section "DEMO H: Reputation Check"
########################################################################

step "Querying reputation for worker"
echo "  Worker ($WORKER):"
api GET "/api/v1/reputation/$WORKER?role=worker" | python3 -m json.tool

step "Querying reputation for buyer"
echo "  Buyer ($BUYER):"
api GET "/api/v1/reputation/$BUYER?role=buyer" | python3 -m json.tool

step "Querying all roles for worker"
RESP_H=$(api GET "/api/v1/reputation/$WORKER")
echo "$RESP_H" | python3 -m json.tool

jq_set "demo_h" "$(echo "$RESP_H")"
echo "  ✓ Demo H complete"

########################################################################
section "DEMO I: Emergency Response"
########################################################################

I_DEADLINE=$(ts_plus 7200)

step "Creating escrow for emergency demo"
RESP=$(api POST /api/v1/escrows -d "{
  \"title\": \"Demo I: Emergency Response\",
  \"description\": \"Freeze, attempt action, emergency resolve with full refund\",
  \"buyer\": \"$BUYER\",
  \"worker\": \"$WORKER\",
  \"verifier\": \"$VERIFIER\",
  \"arbitrator\": \"$ARBITRATOR\",
  \"verifier_panel\": [\"$VERIFIER\"],
  \"quorum_verifier_count\": 1,
  \"quorum_threshold\": 1,
  \"amount\": \"100000000000000\",
  \"submission_deadline\": \"$I_DEADLINE\",
  \"review_period_seconds\": \"3600\",
  \"dispute_period_seconds\": \"3600\",
  \"arbitrator_timeout_seconds\": \"7200\"
}")
echo "$RESP" | python3 -m json.tool
I_ID=$(echo "$RESP" | extract escrow_id)
I_ADDR=$(echo "$RESP" | extract escrow_address)
I_TX_CREATE=$(echo "$RESP" | extract tx_hash)
echo "  Escrow ID=$I_ID Addr=$I_ADDR"

step "Waiting for create tx to be mined..."
wait_tx_mined "$I_TX_CREATE"
wait_indexer

step "Funding (buyer sends 0.0001 ETH)"
RESP=$(api_retry POST "/api/v1/escrows/${I_ID}/fund")
I_TX_FUND=$(echo "$RESP" | extract tx_hash)
echo "  Fund tx: $I_TX_FUND"

wait_indexer

step "Freezing escrow (owner action via HTTP API)"
RESP=$(api_retry POST /api/v1/emergency/freeze-escrow -d "{\"escrow_id\": $I_ID}")
I_TX_FREEZE=$(echo "$RESP" | extract tx_hash)
echo "  Freeze tx: $I_TX_FREEZE"

wait_indexer

step "Attempting submit on frozen escrow (should revert)"
set +e
H=$(cast keccak "ipfs://QmDemoI_should_fail")
FROZEN_RESP=$(cast send "$I_ADDR" "submit(bytes32,string,bytes32)" "$H" "ipfs://QmDemoI_should_fail" "$ZERO_PROOF_HASH" \
  --private-key "$WORKER_KEY" --rpc-url "$RPC_URL" --json 2>&1)
FROZEN_EXIT=$?
set -e
echo "  Exit code: $FROZEN_EXIT (expected revert)"
echo "  Response: $(echo "$FROZEN_RESP" | head -c 200)"
if [ "$FROZEN_EXIT" -eq 0 ]; then
  FROZEN_TX_HASH=$(echo "$FROZEN_RESP" | python3 -c "import json,sys; print(json.load(sys.stdin)['transactionHash'])")
  FROZEN_RECEIPT=$(wait_tx_receipt "$FROZEN_TX_HASH") || {
    echo "ERROR: frozen submit tx broadcast but no receipt found: $FROZEN_TX_HASH" >&2
    exit 1
  }
  FROZEN_STATUS=$(echo "$FROZEN_RECEIPT" | python3 -c "import json,sys; s=json.load(sys.stdin).get('status'); print(int(s, 0) if isinstance(s, str) else int(s))")
  echo "  Receipt: $(echo "$FROZEN_RECEIPT" | head -c 400)"
  [ "$FROZEN_STATUS" -eq 0 ] || {
    echo "ERROR: frozen submit unexpectedly succeeded" >&2
    echo "$FROZEN_RECEIPT" >&2
    exit 1
  }
fi

step "Emergency resolve (full refund to buyer, 0 bps)"
RESP=$(api_retry POST /api/v1/emergency/resolve -d "{\"escrow_id\": $I_ID, \"worker_award_bps\": 0}")
I_TX_RESOLVE=$(echo "$RESP" | extract tx_hash)
echo "  Emergency resolve tx: $I_TX_RESOLVE"

wait_indexer

step "Listing emergency actions (audit log)"
api GET "/api/v1/emergency/actions?limit=10" | python3 -m json.tool

step "Checking final state"
api GET "/api/v1/escrows/${I_ID}" | python3 -c "
import sys,json
d=json.load(sys.stdin)
e=d.get('escrow',d)
print(f\"  Status: {e.get('status','?')}\")
"

jq_set "demo_i" "{
  \"escrow_id\": $I_ID,
  \"escrow_address\": \"$I_ADDR\",
  \"tx_create\": \"$I_TX_CREATE\",
  \"tx_fund\": \"$I_TX_FUND\",
  \"tx_freeze\": \"$I_TX_FREEZE\",
  \"tx_emergency_resolve\": \"$I_TX_RESOLVE\"
}"
echo "  ✓ Demo I complete"

########################################################################
section "OBSERVATION DEMOS"
########################################################################

step "A2A Agent Card"
api GET "/.well-known/agent.json" | python3 -m json.tool

step "SSE Event Stream (3-second sample)"
timeout 3 curl -sN "${BASE_URL}/api/v1/events?granularity=L1" 2>/dev/null || true
echo ""

echo "  ✓ Observation demos complete"

########################################################################
section "SUMMARY"
########################################################################

echo "All V2 demos complete!"
echo ""
echo "Results saved to: $RESULTS_FILE"
echo ""
cat "$RESULTS_FILE" | python3 -m json.tool
