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

Required fields: `title`, `description`, `buyer`, `worker`, `verifier`, `arbitrator`, `amount`, `submission_deadline`, `review_period_seconds`, `dispute_period_seconds`, `arbitrator_timeout_seconds`

Optional fields: `worker_stake`, `token`, `milestones` (array of `{amount, submission_deadline}`), `backup_worker`, `backup_deadline_extension`

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
| `--status <status>` | Filter by state: `Created`, `Funded`, `Submitted`, `Approved`, `Disputed`, `Resolved`, `Settled`, `Refunded`, `Cancelled` |

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

Fields: `submission_uri` (required), `milestone_index` (optional, int)

#### `escrow approve <id>` (body required)

```
POST /api/v1/escrows/{id}/approve
```

Fields: `role` (required: `"buyer"` or `"verifier"`), `milestone_index` (optional, int)

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

Optional fields: `token`, `verifier`, `arbitrator`, `worker_stake`, `milestones_json`, `requirements_json`

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

#### `bid place <rfq-id>` (body required)

```
POST /api/v1/rfqs/{id}/bids
```

Required fields: `bidder`, `amount`, `expires_at`

Optional fields: `estimated_duration` (int, seconds), `reputation_bond`, `milestones_json`, `message`

#### `bid list <rfq-id>`

```
GET /api/v1/rfqs/{id}/bids
```

#### `bid accept <rfq-id>` (body required)

```
POST /api/v1/rfqs/{id}/accept
```

Fields: `bid_id` (required, int), `caller` (optional)

Accepting a bid atomically creates an on-chain escrow.

---

### `reputation`

#### `reputation get <address>`

```
GET /api/v1/reputation/{address}
```

| Flag | Description |
|------|-------------|
| `--role buyer\|worker` | Filter by role |

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
