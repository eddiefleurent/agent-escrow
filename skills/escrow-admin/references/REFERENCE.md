# Escrow Admin CLI -- Complete Reference

Binary: `escrow-cli` (on PATH after install, or `./go-server/bin/escrow-cli`)

All global flags from the participant CLI apply here too:

| Flag | Default | Description |
|------|---------|-------------|
| `--server <url>` | `$ESCROW_SERVER_URL` or `http://localhost:8080` | Server base URL |
| `--output text\|json` | `text` | Output format |
| `--timeout <duration>` | `0` (disabled) | Per-request timeout |

---

## `health`

```
GET /api/v1/health
```

Returns `{"status":"ok","chain":{"block_number":...,"chain_id":...}}`.
If `status` is `"degraded"`, the chain RPC is unreachable.

---

## Emergency Protocol

All emergency commands are owner-only. The private key configured on the server
must be the factory owner.

### `emergency freeze-address` (body required)

```
POST /api/v1/emergency/freeze-address
```

Body fields:

| Field | Type | Description |
|-------|------|-------------|
| `address` | string | Checksummed hex address to freeze |

```json
{"address": "0xBadActorAddress..."}
```

### `emergency unfreeze-address` (body required)

```
POST /api/v1/emergency/unfreeze-address
```

Same body as `freeze-address`.

### `emergency freeze-escrow` (body required)

```
POST /api/v1/emergency/freeze-escrow
```

| Field | Type | Description |
|-------|------|-------------|
| `escrow_id` | integer | Local database escrow ID (not the on-chain address) |

```json
{"escrow_id": 42}
```

### `emergency unfreeze-escrow` (body required)

```
POST /api/v1/emergency/unfreeze-escrow
```

Same body as `freeze-escrow`.

### `emergency resolve` (body required)

```
POST /api/v1/emergency/resolve
```

Force-settles a frozen escrow. Irreversible.

| Field | Type | Description |
|-------|------|-------------|
| `escrow_id` | integer | Local database escrow ID |
| `worker_award_bps` | integer | Basis points (0–10000) awarded to worker |

```json
{"escrow_id": 42, "worker_award_bps": 3000}
```

`worker_award_bps` examples:
- `0` = full refund to buyer
- `5000` = 50/50 split
- `10000` = full payout to worker

### `emergency frozen-addresses`

```
GET /api/v1/emergency/frozen-addresses
```

No flags.

### `emergency actions`

```
GET /api/v1/emergency/actions
```

| Flag | Description |
|------|-------------|
| `--limit <n>` | Max results (default: server default) |
| `--offset <n>` | Pagination offset |

---

## AP2 Gasless Funding

Requires `X402_ENABLED=true` on the server.

### Mandate Envelope Schema

All AP2 requests wrap their payload in a `mandate_envelope`:

```json
{
  "mandate_envelope": {
    "type": "payment",
    "payload": { ... },
    "signature": "0x...",
    "signer_address": "0xBuyerAddress",
    "authorization": {
      "from": "0xBuyerAddress",
      "to": "0xEscrowOrRelayerAddress",
      "value": "1000000",
      "valid_after": "0",
      "valid_before": "1740000000",
      "nonce": "0x...",
      "v": 28,
      "r": "0x...",
      "s": "0x..."
    }
  }
}
```

**Mandate types and their `payload` fields:**

`"intent"` -- budget constraint:
```json
{
  "signer": "0x...",
  "budget_amount": "5000000",
  "budget_currency": "USDC",
  "ttl_seconds": 3600,
  "description": "optional"
}
```

`"cart"` -- exact transaction:
```json
{
  "signer": "0x...",
  "merchant": "0x...",
  "amount": "1000000",
  "currency": "USDC",
  "items_hash": "0x...",
  "ttl_seconds": 300
}
```

`"payment"` -- compact credential:
```json
{
  "signer": "0x...",
  "amount": "1000000",
  "currency": "USDC",
  "recipient": "0x...",
  "nonce": "0x..."
}
```

### `ap2 validate` (body required)

```
POST /api/v1/ap2/validate
```

Dry-run validation. Does not execute funding.

Body: `{"mandate_envelope": { ... }}`

Response: `{"valid": true}` or `{"valid": false, "reason": "..."}`

### `ap2 fund` (body required)

```
POST /api/v1/ap2/fund
```

Executes gasless escrow funding.

| Field | Type | Description |
|-------|------|-------------|
| `escrow_id` | integer | Local database escrow ID |
| `mandate_envelope` | object | See mandate envelope schema above |

```json
{
  "escrow_id": 42,
  "mandate_envelope": { ... }
}
```

Response:
```json
{
  "tx_hash": "0x...",
  "escrow_id": 42,
  "mandate_id": "...",
  "status": "funded"
}
```

### `ap2 mandate <id>`

```
GET /api/v1/ap2/mandates/{id}
```

Returns the full mandate record including binding status, funding tx hash, and
expiry.
