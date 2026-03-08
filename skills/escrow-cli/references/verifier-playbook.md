# Verifier / Arbitrator Playbook — Autonomous Loop

This playbook covers both roles:
- **Verifier**: reviews submitted work and approves or rejects it
- **Arbitrator**: resolves disputes when verifier rejects or buyer disputes

The `ESCROW_ROLE` env var controls which role is active (`verifier` or `arbitrator`).

## Prerequisites

Bootstrap complete (SKILL.md §1). Required env vars:

```bash
# Verifier session:
VERIFIER_KEY    # verifier signing key (becomes PRIVATE_KEY after bootstrap)
PRIVATE_KEY     # set by bootstrap from VERIFIER_KEY
VERIFIER        # verifier address

# Arbitrator session:
ARBITRATOR_KEY  # arbitrator signing key
PRIVATE_KEY     # set by bootstrap from ARBITRATOR_KEY
ARBITRATOR      # arbitrator address

ESCROW_SERVER_URL
```

---

## Verifier Loop

### V1. Discover Submitted Escrows

```bash
MY_ADDRESS="$VERIFIER"

escrow-cli escrow list --output json --role verifier --address "$MY_ADDRESS" --status submitted \
  | jq '.[] | {id, title, submission_uri, buyer, worker}'
```

If none are ready yet, poll:

```bash
POLLS=0
while true; do
  COUNT=$(escrow-cli escrow list --output json --role verifier --address "$MY_ADDRESS" --status submitted \
    | jq 'length')
  [ "$COUNT" -gt 0 ] && break
  POLLS=$((POLLS+1))
  [ $POLLS -ge 40 ] && { echo "TIMEOUT: no submitted escrows found after 10 minutes"; exit 1; }
  echo "[$POLLS/40] No submitted escrows — waiting 15s..."
  sleep 15
done
```

### V2. Inspect the Submission

```bash
# Get the first submitted escrow (or use ESCROW_ID if already known)
if [ -z "${ESCROW_ID:-}" ]; then
  ESCROW_ID=$(escrow-cli escrow list --output json --role verifier --address "$MY_ADDRESS" --status submitted \
    | jq -r '.[0].id')
fi

echo "Reviewing escrow: $ESCROW_ID"
ESCROW_JSON=$(escrow-cli escrow get "$ESCROW_ID" --output json)

echo "$ESCROW_JSON" | jq '{id, title, description, submission_uri, proof_hash}'

# If submission_uri is a local file path, inspect the deliverable
SUBMISSION_URI=$(echo "$ESCROW_JSON" | jq -r '.submission_uri // empty')
if echo "$SUBMISSION_URI" | grep -q "^file://"; then
  DELIVERABLE_PATH="${SUBMISSION_URI#file://}"
  echo "--- Deliverable content ---"
  cat "$DELIVERABLE_PATH"
  echo "---"

  # Verify proof hash if present
  PROOF_HASH=$(echo "$ESCROW_JSON" | jq -r '.proof_hash // empty')
  if [ -n "$PROOF_HASH" ]; then
    COMPUTED_HASH=$(sha256sum "$DELIVERABLE_PATH" | awk '{print "0x"$1}')
    if [ "$COMPUTED_HASH" = "$PROOF_HASH" ]; then
      echo "Proof hash verified."
    else
      echo "WARNING: proof hash mismatch. Expected $PROOF_HASH, got $COMPUTED_HASH"
    fi
  fi
fi
```

### V3. Approve or Reject

**Single-verifier approval** (`quorum_threshold == 1`):

```bash
# Approve
escrow-cli escrow approve "$ESCROW_ID" --output json --data '{"role": "verifier"}'
echo "Approved escrow $ESCROW_ID"
```

```bash
# Reject (raises dispute, goes to arbitrator)
escrow-cli escrow dispute "$ESCROW_ID" --output json --data '{
  "role": "verifier",
  "reason_uri": "ipfs://QmRejectionReason..."
}'
echo "Rejected escrow $ESCROW_ID — dispute raised for arbitrator"
```

**Quorum verifier** (`quorum_threshold > 1`):

```bash
# Check quorum threshold
QUORUM=$(echo "$ESCROW_JSON" | jq -r '.quorum_threshold')
echo "Quorum threshold: $QUORUM"

if [ "$QUORUM" -gt 1 ]; then
  # Use quorum-vote instead of approve
  escrow-cli escrow quorum-vote "$ESCROW_ID" --output json --data '{
    "approve": true,
    "role": "verifier"
  }'
  echo "Quorum vote cast (approve=true)"
else
  escrow-cli escrow approve "$ESCROW_ID" --output json --data '{"role": "verifier"}'
fi
```

**Verifier stake** (if configured):

```bash
VERIFIER_STAKE=$(echo "$ESCROW_JSON" | jq -r '.verifier_stake_per_verifier // "0"')
if [ "$VERIFIER_STAKE" != "0" ] && [ -n "$VERIFIER_STAKE" ]; then
  echo "Depositing verifier stake: $VERIFIER_STAKE wei"
  escrow-cli escrow verifier-stake "$ESCROW_ID" --output json
fi
```

### V4. Milestone Verification

For milestone escrows, verify each milestone separately:

```bash
MILESTONE_COUNT=$(echo "$ESCROW_JSON" | jq '.milestones | length // 0')

for MILESTONE_INDEX in $(seq 0 $((MILESTONE_COUNT - 1))); do
  # Wait for this milestone to be submitted
  POLLS=0
  while true; do
    M_STATUS=$(escrow-cli escrow get "$ESCROW_ID" --output json \
      | jq -r ".milestones[$MILESTONE_INDEX].status // \"pending\"")
    [ "$M_STATUS" = "submitted" ] && break
    [ "$M_STATUS" = "settled" ] && { echo "Milestone $MILESTONE_INDEX already settled"; break; }
    POLLS=$((POLLS+1))
    [ $POLLS -ge 40 ] && { echo "TIMEOUT waiting for milestone $MILESTONE_INDEX submission"; exit 1; }
    echo "[$POLLS/40] Milestone $MILESTONE_INDEX: $M_STATUS — waiting 15s..."
    sleep 15
  done

  echo "Approving milestone $MILESTONE_INDEX"
  escrow-cli escrow approve "$ESCROW_ID" --output json --data \
    "{\"role\": \"verifier\", \"milestone_index\": $MILESTONE_INDEX}"
done
```

---

## Arbitrator Loop

The arbitrator resolves disputes when the buyer or verifier has rejected work.

### A1. Discover Disputed Escrows

```bash
MY_ADDRESS="$ARBITRATOR"

escrow-cli escrow list --output json --role arbitrator --address "$MY_ADDRESS" --status disputed \
  | jq '.[] | {id, title, buyer, worker, reason_uri}'
```

Poll if not ready:

```bash
POLLS=0
while true; do
  COUNT=$(escrow-cli escrow list --output json --role arbitrator --address "$MY_ADDRESS" --status disputed \
    | jq 'length')
  [ "$COUNT" -gt 0 ] && break
  POLLS=$((POLLS+1))
  [ $POLLS -ge 40 ] && { echo "TIMEOUT: no disputed escrows found after 10 minutes"; exit 1; }
  echo "[$POLLS/40] No disputed escrows — waiting 15s..."
  sleep 15
done
```

### A2. Review the Dispute

```bash
if [ -z "${ESCROW_ID:-}" ]; then
  ESCROW_ID=$(escrow-cli escrow list --output json --role arbitrator --address "$MY_ADDRESS" --status disputed \
    | jq -r '.[0].id')
fi

echo "Reviewing dispute: escrow $ESCROW_ID"
escrow-cli escrow get "$ESCROW_ID" --output json | jq '{
  id, title, status, buyer, worker, amount,
  submission_uri, proof_hash, reason_uri
}'
```

### A3. Resolve Dispute

Award the full amount to the worker if work is satisfactory:

```bash
escrow-cli escrow resolve "$ESCROW_ID" --output json --data '{
  "worker_award_bps": "10000",
  "resolution_uri": "ipfs://QmResolutionDecision..."
}'
echo "Resolved: full payment to worker"
```

Award nothing to the worker (full refund to buyer):

```bash
escrow-cli escrow resolve "$ESCROW_ID" --output json --data '{
  "worker_award_bps": "0",
  "resolution_uri": "ipfs://QmResolutionDecision..."
}'
echo "Resolved: full refund to buyer"
```

Split payment (70% to worker):

```bash
escrow-cli escrow resolve "$ESCROW_ID" --output json --data '{
  "worker_award_bps": "7000",
  "resolution_uri": "ipfs://QmSplitDecision..."
}'
echo "Resolved: 70% to worker, 30% to buyer"
```

For milestone dispute resolution, add `"milestone_index": <n>`.

### A4. Emergency Override (Owner Only)

If the escrow is frozen or requires owner intervention:

```bash
escrow-cli emergency resolve --data '{"escrow_id": "'$ESCROW_ID'", "worker_award_bps": "5000"}'
```

For full emergency operations, see `REFERENCE.md § emergency`.

---

## Poll for Settlement

After approval or resolution, verify the escrow settles:

```bash
POLLS=0
while true; do
  STATUS=$(escrow-cli escrow get "$ESCROW_ID" --output json | jq -r '.status')
  case "$STATUS" in
    Settled|Refunded|Resolved) echo "DONE: $STATUS"; break ;;
    *) POLLS=$((POLLS+1))
       [ $POLLS -ge 40 ] && { echo "TIMEOUT waiting for final state"; exit 1; }
       echo "[$POLLS/40] State: $STATUS — waiting 15s..."
       sleep 15 ;;
  esac
done

escrow-cli escrow get "$ESCROW_ID" --output json | jq '{status, worker_award_bps}'
```
