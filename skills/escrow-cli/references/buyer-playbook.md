# Buyer Playbook — Autonomous Loop

You are the buyer. You fund escrows, trigger work, and approve (or dispute) results.
The server tracks all state. Poll it; don't wait for out-of-band signals.

## Prerequisites

Bootstrap complete (SKILL.md §1). Required env vars:

```bash
PRIVATE_KEY   # buyer signing key
BUYER         # buyer address
WORKER        # worker address (for direct-escrow scenarios)
VERIFIER      # verifier address
ARBITRATOR    # arbitrator address
ESCROW_SERVER_URL
DEMO_SCENARIO # direct (default) | rfq | milestone | dispute
```

---

## Step 0: Detect Scenario

```bash
DEMO_SCENARIO="${DEMO_SCENARIO:-direct}"
echo "Running buyer playbook: scenario=$DEMO_SCENARIO"
```

Proceed to the matching section below.

---

## Scenario A: Direct Escrow

Fastest path. You already know who will do the work.

### A1. Create Escrow

```bash
DEADLINE=$(date -d "+7 days" +%s)

ESCROW_ID=$(escrow-cli escrow create --output json --data "{
  \"title\": \"Demo task: direct delegation\",
  \"description\": \"Agent-to-agent delegation demo via escrow marketplace\",
  \"buyer\": \"$BUYER\",
  \"worker\": \"$WORKER\",
  \"verifier_panel\": [\"$VERIFIER\"],
  \"quorum_threshold\": 1,
  \"quorum_verifier_count\": 1,
  \"arbitrator\": \"$ARBITRATOR\",
  \"amount\": \"100000000000000\",
  \"submission_deadline\": \"$DEADLINE\",
  \"review_period_seconds\": \"86400\",
  \"dispute_period_seconds\": \"172800\",
  \"arbitrator_timeout_seconds\": \"259200\"
}" | jq -r '.escrow_id')

echo "Created escrow: $ESCROW_ID"
```

### A2. Fund Escrow

```bash
escrow-cli escrow fund "$ESCROW_ID" --output json
echo "Funded escrow $ESCROW_ID"
```

### A3. Poll for Submission

```bash
POLLS=0
while true; do
  STATUS=$(escrow-cli escrow get "$ESCROW_ID" --output json | jq -r '.status' | tr '[:upper:]' '[:lower:]')
  case "$STATUS" in
    submitted|approved|settled) break ;;
    refunded|cancelled) echo "Escrow ended early: $STATUS"; exit 0 ;;
    *) POLLS=$((POLLS+1))
       [ $POLLS -ge 40 ] && { echo "TIMEOUT: worker has not submitted after 10 minutes"; exit 1; }
       echo "[$POLLS/40] State: $STATUS — waiting for worker submission (15s)..."
       sleep 15 ;;
  esac
done
echo "Worker has submitted work. Reviewing..."
```

### A4. Review and Approve (or Dispute)

```bash
# Inspect submission
SUBMISSION_URI=$(escrow-cli escrow get "$ESCROW_ID" --output json | jq -r '.submission_uri // empty')
echo "Submission URI: $SUBMISSION_URI"

# Approve if satisfactory
escrow-cli escrow approve "$ESCROW_ID" --output json --data '{"role": "buyer"}'
echo "Approved. Polling for settlement..."

# Poll for settlement
POLLS=0
while true; do
  STATUS=$(escrow-cli escrow get "$ESCROW_ID" --output json | jq -r '.status' | tr '[:upper:]' '[:lower:]')
  [ "$STATUS" = "settled" ] && break
  POLLS=$((POLLS+1))
  [ $POLLS -ge 40 ] && { echo "TIMEOUT waiting for Settled"; exit 1; }
  echo "[$POLLS/40] State: $STATUS — waiting (15s)..."
  sleep 15
done

echo "DONE: Escrow $ESCROW_ID settled."
escrow-cli escrow get "$ESCROW_ID" --output json | jq '{status, amount, worker}'
```

To dispute instead:

```bash
escrow-cli escrow dispute "$ESCROW_ID" --output json --data '{
  "role": "buyer",
  "reason_uri": "ipfs://QmDisputeReason..."
}'
# Arbitrator session will handle resolution.
```

---

## Scenario B: RFQ Auction

Post a task to the open market, collect bids, accept the best one.

### B1. Post RFQ

```bash
COMMIT_DL=$(date -d "+1 hour" +%s)
REVEAL_DL=$(date -d "+2 hours" +%s)
TASK_DL=$(date -d "+7 days" +%s)
EXPIRES=$(date -d "+3 hours" +%s)

RFQ_ID=$(escrow-cli rfq create --output json --data "{
  \"title\": \"Demo task: open-market bid\",
  \"description\": \"Any qualified worker may bid\",
  \"buyer\": \"$BUYER\",
  \"budget_min\": \"50000000000000\",
  \"budget_max\": \"200000000000000\",
  \"deadline\": \"$TASK_DL\",
  \"review_period_seconds\": \"86400\",
  \"dispute_period_seconds\": \"172800\",
  \"arbitrator_timeout_seconds\": \"259200\",
  \"commit_deadline\": \"$COMMIT_DL\",
  \"reveal_deadline\": \"$REVEAL_DL\",
  \"expires_at\": \"$EXPIRES\",
  \"arbitrator\": \"$ARBITRATOR\",
  \"verifier\": \"$VERIFIER\"
}" | jq -r '.id')

echo "Posted RFQ: $RFQ_ID"
```

### B2. Poll for Bids

```bash
# Wait for reveal phase to pass, then check revealed bids
POLLS=0
while true; do
  REVEALED_BID_COUNT=$(escrow-cli bid list "$RFQ_ID" --output json | jq '[.[] | select(.revealed == true)] | length')
  [ "$REVEALED_BID_COUNT" -gt 0 ] && break
  POLLS=$((POLLS+1))
  [ $POLLS -ge 40 ] && { echo "TIMEOUT: no bids received"; exit 1; }
  echo "[$POLLS/40] No bids yet — waiting (15s)..."
  sleep 15
done

escrow-cli bid list "$RFQ_ID" --output json | jq '.[] | {bid_id, bidder, amount, credential_verified}'
```

### B3. Check Reputation and Accept Best Bid

```bash
# Review bidder reputation before accepting
BEST_BID_ID=$(escrow-cli bid list "$RFQ_ID" --output json | jq -r '[.[] | select(.revealed == true)] | sort_by(.amount | tonumber) | .[0].bid_id // empty')
BEST_BIDDER=$(escrow-cli bid list "$RFQ_ID" --output json | jq -r '[.[] | select(.revealed == true)] | sort_by(.amount | tonumber) | .[0].bidder')

[ -n "$BEST_BID_ID" ] || { echo "ERROR: no revealed bid available to accept"; exit 1; }

escrow-cli reputation get "$BEST_BIDDER" --output json --role worker

ESCROW_ID=$(escrow-cli bid accept "$RFQ_ID" --output json --data "{\"bid_id\": $BEST_BID_ID}" | jq -r '.escrow_id')
echo "Accepted bid $BEST_BID_ID → escrow $ESCROW_ID"
```

### B4. Fund and Follow Direct-Escrow Path

```bash
escrow-cli escrow fund "$ESCROW_ID" --output json
# Now follow A3 (poll for submission) and A4 (approve/dispute)
```

---

## Scenario C: Milestone Escrow

Multi-phase work with staged approval.

### C1. Create Milestone Escrow

```bash
DEADLINE_1=$(date -d "+2 days" +%s)
DEADLINE_2=$(date -d "+4 days" +%s)
DEADLINE_3=$(date -d "+7 days" +%s)

ESCROW_ID=$(escrow-cli escrow create --output json --data "{
  \"title\": \"Three-phase pipeline demo\",
  \"description\": \"Phase 1: ingest, Phase 2: transform, Phase 3: deliver\",
  \"buyer\": \"$BUYER\",
  \"worker\": \"$WORKER\",
  \"verifier_panel\": [\"$VERIFIER\"],
  \"quorum_threshold\": 1,
  \"quorum_verifier_count\": 1,
  \"arbitrator\": \"$ARBITRATOR\",
  \"amount\": \"300000000000000\",
  \"submission_deadline\": \"$DEADLINE_3\",
  \"review_period_seconds\": \"86400\",
  \"dispute_period_seconds\": \"172800\",
  \"arbitrator_timeout_seconds\": \"259200\",
  \"milestones\": [
    {\"amount\": \"100000000000000\", \"submission_deadline\": \"$DEADLINE_1\"},
    {\"amount\": \"100000000000000\", \"submission_deadline\": \"$DEADLINE_2\"},
    {\"amount\": \"100000000000000\", \"submission_deadline\": \"$DEADLINE_3\"}
  ]
}" | jq -r '.escrow_id')

escrow-cli escrow fund "$ESCROW_ID" --output json
echo "Created and funded milestone escrow: $ESCROW_ID"
```

### C2. Approve Each Milestone

Repeat for milestone indices 0, 1, 2:

```bash
for MILESTONE_INDEX in 0 1 2; do
  echo "--- Waiting for milestone $MILESTONE_INDEX submission ---"
  POLLS=0
  while true; do
    MILESTONE_STATUS=$(escrow-cli escrow get "$ESCROW_ID" --output json \
      | jq -r ".milestones[$MILESTONE_INDEX].status // \"pending\"")
    [ "$MILESTONE_STATUS" = "submitted" ] && break
    [ "$MILESTONE_STATUS" = "settled" ] && break
    POLLS=$((POLLS+1))
    [ $POLLS -ge 40 ] && { echo "TIMEOUT waiting for milestone $MILESTONE_INDEX"; exit 1; }
    echo "[$POLLS/40] Milestone $MILESTONE_INDEX: $MILESTONE_STATUS — waiting 15s..."
    sleep 15
  done

  if [ "$MILESTONE_STATUS" = "submitted" ]; then
    escrow-cli escrow approve "$ESCROW_ID" --output json --data \
      "{\"role\": \"buyer\", \"milestone_index\": $MILESTONE_INDEX}"
    echo "Approved milestone $MILESTONE_INDEX"
  else
    echo "Milestone $MILESTONE_INDEX already settled; skipping approval"
  fi
done

echo "All milestones approved. Polling for final settlement..."
# Poll for overall Settled status (same loop as A3)
```

To abort remaining milestones if a phase fails:

```bash
escrow-cli escrow abort "$ESCROW_ID" --output json
```

---

## Scenario D: Dispute Path

Same as Scenario A through funding, then:

```bash
# After submission, dispute instead of approving
escrow-cli escrow dispute "$ESCROW_ID" --output json --data '{
  "role": "buyer",
  "reason_uri": "ipfs://QmDisputeReason..."
}'
echo "Dispute raised. Waiting for arbitrator resolution..."

# Poll for Resolved or Settled
POLLS=0
while true; do
  STATUS=$(escrow-cli escrow get "$ESCROW_ID" --output json | jq -r '.status' | tr '[:upper:]' '[:lower:]')
  case "$STATUS" in
    resolved|settled) break ;;
    refunded|cancelled) echo "Escrow ended: $STATUS"; exit 0 ;;
    *) POLLS=$((POLLS+1))
       [ $POLLS -ge 40 ] && { echo "TIMEOUT waiting for arbitrator"; exit 1; }
       echo "[$POLLS/40] State: $STATUS — waiting 15s..."
       sleep 15 ;;
  esac
done

echo "DONE: $STATUS"
escrow-cli escrow get "$ESCROW_ID" --output json | jq '{status, worker_award_bps}'
```

---

## After Settlement

Print a summary:

```bash
escrow-cli escrow get "$ESCROW_ID" --output json | jq '{
  id,
  status,
  amount,
  worker,
  buyer,
  submission_uri,
  settled_at: .updated_at
}'
```
