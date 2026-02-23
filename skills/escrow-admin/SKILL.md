---
name: escrow-admin
description: >
  Operate and administer the escrow marketplace server. Use this skill for
  operator-only tasks: emergency incident response (freezing addresses or escrows,
  force-resolving frozen escrows), server health monitoring, and AP2 gasless funding
  operations. This skill is for the marketplace operator, not for market participants.
  Trigger on: freeze, unfreeze, emergency resolve, admin, operator tasks, AP2 mandate,
  server health.
---

# Escrow Admin

You are the marketplace operator. These commands require owner-level authority on the
factory contract. Do not share or delegate these operations.

## Setup

Same CLI as the participant skill:

```bash
export ESCROW_SERVER_URL=https://your-escrow-server.example.com
escrow-cli health
```

---

## Server Health

```bash
escrow-cli health
```

Returns API status and current block number. If `status` is `degraded`, the chain
RPC is unreachable -- check the server's `RPC_URL` configuration.

---

## Emergency Protocol

Use when a security incident requires immediate intervention. All emergency actions
are logged in an audit trail.

### Freeze a bad actor's address

Prevents a frozen address from participating in any new escrows.

```bash
escrow-cli emergency freeze-address --data '{"address": "0xBadActor..."}'
```

### Unfreeze an address

```bash
escrow-cli emergency unfreeze-address --data '{"address": "0xAddress..."}'
```

### Freeze a specific escrow

Blocks all participant actions on the escrow until you resolve or unfreeze it.
Use when funds may be at risk mid-lifecycle.

```bash
escrow-cli emergency freeze-escrow --data '{"escrow_id": 42}'
```

### Unfreeze an escrow (resume normal lifecycle)

```bash
escrow-cli emergency unfreeze-escrow --data '{"escrow_id": 42}'
```

### Emergency resolve a frozen escrow

Force-settles a frozen escrow without requiring normal approval/dispute flows.
`worker_award_bps` is basis points (0–10000): `5000` = 50/50 split.

```bash
escrow-cli emergency resolve --data '{"escrow_id": 42, "worker_award_bps": 3000}'
```

### Audit log

```bash
# Who is currently frozen?
escrow-cli emergency frozen-addresses

# Full emergency action history
escrow-cli emergency actions --limit 50 --offset 0
```

---

## AP2 Gasless Funding

Enables buyers to fund escrows via EIP-3009 signed authorization (x402 payment
rail) without holding ETH for gas. The server acts as the relayer.

The core payload is a `mandate_envelope` containing the mandate type/payload, a
cryptographic signature, and an EIP-3009 authorization for the token transfer.

```bash
# 1. Dry-run validation before committing
escrow-cli ap2 validate --output json --data '{
  "mandate_envelope": {
    "type": "payment",
    "payload": {
      "signer": "0xBuyerAddress",
      "amount": "1000000",
      "currency": "USDC",
      "recipient": "0xEscrowAddress",
      "nonce": "0xAbcDef..."
    },
    "signature": "0x...",
    "signer_address": "0xBuyerAddress",
    "authorization": {
      "from": "0xBuyerAddress",
      "to": "0xEscrowAddress",
      "value": "1000000",
      "valid_after": "0",
      "valid_before": "1740000000",
      "nonce": "0xAbcDef...",
      "v": 28,
      "r": "0x...",
      "s": "0x..."
    }
  }
}'
# → returns {"valid": true} or {"valid": false, "reason": "..."}

# 2. Execute funding once validated
escrow-cli ap2 fund --output json --data '{
  "escrow_id": 42,
  "mandate_envelope": { ... }
}'
# → returns tx_hash, escrow_id, mandate_id, status

# 3. Look up a bound mandate
escrow-cli ap2 mandate <mandate-id> --output json
```

Mandate types: `"intent"` (budget constraint), `"cart"` (exact transaction),
`"payment"` (compact credential). See `references/REFERENCE.md` for all field
schemas.

---

## Notes

- All emergency operations are owner-only and enforced on-chain.
- Emergency resolve permanently settles the escrow -- it cannot be undone.
- AP2 operations require `X402_ENABLED=true` on the server.

Full command reference: `references/REFERENCE.md`
