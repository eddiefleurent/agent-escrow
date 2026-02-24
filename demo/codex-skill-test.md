# Codex Skill Test — Full Escrow Lifecycle

This file is a Codex task prompt. Run it with:

```bash
codex exec --dangerously-bypass-approvals-and-sandbox -C . "$(cat demo/codex-skill-test.md)"
```

It demonstrates a complete escrow lifecycle using the `escrow-cli` skill against a live
Base Sepolia deployment: RFQ → bid → accept → fund → submit → approve → settled.

## Prerequisites

```bash
make go-cli-install   # build and install escrow-cli
```

## Environment variables

Set these before running. All are required.

```bash
export ESCROW_SERVER_URL=<server-base-url>       # e.g. https://your-server.example.com
export BASE_SEPOLIA_RPC=https://sepolia.base.org

# Buyer address — must match the server's signing key.
# The server submits all on-chain transactions as this address.
export BUYER_ADDRESS=<0x...>

# Worker — a separate address; must be distinct from buyer, verifier, arbitrator.
# The worker signs submit() directly via cast, so you need the private key.
export WORKER_ADDRESS=<0x...>
export WORKER_PRIVATE_KEY=<0x...>

# Verifier and arbitrator — distinct addresses; unused (buyer-only approval flow).
export VERIFIER_ADDRESS=<0x...>
export ARBITRATOR_ADDRESS=<0x...>
```

> **Testnet only.** All addresses and keys above are for Base Sepolia.
> The worker key must have enough testnet ETH for one gas payment (~0.00001 ETH on Base).

## Why cast for submit?

The server uses a single signing key (the buyer). Solidity's `submit()` checks
`msg.sender == activeWorker`, so the worker must sign that transaction directly.
All other steps go through `escrow-cli`, which routes through the server.

## Contract constraints discovered during development

- All four role addresses (buyer, worker, verifier, arbitrator) must be **distinct** —
  the factory enforces `RolesNotDistinct` on creation.
- `verifier` and `arbitrator` are set on the **RFQ**, not on bid accept.
- `submit(bytes32,string)` rejects `bytes32(0)` — pass `cast keccak` of the URI.

---

## Steps

Run each step in order. After each command:
1. Capture the JSON output (`--output json` where applicable)
2. Assert exit code is 0 — on failure record raw output and stop
3. Extract IDs from the response for the next step

Write all results to `demo/codex-skill-test-results.json` as a JSON array:
```json
[{"step": "<name>", "exit_code": 0, "output": { ... }}, ...]
```
Failed entries include `"error": true, "raw_output": "..."`.

### Step 0: Health check

```bash
escrow-cli health
```

Assert `status == "ok"`.

### Step 1: Post an RFQ

```bash
NOW=$(date +%s)
SUBMISSION_DEADLINE=$((NOW + 7200))
EXPIRES_AT=$((NOW + 3600))

escrow-cli rfq create --output json --data "{
  \"title\": \"Codex skill test task\",
  \"description\": \"Automated test of the full escrow lifecycle via Codex skill\",
  \"buyer\": \"$BUYER_ADDRESS\",
  \"verifier\": \"$VERIFIER_ADDRESS\",
  \"arbitrator\": \"$ARBITRATOR_ADDRESS\",
  \"budget_min\": \"1000000000000000\",
  \"budget_max\": \"1000000000000000\",
  \"deadline\": \"$SUBMISSION_DEADLINE\",
  \"review_period_seconds\": \"3600\",
  \"dispute_period_seconds\": \"7200\",
  \"arbitrator_timeout_seconds\": \"10800\",
  \"expires_at\": \"$EXPIRES_AT\"
}"
```

Extract `id` from the response → `RFQ_ID`.

### Step 2: Place a bid (worker bids on the RFQ)

```bash
NOW=$(date +%s)
BID_EXPIRES=$((NOW + 3600))

escrow-cli bid place $RFQ_ID --output json --data "{
  \"bidder\": \"$WORKER_ADDRESS\",
  \"amount\": \"1000000000000000\",
  \"estimated_duration\": 7200,
  \"message\": \"Automated test bid from Codex skill demo\",
  \"expires_at\": \"$BID_EXPIRES\"
}"
```

Extract `id` from the response → `BID_ID`.

### Step 3: List bids — verify the bid appears

```bash
escrow-cli bid list $RFQ_ID --output json
```

Assert at least one bid is present.

### Step 4: Accept the bid (creates escrow on-chain)

```bash
escrow-cli bid accept $RFQ_ID --output json --data "{
  \"bid_id\": $BID_ID,
  \"caller\": \"$BUYER_ADDRESS\"
}"
```

`verifier` and `arbitrator` are taken from the RFQ — no need to repeat them here.

Extract `escrow_id` → `ESCROW_ID`. Extract `escrow_address` → `ESCROW_ADDR`.

### Step 5: Get escrow — verify Created state

```bash
escrow-cli escrow get $ESCROW_ID --output json
```

Assert status is `created` or `funded`.

### Step 6: Fund the escrow (buyer locks 0.001 ETH on-chain)

```bash
escrow-cli escrow fund $ESCROW_ID --output json
```

Assert exit code 0. Poll `escrow get` until status is `funded`.

### Step 7: Submit work (worker signs directly via cast)

```bash
SUBMISSION_URI="ipfs://QmCodexSkillTestDeliverable"
SUBMISSION_HASH=$(cast keccak "$SUBMISSION_URI")

cast send $ESCROW_ADDR \
  "submit(bytes32,string)" \
  $SUBMISSION_HASH \
  "$SUBMISSION_URI" \
  --private-key $WORKER_PRIVATE_KEY \
  --rpc-url $BASE_SEPOLIA_RPC
```

Assert exit code 0. Poll `escrow get` until status is `submitted`.

### Step 8: Approve the work (buyer approves via escrow-cli)

```bash
escrow-cli escrow approve $ESCROW_ID --output json --data '{"role": "buyer"}'
```

Assert exit code 0.

### Step 9: Final status check

```bash
escrow-cli escrow get $ESCROW_ID --output json
```

Assert status is `approved` or `settled`. Record final status in results.

---

## Results file

Write `demo/codex-skill-test-results.json` (gitignored).
Format: JSON array with one object per step.
