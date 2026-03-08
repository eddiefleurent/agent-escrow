# Codex Buyer Agent — Two-Agent Escrow Demo

You are the **buyer agent** in a two-agent escrow demo. Your job is to post a task,
wait for a worker to bid, accept the bid, fund the escrow, wait for work to be
submitted, and approve it. All on-chain actions go through the escrow server.

A separate **worker agent** process will discover your RFQ, place a bid, and submit work.
You coordinate via shared state files in `demo/runtime/agent-state/`.

## Prerequisites

```bash
make go-cli-install   # build and install escrow-cli (if not already installed)
```

## Environment variables required

```bash
export ESCROW_SERVER_URL=<server-base-url>       # e.g. https://your-server.example.com
export BASE_SEPOLIA_RPC=https://sepolia.base.org
```

## Fixed addresses (Base Sepolia testnet)

| Role | Address |
|------|---------|
| Buyer (server signing key) | `0xA52bd5190B344445d91877c7E1e1a11718A205d1` |
| Verifier | `0x2197e5122d81F544a57DEF921414610e7D66bd98` |
| Arbitrator | `0x0Ee4aa0CAa6974076b85E219835FB54B960Bc8c8` |

## Shared state files

Write intermediate results here so the orchestrator and worker agent can coordinate:

| File | Contents |
|------|---------|
| `demo/runtime/agent-state/rfq_id` | RFQ ID (plain integer, no newline) — write after Step 1 |
| `demo/runtime/agent-state/escrow` | JSON with `escrow_id` and `escrow_address` — write after Step 3 |
| `demo/runtime/agent-state/buyer-done` | Final status JSON — write after Step 8 |

---

## Steps

Run each step in order. Capture stdout. Assert exit code 0 before proceeding.

### Step 0: Health check

```bash
escrow-cli health
```

Assert `status == "ok"`. If the server is unreachable, stop and report the error.

### Step 1: Post an RFQ

```bash
NOW=$(date +%s)
SUBMISSION_DEADLINE=$((NOW + 7200))
EXPIRES_AT=$((NOW + 3600))

escrow-cli rfq create --output json --data "{
  \"title\": \"Two-agent escrow demo task\",
  \"description\": \"Automated demo: buyer and worker Codex agents coordinate via on-chain escrow. Worker delivers a demo artifact and buyer approves. Paper: Intelligent AI Delegation (Tomasev et al., 2026).\",
  \"buyer\": \"0xA52bd5190B344445d91877c7E1e1a11718A205d1\",
  \"verifier\": \"0x2197e5122d81F544a57DEF921414610e7D66bd98\",
  \"arbitrator\": \"0x0Ee4aa0CAa6974076b85E219835FB54B960Bc8c8\",
  \"budget_min\": \"50000000000000\",
  \"budget_max\": \"150000000000000\",
  \"deadline\": \"$SUBMISSION_DEADLINE\",
  \"review_period_seconds\": \"3600\",
  \"dispute_period_seconds\": \"7200\",
  \"arbitrator_timeout_seconds\": \"10800\",
  \"expires_at\": \"$EXPIRES_AT\"
}"
```

Extract `id` from the response → `RFQ_ID`.

**Write RFQ_ID to shared state:**
```bash
printf '%s' "$RFQ_ID" > demo/runtime/agent-state/rfq_id
```

### Step 2: Poll for bids (max 5 minutes, 15-second interval)

```bash
for i in $(seq 1 20); do
  BIDS=$(escrow-cli bid list $RFQ_ID --output json)
  COUNT=$(echo "$BIDS" | python3 -c "import sys,json; data=json.load(sys.stdin); print(len(data) if isinstance(data, list) else 0)" 2>/dev/null || echo "0")
  if [ "$COUNT" -ge 1 ]; then
    echo "Bids received: $COUNT"
    echo "$BIDS"
    break
  fi
  echo "Waiting for bids... attempt $i/20"
  sleep 15
done
```

If no bids after 20 attempts (5 minutes), stop and report: `{"error": "timeout waiting for bids"}`.

Extract `id` of the first bid → `BID_ID`.

### Step 3: Accept the bid (creates escrow on-chain)

```bash
escrow-cli bid accept $RFQ_ID --output json --data "{
  \"bid_id\": $BID_ID,
  \"caller\": \"0xA52bd5190B344445d91877c7E1e1a11718A205d1\"
}"
```

`verifier` and `arbitrator` are taken from the RFQ — do not repeat them here.

Extract `escrow_id` → `ESCROW_ID`. Extract `escrow_address` → `ESCROW_ADDR`.

**Write escrow info to shared state:**
```bash
printf '{"escrow_id": "%s", "escrow_address": "%s"}' "$ESCROW_ID" "$ESCROW_ADDR" > demo/runtime/agent-state/escrow
```

### Step 4: Fund the escrow

```bash
escrow-cli escrow fund $ESCROW_ID --output json
```

Assert exit code 0.

**Poll until `status == "funded"` (max 3 minutes, 15-second interval):**
```bash
for i in $(seq 1 12); do
  STATUS=$(escrow-cli escrow get $ESCROW_ID --output json | python3 -c "import sys,json; print(json.load(sys.stdin).get('status','unknown'))" 2>/dev/null)
  echo "Escrow status: $STATUS (attempt $i/12)"
  if [ "$STATUS" = "funded" ]; then break; fi
  sleep 15
done
```

If not `funded` after 3 minutes, stop with error.

### Step 5: Poll for work submission (max 10 minutes, 20-second interval)

```bash
for i in $(seq 1 30); do
  STATUS=$(escrow-cli escrow get $ESCROW_ID --output json | python3 -c "import sys,json; print(json.load(sys.stdin).get('status','unknown'))" 2>/dev/null)
  echo "Waiting for submission... status: $STATUS (attempt $i/30)"
  if [ "$STATUS" = "submitted" ]; then break; fi
  sleep 20
done
```

If not `submitted` after 10 minutes, stop with error.

### Step 6: Approve the work

```bash
escrow-cli escrow approve $ESCROW_ID --output json --data '{"role": "buyer"}'
```

Assert exit code 0.

### Step 7: Verify settled

```bash
FINAL=$(escrow-cli escrow get $ESCROW_ID --output json)
echo "$FINAL"
STATUS=$(echo "$FINAL" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status','unknown'))" 2>/dev/null)
```

Assert `status == "settled"` (or `"approved"` if indexer lag — re-check after 10s).

### Step 8: Write completion marker

```bash
printf '{"escrow_id": "%s", "escrow_address": "%s", "status": "%s"}' \
  "$ESCROW_ID" "$ESCROW_ADDR" "$STATUS" > demo/runtime/agent-state/buyer-done
echo "Buyer agent done. Escrow $ESCROW_ID settled."
```

---

## Results

Write a summary JSON to stdout in this format:
```json
{
  "agent": "buyer",
  "rfq_id": "<id>",
  "escrow_id": "<id>",
  "escrow_address": "0x...",
  "final_status": "settled",
  "steps_completed": 8
}
```
