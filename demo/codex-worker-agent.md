# Codex Worker Agent — Two-Agent Escrow Demo

You are the **worker agent** in a two-agent escrow demo. A buyer agent has already
posted an RFQ. Your job is to read that RFQ, place a competitive bid, wait until
your bid is accepted and the escrow is funded, submit work, and verify the escrow
reaches `submitted` status.

You submit work by calling `submit(bytes32,string)` directly on-chain using your
own private key — the server's signing key belongs to the buyer, so `cast send`
is required for this step.

## Prerequisites

```bash
make go-cli-install   # build and install escrow-cli (if not already installed)
# cast must be installed: https://book.getfoundry.sh/getting-started/installation
```

## Environment variables required

```bash
export ESCROW_SERVER_URL=<server-base-url>       # e.g. https://your-server.example.com
export BASE_SEPOLIA_RPC=https://sepolia.base.org
export WORKER_PRIVATE_KEY=<0x...>                # worker's private key (from .env)
```

## Fixed addresses (Base Sepolia testnet)

| Role | Address |
|------|---------|
| Worker | `0x13c010aC7cf2bd187adAfEAd2D73E52fF48765e2` |

## Shared state files (written by buyer agent, read by orchestrator)

| File | Contents |
|------|---------|
| `demo/.agent-state/rfq_id` | RFQ ID to bid on — read at startup |
| `demo/.agent-state/worker-done` | Final result — write after Step 6 |

---

## Steps

Run each step in order. Capture stdout. Assert exit code 0 before proceeding.

### Step 0: Read RFQ ID and verify server

```bash
RFQ_ID=$(cat demo/.agent-state/rfq_id)
echo "Using RFQ_ID: $RFQ_ID"

escrow-cli health
```

Assert `status == "ok"`. If the RFQ ID file is missing, wait up to 60 seconds
(checking every 5 seconds) for the buyer agent to write it. If still missing, stop.

### Step 1: Read the RFQ details

```bash
RFQ_JSON=$(escrow-cli rfq get "$RFQ_ID" --output json)
echo "$RFQ_JSON"
BUDGET_MAX=$(echo "$RFQ_JSON" | python3 -c "import sys,json; print(json.load(sys.stdin).get('budget_max',''))")
if [ -z "$BUDGET_MAX" ]; then
  echo "ERROR: RFQ budget_max missing" >&2
  exit 1
fi
```

Use `budget_max` from the RFQ as your bid amount.

### Step 2: Place a bid

```bash
NOW=$(date +%s)
BID_EXPIRES=$((NOW + 3600))

escrow-cli bid place $RFQ_ID --output json --data "{
  \"bidder\": \"0x13c010aC7cf2bd187adAfEAd2D73E52fF48765e2\",
  \"amount\": \"$BUDGET_MAX\",
  \"estimated_duration\": 3600,
  \"message\": \"Worker agent bid for two-agent demo. Will deliver a verifiable artifact demonstrating autonomous task delegation per Tomasev et al. (2026).\",
  \"expires_at\": \"$BID_EXPIRES\"
}"
```

Extract `id` from the response → `BID_ID`.

### Step 3: Poll for escrow assignment (max 5 minutes, 15-second interval)

After the buyer accepts your bid, an escrow will appear in your worker list.

```bash
for i in $(seq 1 20); do
  ESCROWS=$(escrow-cli escrow list --output json --role worker \
    --address 0x13c010aC7cf2bd187adAfEAd2D73E52fF48765e2)
  COUNT=$(echo "$ESCROWS" | python3 -c "import sys,json; data=json.load(sys.stdin); print(len(data) if isinstance(data, list) else 0)" 2>/dev/null || echo "0")
  if [ "$COUNT" -ge 1 ]; then
    echo "Escrow found: $COUNT"
    echo "$ESCROWS"
    break
  fi
  echo "Waiting for escrow... attempt $i/20"
  sleep 15
done
```

If no escrow after 20 attempts (5 minutes), stop with error.

Extract `id` → `ESCROW_ID` and `address` → `ESCROW_ADDR` from the first escrow.

### Step 4: Poll until escrow is funded (max 5 minutes, 15-second interval)

```bash
for i in $(seq 1 20); do
  STATUS=$(escrow-cli escrow get $ESCROW_ID --output json | \
    python3 -c "import sys,json; print(json.load(sys.stdin).get('status','unknown'))" 2>/dev/null)
  echo "Escrow status: $STATUS (attempt $i/20)"
  if [ "$STATUS" = "funded" ]; then break; fi
  sleep 15
done
```

If not `funded` after 5 minutes, stop with error.

### Step 5: Submit work via cast (worker's own key)

Compute the submission hash and call `submit()` on-chain with the worker's private key.
The server's signing key is the buyer's — the contract enforces `msg.sender == activeWorker`,
so this transaction must be signed by the worker directly.

```bash
SUBMISSION_URI="ipfs://QmTwoAgentDemoDeliverable-$(date +%s)"
SUBMISSION_HASH=$(cast keccak "$SUBMISSION_URI")

echo "Submitting: URI=$SUBMISSION_URI HASH=$SUBMISSION_HASH"

cast send "$ESCROW_ADDR" \
  "submit(bytes32,string)" \
  "$SUBMISSION_HASH" \
  "$SUBMISSION_URI" \
  --private-key "$WORKER_PRIVATE_KEY" \
  --rpc-url "$BASE_SEPOLIA_RPC"
```

Assert exit code 0. Record the transaction hash from cast output.

### Step 6: Verify submitted status

```bash
for i in $(seq 1 6); do
  STATUS=$(escrow-cli escrow get $ESCROW_ID --output json | \
    python3 -c "import sys,json; print(json.load(sys.stdin).get('status','unknown'))" 2>/dev/null)
  echo "Post-submit status: $STATUS (attempt $i/6)"
  if [ "$STATUS" = "submitted" ]; then break; fi
  sleep 10
done
```

Assert `status == "submitted"`.

### Step 7: Write completion marker

```bash
printf '{"escrow_id": "%s", "escrow_address": "%s", "submission_uri": "%s", "status": "submitted"}' \
  "$ESCROW_ID" "$ESCROW_ADDR" "$SUBMISSION_URI" > demo/.agent-state/worker-done
echo "Worker agent done. Escrow $ESCROW_ID submitted."
```

---

## Results

Write a summary JSON to stdout in this format:
```json
{
  "agent": "worker",
  "rfq_id": "<id>",
  "bid_id": "<id>",
  "escrow_id": "<id>",
  "escrow_address": "0x...",
  "submission_uri": "ipfs://...",
  "final_status": "submitted"
}
```
