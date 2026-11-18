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

```text
GET /api/v1/health
```

Returns `{"status":"ok","chain":{"block_number":...,"chain_id":...}}`.
If `status` is `"degraded"`, the chain RPC is unreachable.

---

## Emergency Protocol

All emergency commands are owner-only. The private key configured on the server
must be the factory owner.

### `emergency freeze-address` (body required)

```text
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

```text
POST /api/v1/emergency/unfreeze-address
```

Same body as `freeze-address`.

### `emergency freeze-escrow` (body required)

```text
POST /api/v1/emergency/freeze-escrow
```

| Field | Type | Description |
|-------|------|-------------|
| `escrow_id` | integer | Local database escrow ID (not the on-chain address) |

```json
{"escrow_id": 42}
```

### `emergency unfreeze-escrow` (body required)

```text
POST /api/v1/emergency/unfreeze-escrow
```

Same body as `freeze-escrow`.

### `emergency resolve` (body required)

```text
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

```text
GET /api/v1/emergency/frozen-addresses
```

No flags.

### `emergency actions`

```text
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
    "signature": "0x<65-byte EIP-712 mandate signature>",
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
  "issued_at": 1740000000,
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
  "items_hash": "0x<32-byte hex>",
  "ttl_seconds": 300,
  "issued_at": 1740000000
}
```

`"payment"` -- compact credential:
```json
{
  "signer": "0x...",
  "amount": "1000000",
  "currency": "USDC",
  "recipient": "0x...",
  "nonce": "0x<32-byte hex>"
}
```

Payload rules: unknown fields are rejected; `signer` must equal `signer_address`;
amounts are base-10 strings without leading zeros. Payment: `amount` must equal
`authorization.value` and `recipient` must equal `authorization.to`. Cart: `amount`
must equal `authorization.value`. Intent: `authorization.value` must not exceed
`budget_amount`.

### Mandate Signature (EIP-712)

`signature` is a 65-byte `r || s || v` secp256k1 signature (0x hex, low-s, `v` in
27/28 or 0/1) by `signer_address` over EIP-712 typed data. Unsigned, malformed, or
wrong-signer mandates are rejected.

Domain:

```json
{"name": "agent-escrow AP2", "version": "1", "chainId": <server CHAIN_ID>, "verifyingContract": <server FACTORY_ADDRESS>}
```

Types (the primary type is chosen by `type`; JSON `snake_case` fields map to the
`camelCase` names below):

```text
Authorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)
IntentMandate(address signer,uint256 budgetAmount,string budgetCurrency,uint256 ttlSeconds,uint256 issuedAt,string description,Authorization authorization)
CartMandate(address signer,address merchant,uint256 amount,string currency,bytes32 itemsHash,uint256 ttlSeconds,uint256 issuedAt,Authorization authorization)
PaymentMandate(address signer,uint256 amount,string currency,address recipient,bytes32 nonce,Authorization authorization)
```

`authorization` in the typed message is the envelope's EIP-3009 authorization
(without `v`/`r`/`s`), so a mandate signature cannot be paired with a different
transfer. Validity window: `valid_after < now < valid_before`, and for intent/cart
mandates also `now < issued_at + ttl_seconds` (`issued_at` may be at most 5 minutes
in the future). See `demo/ap2_demo.py` for a signing example with `eth_account`.

### `ap2 validate` (body required)

```text
POST /api/v1/ap2/validate
```

Dry-run validation. Does not execute funding.

Body: `{"mandate_envelope": { ... }}`

Response: `{"valid": true}` or `{"valid": false, "reason": "..."}`

### `ap2 fund` (body required)

```text
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

```text
GET /api/v1/ap2/mandates/{id}
```

Returns the full mandate record including binding status, funding tx hash, and
expiry.
