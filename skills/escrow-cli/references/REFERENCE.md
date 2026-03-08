# Escrow CLI -- Complete Reference

Binary: `go-server/bin/escrow-cli` (or `escrow-cli` if installed via `make go-cli-install`)

## Global Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--server <url>` | `$ESCROW_SERVER_URL` or `http://localhost:8080` | Server base URL |
| `--output text\|json` | `text` | Output format |
| `--timeout <duration>` | `0` (disabled) | Per-request timeout (e.g. `30s`, `2m`) |

## Request Body Flags

Available on commands marked "body required":

| Flag | Description |
|------|-------------|
| `--data '<json>'` | Inline JSON string |
| `--data-file <path>` | Path to a JSON file |

Exactly one of `--data` or `--data-file` must be provided when a body is required.

---

## Command Tree

### `health`

Check API and chain connectivity.

```
GET /api/v1/health
```

---

### `escrow`

#### `escrow create` (body required)

```
POST /api/v1/escrows
```

Required fields: `title`, `description`, `buyer`, `worker`, `verifier_panel` (1-7 addresses), `quorum_threshold`, `quorum_verifier_count`, `arbitrator`, `amount`, `submission_deadline`, `review_period_seconds`, `dispute_period_seconds`, `arbitrator_timeout_seconds`

Optional fields: `worker_stake`, `token`, `milestones` (array of `{amount, submission_deadline}`), `backup_worker`, `backup_deadline_extension`, `zk_verifier`, `circuit_id`, `parent_escrow_id` (numeric DB escrow ID for sub-delegation linkage)

All numeric values are strings. Amounts in wei. Deadlines as Unix timestamps. Periods in seconds.

#### `escrow get <id>`

```
GET /api/v1/escrows/{id}
```

Returns escrow details including milestone state if applicable.

#### `escrow list`

```
GET /api/v1/escrows
```

| Flag | Description |
|------|-------------|
| `--role <role>` | Filter: `buyer`, `worker`, `verifier`, `arbitrator` |
| `--address <addr>` | Filter by participant address |
| `--status <status>` | Filter by state: `created`, `funded`, `submitted`, `approved`, `disputed`, `resolved`, `settled`, `refunded`, `cancelled` |

#### `escrow fund <id>`

```
POST /api/v1/escrows/{id}/fund
```

No body.

#### `escrow stake <id>`

```
POST /api/v1/escrows/{id}/deposit-stake
```

No body. Deposits the worker stake configured at escrow creation.

#### `escrow submit <id>` (body required)

```
POST /api/v1/escrows/{id}/submit
```

Fields: `submission_uri` (required URI string), `proof_hash` (optional, 0x-prefixed 32-byte hex), `milestone_index` (optional, int)

Hex encoding note: for this payload, only `proof_hash` is hex and it must be 0x-prefixed; `submission_uri` is a URI string and `milestone_index` is a decimal integer.

#### `escrow approve <id>` (body required)

```
POST /api/v1/escrows/{id}/approve
```

Fields: `role` (required: `"buyer"` or `"verifier"`), `milestone_index` (optional, int)

#### `escrow verify-approve <id>` (body required)

```http
POST /api/v1/escrows/{id}/verify-approve
```

Use `verify-approve` instead of `approve` when on-chain or protocol-level ZK verification is required: this command submits the raw proof bytes to the escrow's configured `zkVerifier` contract for cryptographic validation before releasing funds. Use `approve` for standard approvals where no proof is needed.

Fields: `proof` (required, 0x-prefixed hex-encoded bytes), `milestone_index` (optional, int)

#### `escrow dispute <id>` (body required)

```
POST /api/v1/escrows/{id}/dispute
```

Fields: `role` (required: `"buyer"` or `"verifier"`), `reason_uri` (required), `milestone_index` (optional, int)

#### `escrow resolve <id>` (body required)

```
POST /api/v1/escrows/{id}/resolve
```

Fields: `worker_award_bps` (required, string, 0-10000), `resolution_uri` (required), `milestone_index` (optional, int)

#### `escrow abort <id>`

```
POST /api/v1/escrows/{id}/abort-milestones
```

No body. Buyer-only. Aborts uncompleted milestones and refunds their amounts.

#### `escrow backup <id>`

```
POST /api/v1/escrows/{id}/activate-backup
```

No body. Buyer-only. Replaces primary worker with backup, extends deadline.

---

### `rfq`

#### `rfq create` (body required)

```
POST /api/v1/rfqs
```

Required fields: `title`, `description`, `buyer`, `budget_min`, `budget_max`, `deadline`, `review_period_seconds`, `dispute_period_seconds`, `arbitrator_timeout_seconds`, `expires_at`

Optional fields: `token`, `verifier`, `arbitrator`, `worker_stake`, `milestones_json`, `requirements_json`, `required_credentials_json` (JSON array of credential requirement selectors, e.g. `[{"domain":"code-review","capabilities":["solidity"],"trusted_issuers":["0x..."]}]`), `commit_deadline`, `reveal_deadline`, `parent_escrow_id`

When `parent_escrow_id` is set, the server enforces parent-worker ownership checks and a configurable re-bid cooldown gate. Cooldown rejections include retry timing.

#### `rfq list`

```
GET /api/v1/rfqs
```

| Flag | Description |
|------|-------------|
| `--status <status>` | Filter: `open`, `closed`, `cancelled`, `expired` |
| `--buyer <addr>` | Filter by buyer address |

#### `rfq get <id>`

```
GET /api/v1/rfqs/{id}
```

Returns RFQ details with associated bids.

#### `rfq cancel <id>`

```
POST /api/v1/rfqs/{id}/cancel
```

No body.

---

### `bid`

#### `bid commit <rfq-id>` (body required)

```
POST /api/v1/rfqs/{id}/bids/commit
```

Required fields: `bidder`, `commitment` (0x-prefixed 32-byte hex hash), `nonce`

Submits a sealed-bid commitment during the commit phase (before `commit_deadline`).

#### `bid reveal <rfq-id>` (body required)

```
POST /api/v1/rfqs/{id}/bids/reveal
```

Required fields: `bidder`, `nonce`, `salt`, `amount`

Optional fields: `expires_at` (Unix timestamp; when omitted defaults to the RFQ deadline), `estimated_duration` (int, seconds), `reputation_bond`, `milestones_json`, `message`, `stake_mandate_id`, `credentials_json` (JSON array of attestation-v1 payloads for verifiable credentials)

Reveals a sealed bid during the reveal phase. The reveal must match the prior commitment hash. When `credentials_json` is provided, the server validates attestation signatures, subject binding, and expiry, then matches against the RFQ's `required_credentials_json`.

#### `bid list <rfq-id>`

```
GET /api/v1/rfqs/{id}/bids
```

Returns bids with `credential_verified` and `credential_match_summary` fields.

#### `bid accept <rfq-id>` (body required)

```
POST /api/v1/rfqs/{id}/accept
```

Fields: `bid_id` (required, int), `caller` (optional)

Accepting a bid atomically creates an on-chain escrow. When the RFQ has `required_credentials_json`, only bids with `credential_verified=true` can be accepted.

---

### `reputation`

#### `reputation get <address>`

```
GET /api/v1/reputation/{address}
```

| Flag | Description |
|------|-------------|
| `--role buyer\|worker` | Filter by role |

Responses include both immutable raw counters (`completed`, `disputed`, `failed`) and damped metrics under `damped` for stability-aware ranking.

---

### `events`

#### `events subscribe`

```
GET /api/v1/events          (all escrows)
GET /api/v1/escrows/{id}/events  (specific escrow)
```

| Flag | Description |
|------|-------------|
| `--escrow-id <id>` | Scope to a single escrow |
| `--granularity L0\|L1\|L2\|L3` | Default `L1`. L0 = heartbeat, L1 = state transitions |

SSE stream. Stays open until interrupted.

---

### `emergency`

Owner-only operations for incident response.

#### `emergency freeze-address` (body required)

```
POST /api/v1/emergency/freeze-address
```

Body: `{"address": "0x..."}`

#### `emergency unfreeze-address` (body required)

```
POST /api/v1/emergency/unfreeze-address
```

Body: `{"address": "0x..."}`

#### `emergency freeze-escrow` (body required)

```
POST /api/v1/emergency/freeze-escrow
```

Body: `{"escrow_id": "5"}`

#### `emergency unfreeze-escrow` (body required)

```
POST /api/v1/emergency/unfreeze-escrow
```

Body: `{"escrow_id": "5"}`

#### `emergency resolve` (body required)

```
POST /api/v1/emergency/resolve
```

Body: `{"escrow_id": "5", "worker_award_bps": "3000"}`

#### `emergency frozen-addresses`

```
GET /api/v1/emergency/frozen-addresses
```

#### `emergency actions`

```
GET /api/v1/emergency/actions
```

| Flag | Description |
|------|-------------|
| `--limit <n>` | Result limit |
| `--offset <n>` | Result offset |

---

### `ap2`

Gasless ERC20 funding via x402 payment rail.

#### `ap2 validate` (body required)

```
POST /api/v1/ap2/validate
```

#### `ap2 fund` (body required)

```
POST /api/v1/ap2/fund
```

#### `ap2 mandate <id>`

```
GET /api/v1/ap2/mandates/{id}
```

---

### `decomposition`

Contract-first task decomposition: break a complex task into a tree of sub-tasks, validate the structure, then finalize into one RFQ per leaf node.

> **Flag note**: decomposition commands use `--json` (not `--data`) for the request body.

#### `decomposition create` (body required)

```
POST /api/v1/decompositions
```

Required fields: `title`, `description`, `buyer`, `sub_tasks[]`

Each `sub_tasks` entry:

| Field | Type | Description |
|-------|------|-------------|
| `temp_id` | string | Client-assigned ID for referencing in `parent_temp_id` |
| `parent_temp_id` | string | `""` for root nodes |
| `title` | string | Sub-task title |
| `description` | string | Sub-task description |
| `verification_type` | string | `optimistic` \| `quorum` \| `zk_proof` \| `unit_test` \| `""` |
| `delegate_preference` | string | Advisory hint: `human` \| `ai` \| `any` \| `""` |
| `requires_further_decomposition` | bool | True if this node should be recursively decomposed |

Optional top-level fields: `spec_hash` (0x-prefixed content hash of specification document)

Returns: decomposition record with `id`, all nodes with assigned `node_id`s, `structural_issues` (hard blockers), and `market_context` per leaf node (informational).

#### `decomposition list`

```
GET /api/v1/decompositions
```

| Flag | Description |
|------|-------------|
| `--buyer <addr>` | Filter by buyer address |
| `--status <status>` | Filter: `draft` \| `valid` \| `finalized` |

#### `decomposition get <id>`

```
GET /api/v1/decompositions/{id}
```

Returns decomposition + all nodes + `structural_issues` + `market_context`.

#### `decomposition finalize <id>` (body required)

```
POST /api/v1/decompositions/{id}/finalize
```

Converts validated decomposition to one RFQ per leaf node. Only works when decomposition status is `valid` (no structural issues).

Required fields: `buyer`, `token`, `deadline`, `review_period_seconds`, `dispute_period_seconds`, `arbitrator_timeout_seconds`

Optional fields: `arbitrator`, `verifier_panel` (array), `quorum_count`, `budget_min`, `budget_max`, `commit_deadline`, `reveal_deadline`, `expires_at`

Returns: array of created RFQ IDs (one per leaf sub-task).

---

### `ucp`

UCP (Universal Checkout Protocol) adapter — maps escrow lifecycle to a standardized checkout state machine.

#### `ucp profile`

```
GET /.well-known/ucp
```

Returns provider profile: `version`, `provider_name`, `provider_url`, supported `operations`, endpoint map, and UCP-to-escrow `status_map`.

#### `ucp create` (body required)

```
POST /api/v1/ucp/checkouts
```

Fields:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `session_id` | string | recommended | Caller session identifier |
| `idempotency_key` | string | recommended | Prevents duplicate creation |
| `checkout_id` | string | no | Client-chosen ID (server generates if omitted) |
| `escrow_id` | int | one of | Attach to existing escrow |
| `create_escrow` | object | one of | Create new escrow inline (same fields as `escrow create`) |
| `auto_fund` | bool | no | Fund the escrow immediately after creation |

Returns: `Checkout` object with `checkout_id`, `escrow_id`, `ucp_status`, `escrow_status`, `next_action`.

#### `ucp get <checkout-id>`

```
GET /api/v1/ucp/checkouts/{checkout-id}
```

Returns current checkout state with embedded escrow details.

#### `ucp update <checkout-id>` (body required)

```
PATCH /api/v1/ucp/checkouts/{checkout-id}
```

Maps a UCP operation to the underlying escrow action.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `operation` | string | yes | `submit` \| `approve` \| `dispute` \| `resolve` \| `verify` |
| `idempotency_key` | string | no | Deduplication key |
| `role` | string | context | `buyer` or `verifier` (for approve/dispute) |
| `submission_uri` | string | for submit | Deliverable URI |
| `proof_hash` | string | no | 0x-prefixed 32-byte hex |
| `proof` | string | no | 0x-prefixed hex proof bytes (for ZK verify) |
| `milestone_index` | int | no | Target milestone |
| `approve` | bool | no | True = approve, false = dispute |
| `reason_uri` | string | for dispute | Dispute reason URI |
| `worker_award_bps` | int | for resolve | 0–10000 |
| `resolution_uri` | string | for resolve | Resolution URI |

#### `ucp complete <checkout-id>`

```
POST /api/v1/ucp/checkouts/{checkout-id}/complete
```

Attempts buyer-side completion. Optional body: `idempotency_key`, `role`, `proof` (for ZK), `milestone_index`.

#### `ucp cancel <checkout-id>`

```
POST /api/v1/ucp/checkouts/{checkout-id}/cancel
```

Cancels checkout via escrow cancellation or refund path. Optional body: `idempotency_key`, `milestone_index`.

**UCP status mapping:**

| UCP status | Escrow status(es) |
|------------|-------------------|
| `incomplete` | Created (unfunded) |
| `ready_for_complete` | Submitted |
| `complete_in_progress` | Approved, Resolved |
| `completed` | Settled |
| `canceled` | Cancelled, Refunded |
| `requires_escalation` | Disputed |

---

### `dct`

Delegation Capability Tokens — macaroon-style bearer tokens that authorize specific operations on escrows with optional scope attenuation.

#### `dct mint` (body required)

```
POST /api/v1/dcts/mint
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `escrow_id` | int | yes | Escrow to scope this token to |
| `subject` | string | yes | Address being authorized |
| `issuer` | string | no | Issuing address (defaults to caller) |
| `operations` | []string | yes | Permitted operations (e.g. `["submit","approve"]`) |
| `resources` | []string | yes | Permitted resource IDs |
| `expires_at` | int | yes | Unix timestamp |
| `caller` | string | yes | Address performing the mint |

#### `dct delegate` (body required)

```
POST /api/v1/dcts/delegate
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `parent_token` | string | yes | Parent DCT token string |
| `subject` | string | yes | Address being delegated to |
| `issuer` | string | no | Issuing address |
| `operations` | []string | yes | Must be a subset of parent's operations |
| `resources` | []string | yes | Must be a subset of parent's resources |
| `expires_at` | int | yes | Must not exceed parent's expiry |
| `caller` | string | yes | Address performing the delegation |

#### `dct introspect` (body required)

```
POST /api/v1/dcts/introspect
```

Body: `{"token": "<dct-token-string>"}`

Returns: `token` record, `active` (bool), `reasons` (why inactive if false).

#### `dct revoke` (body required)

```
POST /api/v1/dcts/revoke
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `token_id` | string | yes | ID of the DCT to revoke |
| `reason` | string | no | Human-readable revocation reason |
| `by` | string | no | Address initiating revocation |
| `caller` | string | yes | Authorized caller address |

#### `dct emergency-override` (body required, owner only)

```
POST /api/v1/dcts/emergency-override
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `escrow_id` | int | yes | Target escrow |
| `operation` | string | yes | Operation to authorize |
| `caller_address` | string | yes | Address to authorize for the operation |
| `reason` | string | yes | Reason for override |
| `owner` | string | yes | Factory owner address (must match config) |

#### `dct list-escrow <escrow-id>`

```
GET /api/v1/escrows/{id}/dcts
```

Lists all DCTs issued for the given escrow.

#### `dct audit [escrow-id]`

```
GET /api/v1/dcts/audit
```

| Query/Flag | Description |
|------------|-------------|
| `[escrow-id]` arg | Filter to a specific escrow (optional) |

Returns DCT authorization audit log entries in chronological order.

---

### `escrow checkpoint-*`

Mid-task state snapshots for resumability (paper §6.1).

#### `escrow checkpoint-commit <id>` (body required)

```
POST /api/v1/escrows/{id}/checkpoints
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `state_snapshot_uri` | string | yes | URI of the serialized checkpoint artifact |
| `snapshot_hash` | string | yes | 0x-prefixed SHA-256 of the artifact |
| `committed_by` | string | yes | Worker or agent address |
| `milestone_index` | int | no | For milestone escrows |
| `completion_pct` | int | no | 0–100 estimate of task completion |
| `metadata_json` | string | no | Arbitrary JSON metadata string |

#### `escrow checkpoints <id>`

```
GET /api/v1/escrows/{id}/checkpoints
```

| Flag | Description |
|------|-------------|
| `--milestone-index <n>` | Filter by milestone index |

Returns all checkpoints in chronological order.

#### `escrow checkpoint-latest <id>`

```
GET /api/v1/escrows/{id}/checkpoints/latest
```

| Flag | Description |
|------|-------------|
| `--milestone-index <n>` | Scope to a specific milestone |

Returns the most recent checkpoint for this escrow (or milestone).

#### `escrow attestation-chain <id>`

```
GET /api/v1/escrows/{id}/attestation-chain
```

Returns full ordered attestation history for the escrow (paper §4.8).

#### `escrow children <id>`

```
GET /api/v1/escrows/{id}/children
```

Lists child escrows linked via `parent_escrow_id`.

---

### `escrow` (quorum extensions)

#### `escrow verifier-stake <id>`

```
POST /api/v1/escrows/{id}/deposit-verifier-stake
```

No body. Deposits verifier quorum stake for quorum-gated escrows.

#### `escrow withdraw-stake <id>`

```
POST /api/v1/escrows/{id}/withdraw-stake
```

No body. Withdraws owed verifier stake after quorum settlement or refund.

#### `escrow quorum-vote <id>` (body required)

```
POST /api/v1/escrows/{id}/quorum-vote
```

Cast a verifier quorum vote. Use instead of `approve` when `quorum_threshold > 1`.
