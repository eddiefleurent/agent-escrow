# Multi-Role Demo Guide

How to run a complete escrow lifecycle demo with independent agent sessions — one per role.
The HTTP server is the shared coordination layer. Sessions discover state by polling;
they don't need to communicate with each other.

## Prerequisites

- Server running: `make go-run` (or point at a live Base Sepolia deployment)
- `escrow-cli` installed: `make go-cli-install`
- Four funded testnet addresses (buyer, worker, verifier, arbitrator)
- `~/.config/agent-escrow/escrow-cli.env` configured (see SKILL.md §1)

```bash
# Verify everything works before launching sessions
escrow-cli health
```

---

## Option 1: Three-Session Role-Separated Demo (Recommended)

Open three terminals. Each runs an independent agent session.

### Terminal 1 — Buyer Session

```bash
export ESCROW_ROLE=buyer
export DEMO_SCENARIO=direct    # direct | rfq | milestone | dispute

# Launch Claude Code and invoke the escrow-cli skill:
# "I am the buyer in an escrow demo. Use the escrow-cli skill."
```

The buyer will:
1. Create and fund a direct escrow with the worker address from env
2. Poll for worker submission
3. Approve and wait for settlement

### Terminal 2 — Worker Session

```bash
export ESCROW_ROLE=worker

# Launch Claude Code and invoke the escrow-cli skill:
# "I am the worker in an escrow demo. Use the escrow-cli skill."
```

The worker will:
1. Poll for funded escrows assigned to its address
2. Deposit stake if required
3. Execute work, write output to `/tmp/escrow-deliverable-<id>.txt`
4. Submit with proof hash
5. Wait for payment

### Terminal 3 — Verifier Session

```bash
export ESCROW_ROLE=verifier

# Launch Claude Code and invoke the escrow-cli skill:
# "I am the verifier in an escrow demo. Use the escrow-cli skill."
```

The verifier will:
1. Poll for submitted escrows where it is in the verifier panel
2. Inspect the deliverable file at the path in `submission_uri`
3. Approve (or reject, which triggers arbitrator session)

### Coordination

Sessions coordinate only through server state:

```
Buyer creates escrow
  → Server: status=Created
Buyer funds escrow
  → Server: status=Funded
Worker discovers it (polls escrow list)
  → Worker submits
  → Server: status=Submitted
Verifier discovers it (polls escrow list)
  → Verifier approves
  → Server: status=Approved → Settled
Buyer session wakes from poll
  → Prints "Settled: payment released"
```

---

## Option 2: Four-Session Demo with Dispute

Add a fourth terminal for the arbitrator when you want to test the dispute path.

```bash
# Terminal 1 — Buyer (with DEMO_SCENARIO=dispute)
export ESCROW_ROLE=buyer
export DEMO_SCENARIO=dispute

# Terminal 2 — Worker (same as above)
export ESCROW_ROLE=worker

# Terminal 3 — Verifier (will reject work, raising dispute)
export ESCROW_ROLE=verifier

# Terminal 4 — Arbitrator
export ESCROW_ROLE=arbitrator
```

Flow:

```
Buyer creates + funds escrow
Worker submits
Verifier rejects → escrow enters Disputed
Arbitrator discovers dispute, reviews, resolves with worker_award_bps
Settlement follows resolution
```

---

## Option 3: Single-Session All-Roles Demo

One Claude session plays all roles sequentially. Useful for CI/scripted demos.

```bash
export ESCROW_ROLE=all
```

Explicit operation order:

```bash
# 1. Create and fund (buyer role)
export PRIVATE_KEY="$PRIVATE_KEY"   # buyer key
ESCROW_ID=$(escrow-cli escrow create --output json --data '{...}' | jq -r '.escrow_id')
escrow-cli escrow fund "$ESCROW_ID" --output json

# 2. Submit (switch to worker key)
export PRIVATE_KEY="$WORKER_KEY"
echo "Demo deliverable" > /tmp/escrow-deliverable-${ESCROW_ID}.txt
PROOF_HASH=$(sha256sum /tmp/escrow-deliverable-${ESCROW_ID}.txt | awk '{print "0x"$1}')
escrow-cli escrow submit "$ESCROW_ID" --output json --data "{
  \"submission_uri\": \"file:///tmp/escrow-deliverable-${ESCROW_ID}.txt\",
  \"proof_hash\": \"$PROOF_HASH\"
}"

# 3. Verify and approve (switch to verifier key)
export PRIVATE_KEY="$VERIFIER_KEY"
escrow-cli escrow approve "$ESCROW_ID" --output json --data '{"role": "verifier"}'

# 4. Final buyer approval (switch back to buyer key)
export PRIVATE_KEY="$PRIVATE_KEY_BUYER"  # save buyer key above as PRIVATE_KEY_BUYER
escrow-cli escrow approve "$ESCROW_ID" --output json --data '{"role": "buyer"}'

# 5. Check result
escrow-cli escrow get "$ESCROW_ID" --output json | jq '{status, amount}'
```

---

## Option 4: RFQ Auction Demo

Tests the full bid lifecycle. Requires timing coordination for commit/reveal windows.

```bash
# Use short commit/reveal windows for demos (1 minute each):
COMMIT_DL=$(date -d "+1 minute" +%s)
REVEAL_DL=$(date -d "+2 minutes" +%s)

# Buyer: post RFQ with short windows
# Worker: commit bid, wait 1 minute, reveal bid
# Buyer: list bids, accept best, fund → continues as direct escrow
```

---

## Scenario Environment Variables

| Variable | Values | Description |
|----------|--------|-------------|
| `ESCROW_ROLE` | `buyer\|worker\|verifier\|arbitrator\|all` | Role for this session |
| `DEMO_SCENARIO` | `direct\|rfq\|milestone\|dispute` | Buyer scenario selector |
| `ESCROW_ID` | numeric | Pre-set to skip discovery and work on a specific escrow |
| `ESCROW_SERVER_URL` | URL | Override server endpoint |

---

## V3 Demo: Decomposition → RFQ Pipeline

Use the decomposition API to break a complex task into sub-tasks, then run the RFQ
auction flow for each leaf node.

```bash
# 1. Create decomposition
DECOMP_ID=$(escrow-cli decomposition create --output json --json "{
  \"title\": \"Build recommendation system\",
  \"description\": \"End-to-end ML recommendation pipeline\",
  \"buyer\": \"$BUYER\",
  \"sub_tasks\": [
    {
      \"temp_id\": \"data\",
      \"parent_temp_id\": \"\",
      \"title\": \"Data ingestion\",
      \"description\": \"ETL pipeline from raw sources\",
      \"verification_type\": \"unit_test\",
      \"delegate_preference\": \"ai\",
      \"requires_further_decomposition\": false
    },
    {
      \"temp_id\": \"model\",
      \"parent_temp_id\": \"\",
      \"title\": \"Model training\",
      \"description\": \"Train collaborative filtering model\",
      \"verification_type\": \"optimistic\",
      \"delegate_preference\": \"any\",
      \"requires_further_decomposition\": false
    }
  ]
}" | jq -r '.decomposition_id')

echo "Decomposition: $DECOMP_ID"

# 2. Check for structural issues
escrow-cli decomposition get "$DECOMP_ID" --output json | jq '{status, structural_issues, market_context}'

# 3. Finalize (creates one RFQ per leaf sub-task)
EXPIRES=$(date -d "+1 day" +%s)
DEADLINE=$(date -d "+7 days" +%s)

escrow-cli decomposition finalize "$DECOMP_ID" --output json --json "{
  \"buyer\": \"$BUYER\",
  \"token\": \"0x0000000000000000000000000000000000000000\",
  \"deadline\": \"$DEADLINE\",
  \"review_period_seconds\": \"86400\",
  \"dispute_period_seconds\": \"172800\",
  \"arbitrator_timeout_seconds\": \"259200\",
  \"expires_at\": \"$EXPIRES\"
}" | jq '.rfq_ids'
# Returns array of RFQ IDs — run worker bid flow for each
```

---

## Deliverable Storage

For single-host demos, local file paths work as URIs. The verifier reads the same path.

For multi-machine demos, use a shared store:
- **IPFS**: pin with `ipfs add`, use `ipfs://Qm...` URI
- **HTTP**: host on any accessible server
- **Cloud storage**: S3-compatible presigned URL

The server stores URIs opaquely — it does not fetch or validate them.
