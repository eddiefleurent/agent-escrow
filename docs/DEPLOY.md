# Base Sepolia Deployment & Reference Agent Demo

This guide covers deploying the `TaskEscrowFactory` to Base Sepolia and running a reference agent demo through the full escrow lifecycle.

Prerequisite reading: [`SETUP.md`](SETUP.md) (install steps), [`SPEC_V1.md`](SPEC_V1.md) (contract interfaces), [`ARCHITECTURE.md`](ARCHITECTURE.md) (system design).

---

## Part 1: Deploy to Base Sepolia

### 1.1 Prerequisites

| Requirement | Version / Detail |
|---|---|
| Foundry (`forge`, `cast`) | Latest via `foundryup` |
| Go | 1.26+ |
| Base Sepolia ETH | ~0.01 ETH for deployment + lifecycle testing |
| RPC URL | `https://sepolia.base.org` (public) or Alchemy/Infura Base Sepolia endpoint |

Install Foundry if not already present:
```bash
curl -L https://foundry.paradigm.xyz | bash
foundryup
```

Verify:
```bash
forge --version
go version
```

### 1.2 Wallet Setup

**Option A: Generate a fresh deployer keypair.**

```bash
cast wallet new
```

This prints an address and private key. Save both. The address is your deployer -- it will also become the initial `OWNER` if you choose.

**Option B: Use an existing wallet.**

Export the hex-encoded private key (with `0x` prefix). The address derived from this key will pay for deployment gas and become the contract deployer.

### 1.3 Fund the Deployer

Get Base Sepolia testnet ETH from a faucet:

- [Alchemy Base Sepolia Faucet](https://www.alchemy.com/faucets/base-sepolia)
- [Coinbase Base Faucet](https://www.coinbase.com/faucets/base-ethereum-sepolia-faucet)

Verify the balance:
```bash
cast balance <YOUR_DEPLOYER_ADDRESS> --rpc-url https://sepolia.base.org
```

You need roughly 0.005 ETH for the factory deployment. Budget 0.01 ETH total if you plan to run the full demo lifecycle afterward.

### 1.4 Environment Variables

Set all five required variables before deploying:

```bash
export PRIVATE_KEY=0x<YOUR_DEPLOYER_PRIVATE_KEY>
export TREASURY=0x<ADDRESS_THAT_RECEIVES_PROTOCOL_FEES>
export OWNER=0x<ADDRESS_THAT_ADMINISTERS_THE_FACTORY>
export PROTOCOL_FEE_BPS=100
export BASE_SEPOLIA_RPC_URL=https://sepolia.base.org
```

| Variable | Description | Suggested Value |
|---|---|---|
| `PRIVATE_KEY` | Hex-encoded private key of the deployer wallet | Your funded wallet key |
| `TREASURY` | Address that receives protocol fees from settled escrows | Can be same as deployer for testing |
| `OWNER` | Factory admin -- can update fees, treasury, and pause/unpause | Can be same as deployer for testing |
| `PROTOCOL_FEE_BPS` | Protocol fee in basis points (100 = 1%, max 10000) | `100` (1%) |
| `BASE_SEPOLIA_RPC_URL` | Base Sepolia JSON-RPC endpoint | `https://sepolia.base.org` or your Alchemy/Infura URL |

For testnet deployment, using the same address for deployer, treasury, and owner is fine. For mainnet, these should be separate addresses (ideally multisigs).

### 1.5 Build and Deploy

Build contracts first to ensure compilation succeeds:
```bash
make build
```

Deploy the factory:
```bash
make deploy-base-sepolia
```

Under the hood this runs:
```bash
forge script script/DeployFactory.s.sol:DeployFactory \
    --rpc-url $BASE_SEPOLIA_RPC_URL \
    --broadcast \
    -vv
```

The deploy script (`script/DeployFactory.s.sol`) reads `PRIVATE_KEY`, `TREASURY`, `OWNER`, and `PROTOCOL_FEE_BPS` from the environment, then calls:
```solidity
factory = new TaskEscrowFactory(protocolFeeBps, treasury, owner);
```

On success, Forge prints the deployed contract address and transaction hash. Example output:
```text
== Logs ==
  ...

## Setting up 1 EVM.

==========================

Chain 84532

Estimated gas price: ...
Estimated total gas used for script: ...

==========================

##### base-sepolia
✅  Hash: 0xabc123...
Contract Address: 0xdef456...
Block: 12345678
...
```

Record both the **contract address** and **transaction hash**.

### 1.6 Verify on BaseScan

Verify the factory source on the Base Sepolia block explorer so anyone can read the contract:

```bash
forge verify-contract <FACTORY_ADDRESS> \
    src/TaskEscrowFactory.sol:TaskEscrowFactory \
    --chain-id 84532 \
    --constructor-args $(cast abi-encode "constructor(uint16,address,address)" 100 <TREASURY_ADDRESS> <OWNER_ADDRESS>) \
    --etherscan-api-key <BASESCAN_API_KEY>
```

Replace the placeholders:
- `<FACTORY_ADDRESS>` -- the deployed address from step 1.5
- `<TREASURY_ADDRESS>` -- the treasury address you used
- `<OWNER_ADDRESS>` -- the owner address you used
- `<BASESCAN_API_KEY>` -- get one free at [basescan.org/myapikey](https://basescan.org/myapikey)

If using a `100` fee, the constructor args encode as: `uint16(100), address(treasury), address(owner)`.

Verify on the explorer UI: `https://sepolia.basescan.org/address/<FACTORY_ADDRESS>#code`

Individual `TaskEscrow` contracts created by the factory can also be verified, but they are created via `new` in the factory so BaseScan may auto-verify them once the factory source is verified.

### 1.7 Record Deployment

Create or update a `docs/DEPLOYMENTS.md` file with the deployment details:

```markdown
# Deployments

## Base Sepolia (Chain ID 84532)

| Item | Value |
|---|---|
| Factory Address | `0x...` |
| Deploy Tx Hash | `0x...` |
| Deployer | `0x...` |
| Owner | `0x...` |
| Treasury | `0x...` |
| Protocol Fee | 100 bps (1%) |
| Block Number | ... |
| Date | YYYY-MM-DD |
| Explorer | `https://sepolia.basescan.org/address/0x...` |
```

### 1.8 Smoke Test

Build and start the Go server pointed at the deployed factory:

```bash
make go-build

export RPC_URL=https://sepolia.base.org
export FACTORY_ADDRESS=0x<YOUR_DEPLOYED_FACTORY_ADDRESS>
export PRIVATE_KEY=0x<YOUR_PRIVATE_KEY>
export CHAIN_ID=84532
export PORT=8080
export START_BLOCK=<DEPLOY_BLOCK_NUMBER>   # skip scanning before deployment

make go-run
```

In a separate terminal, hit the health endpoint:
```bash
curl -s http://localhost:8080/api/v1/health | jq .
```

Expected response:
```json
{
  "status": "ok",
  "chain": {
    "block_number": 12345678,
    "chain_id": 84532
  }
}
```

The `block_number` confirms the server can reach Base Sepolia via RPC. The `chain_id` confirms it connected to the correct network. If the chain is unreachable, you get a 503 with `"status": "degraded"`.

### 1.9 Troubleshooting

| Problem | Likely Cause | Fix |
|---|---|---|
| `EvmError: OutOfFunds` | Deployer wallet has insufficient ETH | Fund the deployer address with more testnet ETH from a faucet |
| `nonce too low` / `nonce too high` | Stale nonce from a previous failed tx | Wait for pending transactions to confirm, or use `cast nonce <ADDRESS> --rpc-url ...` to check current nonce. Retry the deploy. |
| `Wrong chain ID` | `BASE_SEPOLIA_RPC_URL` points to a different network | Verify with `cast chain-id --rpc-url $BASE_SEPOLIA_RPC_URL` -- should return `84532` |
| Verification fails: `NOTOK` | Wrong constructor args or missing API key | Double-check the constructor args match exactly what you deployed with. Ensure the BaseScan API key is valid. |
| Health returns 503 `degraded` | RPC_URL is unreachable or rate-limited | Check the RPC URL is correct. Try a different provider (Alchemy, Infura). The public endpoint can be rate-limited under load. |
| Indexer crashes with `eth_getLogs` block range error | Free-tier RPC limits block range per request | Set `LOG_CHUNK_SIZE=9` when using Alchemy free tier. The indexer will chunk requests automatically. |
| `failed to create chain client` | Invalid `PRIVATE_KEY` format | Must be hex-encoded with `0x` prefix, 64 hex characters (32 bytes) |
| Forge script hangs | RPC timeout | Add `--timeout 120` to the forge script command, or switch to a faster RPC provider |

---

## Part 2: Reference Agent Demo

This walkthrough shows an AI agent completing a full escrow lifecycle -- from task creation through settlement -- using the HTTP API. Every step can equivalently be performed via the MCP tools (`create_escrow`, `fund_escrow`, etc.) for agents using the MCP interface.

### Prerequisites

- Factory deployed to Base Sepolia (Part 1 above)
- Go server running and healthy (`/api/v1/health` returns `"status": "ok"`)
- Server wallet has Base Sepolia ETH for transaction gas

The contract enforces that all four roles (buyer, worker, verifier, arbitrator) are **distinct addresses**. For this demo, the server wallet acts as the **buyer** and three separate test wallets are used for worker, verifier, and arbitrator. Actions that require the buyer's signature (create, fund, approve) go through the HTTP API. Actions that require a different role's signature (submit, resolve) use `cast send` with that role's private key directly.

In production, each role would be a separate agent with its own key.

### 2.1 Start the Server

**HTTP-only mode** (for curl-based demo):
```bash
export RPC_URL=https://sepolia.base.org
export FACTORY_ADDRESS=0x<YOUR_FACTORY_ADDRESS>
export PRIVATE_KEY=0x<YOUR_PRIVATE_KEY>
export CHAIN_ID=84532
export PORT=8080

make go-run
```

**MCP + HTTP mode** (for agent integration):
```bash
export MCP_TRANSPORT=stdio
# ... same env vars as above ...

make go-run
```

With `MCP_TRANSPORT=stdio`, the server runs both the MCP server on stdin/stdout and the HTTP API on the configured port. An MCP client (e.g., Claude Desktop, Cursor, or any MCP-compatible agent) connects via stdio.

### 2.2 Happy Path: Create -> Fund -> Submit -> Approve -> Settled

#### Step 1: Create Escrow

Set a submission deadline 1 hour from now. The amount is in wei (0.001 ETH = 1000000000000000 wei).

```bash
# Linux/GNU date:
DEADLINE=$(date -d "+1 hour" +%s)

# macOS/BSD date (uncomment if on macOS):
# DEADLINE=$(date -v+1H +%s)

curl -s -X POST http://localhost:8080/api/v1/escrows \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Summarize AI Delegation Paper",
    "description": "Produce a 500-word summary of the Intelligent AI Delegation paper",
    "buyer": "0x<BUYER_ADDRESS>",
    "worker": "0x<WORKER_ADDRESS>",
    "verifier": "0x<VERIFIER_ADDRESS>",
    "arbitrator": "0x<ARBITRATOR_ADDRESS>",
    "amount": "1000000000000000",
    "submission_deadline": "'$DEADLINE'",
    "review_period_seconds": "3600",
    "dispute_period_seconds": "7200",
    "arbitrator_timeout_seconds": "86400"
  }' | jq .
```

**MCP equivalent** -- call the `create_escrow` tool with the same fields.

Expected response:
```json
{
  "escrow_id": 1,
  "task_id": 1,
  "tx_hash": "0xabc123...",
  "escrow_address": "0xdef456...",
  "chain_escrow_id": 0
}
```

Key fields:
- `escrow_id` -- database ID used in all subsequent API calls
- `escrow_address` -- the deployed `TaskEscrow` contract on Base Sepolia
- `chain_escrow_id` -- the factory's sequential escrow counter
- `tx_hash` -- viewable at `https://sepolia.basescan.org/tx/<tx_hash>`

#### Step 2: Fund Escrow

The buyer sends the escrow amount (0.001 ETH) to the escrow contract:

```bash
curl -s -X POST http://localhost:8080/api/v1/escrows/1/fund | jq .
```

Response:
```json
{
  "tx_hash": "0x789abc..."
}
```

The escrow is now in `funded` status and holds 0.001 ETH.

#### Step 3: Submit Work

The worker delivers their output by providing a URI (pointing to the actual deliverable) and a submission hash (computed automatically by the server from the URI):

```bash
curl -s -X POST http://localhost:8080/api/v1/escrows/1/submit \
  -H "Content-Type: application/json" \
  -d '{
    "submission_uri": "ipfs://QmExampleHash/summary.md"
  }' | jq .
```

Response:
```json
{
  "tx_hash": "0xdef789..."
}
```

The escrow moves to `submitted` status. The review window (3600s = 1 hour) begins.

#### Step 4: Approve Work

The buyer (or verifier) reviews the submission and approves:

```bash
curl -s -X POST http://localhost:8080/api/v1/escrows/1/approve \
  -H "Content-Type: application/json" \
  -d '{"role": "buyer"}' | jq .
```

Response:
```json
{
  "tx_hash": "0x123def..."
}
```

Approval triggers immediate settlement on-chain:
- Worker receives `amount - protocolFee` (99% of 0.001 ETH at 1% fee)
- Treasury receives the protocol fee (1% of 0.001 ETH)
- Escrow moves to `settled` status

#### Step 5: Verify Settlement

Check the final escrow state:

```bash
curl -s http://localhost:8080/api/v1/escrows/1 | jq .
```

The `status` field should be `"settled"`. The escrow contract balance is now zero -- all funds have been distributed.

You can verify on-chain:
```bash
cast balance <ESCROW_ADDRESS> --rpc-url https://sepolia.base.org
# Should return 0
```

### 2.3 Dispute Path: Dispute -> Resolve (Split)

This demonstrates what happens when the buyer is unhappy with the submission. Start by creating and funding a second escrow (steps 1-3 from above), then:

#### Step 6: Dispute

The buyer raises a dispute instead of approving. This must happen within the review + dispute window:

```bash
curl -s -X POST http://localhost:8080/api/v1/escrows/2/dispute \
  -H "Content-Type: application/json" \
  -d '{
    "role": "buyer",
    "reason_uri": "ipfs://QmDisputeReason/incomplete-work.md"
  }' | jq .
```

Response:
```json
{
  "tx_hash": "0xaaa111..."
}
```

The escrow moves to `disputed` status. The arbitrator now has authority to resolve it.

#### Step 7: Resolve Dispute

The arbitrator reviews both sides and decides on a 50/50 split (5000 basis points to the worker):

```bash
curl -s -X POST http://localhost:8080/api/v1/escrows/2/resolve \
  -H "Content-Type: application/json" \
  -d '{
    "worker_award_bps": "5000",
    "resolution_uri": "ipfs://QmResolution/split-decision.md"
  }' | jq .
```

Response:
```json
{
  "tx_hash": "0xbbb222..."
}
```

Settlement math for a 50/50 split on 0.001 ETH with 1% fee:
- Worker gross: 0.0005 ETH (50% of escrow)
- Protocol fee: 0.000005 ETH (1% of worker gross)
- Worker net: 0.000495 ETH
- Buyer refund: 0.0005 ETH (50% of escrow)

The `worker_award_bps` parameter controls the split:
- `0` = full refund to buyer (worker gets nothing)
- `5000` = 50/50 split
- `10000` = full payout to worker (buyer gets nothing)

#### Step 8: Verify Resolution

```bash
curl -s http://localhost:8080/api/v1/escrows/2 | jq .
```

The `status` field should be `"settled"`. Both the worker and buyer received their respective portions.

### 2.4 MCP Tool Equivalents

Every HTTP call above maps 1:1 to an MCP tool. An AI agent connected via MCP would call:

| Demo Step | MCP Tool | Key Arguments |
|---|---|---|
| Create escrow | `create_escrow` | `title`, `description`, `buyer`, `worker`, `verifier`, `arbitrator`, `amount`, `submission_deadline`, `review_period_seconds`, `dispute_period_seconds`, `arbitrator_timeout_seconds` |
| Fund escrow | `fund_escrow` | `escrow_id` |
| Submit work | `submit_work` | `escrow_id`, `submission_uri` |
| Approve work | `approve_work` | `escrow_id`, `role` (`"buyer"` or `"verifier"`) |
| Dispute work | `dispute_work` | `escrow_id`, `role` (`"buyer"`, `"verifier"`, or `"worker"`), `reason_uri` |
| Resolve dispute | `resolve_dispute` | `escrow_id`, `worker_award_bps`, `resolution_uri` |
| Check status | `get_escrow` | `escrow_id` |
| List escrows | `list_escrows` | `role`, `address`, `status` (all optional filters) |

The MCP tools return the same JSON payloads as the HTTP endpoints. The server handles all chain interaction -- the agent never needs wallet libraries, ABI encoding, or direct RPC calls.

### 2.5 Full Lifecycle Summary

```text
Agent calls create_escrow
    → Factory deploys TaskEscrow contract
    → Server returns escrow_id, escrow_address, chain_escrow_id

Agent calls fund_escrow
    → Buyer sends ETH to escrow contract
    → Status: funded

Agent calls submit_work
    → Worker records submission hash + URI on-chain
    → Status: submitted

Agent calls approve_work (happy path)      Agent calls dispute_work (dispute path)
    → Settlement executes immediately          → Status: disputed
    → Worker paid, treasury gets fee           → Arbitrator reviews
    → Status: settled                          → Agent calls resolve_dispute
                                               → Split settlement executes
                                               → Status: settled
```

### 2.6 Tips for Agent Integration

- **Deadlines in the future**: `submission_deadline` is a Unix timestamp. Set it far enough in the future for the task to be completed.
- **Amount in wei**: 1 ETH = 10^18 wei. Use `1000000000000000` (10^15) for 0.001 ETH.
- **Poll for status**: After any write call, use `get_escrow` to confirm the status transition occurred. The server triggers an indexer run after each write, so the status should update within seconds.
- **Review window**: Approval and dispute calls must happen within the configured time windows. For testing, use short windows (e.g., 3600 seconds = 1 hour for review).
- **Gas**: The server wallet needs Base Sepolia ETH for every on-chain transaction. Monitor its balance if running many lifecycle tests.
