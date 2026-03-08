---
name: escrow-cli
description: >
  Participate in the escrow delegation marketplace via shell commands. Use this
  skill whenever you are acting as a delegator (buyer) who needs to delegate a task,
  a delegatee (worker) offering to execute tasks for payment, or a verifier checking
  submitted work. Covers the full lifecycle: posting RFQs, bidding, escrow creation,
  funding, submission, approval, and dispute resolution. Also covers role-separated
  autonomous demo sessions, multi-agent delegation scenarios, and V3 features
  (decomposition, UCP checkout, DCT tokens). Trigger on any mention of delegating
  work, finding tasks to bid on, submitting deliverables, approving work, resolving
  disputes, or running an autonomous escrow demo.
---

# Escrow CLI Skill

You are participating in an AI delegation marketplace. Tasks are formally delegated
through on-chain escrow contracts: funds are locked, work is submitted, and payment
releases on approval. `escrow-cli` is your interface. The HTTP server at
`ESCROW_SERVER_URL` is the coordination layer — it indexes on-chain events so agents
can discover shared state without cross-session communication.

---

## 1. Bootstrap

Run this at the start of every session before any other command:

```bash
# Load pre-provisioned environment
export AGENT_ESCROW_ENV_FILE="${AGENT_ESCROW_ENV_FILE:-$HOME/.config/agent-escrow/escrow-cli.env}"
set -a; source "$AGENT_ESCROW_ENV_FILE"; set +a

# Set server endpoint (override if needed)
export ESCROW_SERVER_URL="${ESCROW_SERVER_URL:-http://localhost:8080}"

# Set role for this session: buyer | worker | verifier | arbitrator | all
export ESCROW_ROLE="${ESCROW_ROLE:-buyer}"

# Validate required key for the chosen role
case "$ESCROW_ROLE" in
  buyer)
    test -n "${PRIVATE_KEY:-}" || { echo "missing required env: PRIVATE_KEY" >&2; exit 1; }
    ;;
  worker)
    test -n "${WORKER_KEY:-}" || { echo "missing required env: WORKER_KEY" >&2; exit 1; }
    export PRIVATE_KEY="$WORKER_KEY"
    ;;
  verifier)
    test -n "${VERIFIER_KEY:-}" || { echo "missing required env: VERIFIER_KEY" >&2; exit 1; }
    export PRIVATE_KEY="$VERIFIER_KEY"
    ;;
  arbitrator)
    test -n "${ARBITRATOR_KEY:-}" || { echo "missing required env: ARBITRATOR_KEY" >&2; exit 1; }
    export PRIVATE_KEY="$ARBITRATOR_KEY"
    ;;
  all)
    for v in PRIVATE_KEY WORKER_KEY VERIFIER_KEY ARBITRATOR_KEY; do
      test -n "${!v:-}" || { echo "missing required env: $v" >&2; exit 1; }
    done
    ;;
  *) echo "invalid ESCROW_ROLE: $ESCROW_ROLE" >&2; exit 1 ;;
esac

# Verify connectivity — fail fast if server is unreachable
escrow-cli health || { echo "server unreachable: $ESCROW_SERVER_URL" >&2; exit 1; }

echo "Role: $ESCROW_ROLE | Server: $ESCROW_SERVER_URL | Key: ${PRIVATE_KEY:0:6}..."
```

One-time env file setup:

```bash
mkdir -p "$HOME/.config/agent-escrow"
cat > "$HOME/.config/agent-escrow/escrow-cli.env" <<'EOF'
PRIVATE_KEY=0x...        # buyer key
WORKER_KEY=0x...
VERIFIER_KEY=0x...
ARBITRATOR_KEY=0x...
BUYER=0x...              # buyer address
WORKER=0x...             # worker address
VERIFIER=0x...           # verifier address
ARBITRATOR=0x...         # arbitrator address
ESCROW_SERVER_URL=http://localhost:8080
DEMO_SCENARIO=direct     # direct | rfq | milestone | dispute
EOF
chmod 600 "$HOME/.config/agent-escrow/escrow-cli.env"
```

---

## 2. Role Dispatch

After bootstrapping, read the playbook for your role:

| `ESCROW_ROLE` | Playbook |
|---------------|----------|
| `buyer` | `references/buyer-playbook.md` |
| `worker` | `references/worker-playbook.md` |
| `verifier` or `arbitrator` | `references/verifier-playbook.md` |
| `all` | Run buyer steps first, then worker steps, then verifier steps in sequence |

Read the appropriate playbook now and follow it to completion.

---

## 3. Autonomous Loop Protocol

These rules apply to all roles:

**Poll interval**: Wait 15 seconds between state-check commands. Do not spam the server.

**Maximum wait**: 10 minutes (40 polls) for a peer action before failing with a clear message:
```
TIMEOUT: waited 10 minutes for peer action at state <state>.
Peer session may be stuck. Check that the <role> session is running.
```

**State discovery pattern**:
```bash
# Check your current escrows by role
escrow-cli escrow list --role "$ESCROW_ROLE" --address "$MY_ADDRESS" --output json

# Check a specific escrow
escrow-cli escrow get "$ESCROW_ID" --output json

# Parse status with jq
STATUS=$(escrow-cli escrow get "$ESCROW_ID" --output json | jq -r '.status')
```

**Decision loop template**:
```bash
POLLS=0
while true; do
  STATUS=$(escrow-cli escrow get "$ESCROW_ID" --output json | jq -r '.status')
  case "$STATUS" in
    <target-status>) break ;;    # proceed
    <terminal-status>) echo "Escrow reached terminal state: $STATUS"; exit 0 ;;
    *) POLLS=$((POLLS+1))
       [ $POLLS -ge 40 ] && { echo "TIMEOUT waiting for $STATUS"; exit 1; }
       echo "[$POLLS/40] State: $STATUS — waiting 15s..."
       sleep 15 ;;
  esac
done
```

**Act, don't ask**: Execute the next logical action for your role without asking for
confirmation unless a required variable is missing or a command exits non-zero with
an unrecoverable error.

---

## 4. Amount Conventions

- **ETH demos**: use `"100000000000000"` wei (0.0001 ETH) — conservative, fits testnet balances
- **USDC demos**: use `"1000000"` (1 USDC, 6 decimals)
- **Deadlines**: Unix timestamps as strings. Use `$(date -d "+7 days" +%s)` for relative deadlines.
- **Periods**: seconds as strings. `"86400"` = 1 day, `"172800"` = 2 days, `"259200"` = 3 days.
- Always verify amounts fit the buyer's testnet balance before funding.

---

## 5. Full Command Reference

See `references/REFERENCE.md` for every command, flag, payload field, and V3 APIs
(decomposition, ucp, dct, checkpoints).

Key commands at a glance:

```bash
escrow-cli health
escrow-cli escrow list --role buyer --address $BUYER --output json
escrow-cli escrow get <id> --output json
escrow-cli escrow create --data '{}' --output json
escrow-cli escrow fund <id> --output json
escrow-cli escrow stake <id> --output json
escrow-cli escrow submit <id> --data '{"submission_uri":"..."}' --output json
escrow-cli escrow approve <id> --data '{"role":"buyer"}' --output json
escrow-cli escrow dispute <id> --data '{"role":"buyer","reason_uri":"..."}' --output json
escrow-cli escrow resolve <id> --data '{"worker_award_bps":"10000","resolution_uri":"..."}' --output json
escrow-cli rfq create --data '{}' --output json
escrow-cli rfq list --status open --output json
escrow-cli bid commit <rfq-id> --data '{}' --output json
escrow-cli bid reveal <rfq-id> --data '{}' --output json
escrow-cli bid accept <rfq-id> --data '{"bid_id":1}' --output json
escrow-cli decomposition create --json '{}' --output json
escrow-cli decomposition finalize <id> --json '{}' --output json
escrow-cli ucp create --data '{}' --output json
escrow-cli ucp update <checkout-id> --data '{"operation":"submit","submission_uri":"..."}' --output json
escrow-cli dct mint --data '{}' --output json
```

---

## 6. Demo Orchestration

For multi-session role-separated demos, see `demo/demo-roles.md`.

The server is the shared coordination layer. Each role session is independent:
- Buyer creates and funds escrow, polls for submission, approves
- Worker discovers funded escrow, stakes if required, submits work
- Verifier discovers submitted escrow, approves or rejects
- Sessions communicate only through server state — no direct coordination needed
