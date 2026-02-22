# Agent Escrow

[![Ask DeepWiki — explore this codebase with AI](https://deepwiki.com/badge.svg)](https://deepwiki.com/eddiefleurent/agent-escrow)

Escrow-based settlement for AI agent delegation -- a reference implementation of [**"Intelligent AI Delegation"**](https://arxiv.org/abs/2602.11865) (Tomašev, Franklin, Osindero -- Google DeepMind, 2026). Deployed on [Base Sepolia](https://sepolia.basescan.org/address/0x798830e2d3C25cF9296fe06a46D808CFB550e880) with verified transactions. **[See the live demo.](docs/DEMO_RUN.md)**

## How It Works

Agent A needs a code review. It posts the task with 0.01 ETH locked in a smart-contract escrow. Agent B picks it up, delivers the review, and submits proof. A verifier checks the work. Payment releases automatically.

If the work is rejected, a dispute goes to an arbitrator who splits the funds fairly. No trusted middleman -- the smart contract is the custodian.

![How It Works](docs/diagrams/happy-path.png)

## Why This Exists

As AI agents become more capable, the delegation problem becomes primary: not "can the agent do the task" but "how do we trust it did the task correctly, pay it fairly, and hold it accountable?"

Existing agent protocols (MCP, A2A, AP2, UCP) handle communication and coordination but lack **conditional settlement**, **verifiable task completion**, and **dispute resolution**. This project implements the financial settlement kernel that fills that gap.

- **Buyers** (delegators) need assurance they only pay for acceptable outcomes.
- **Workers** (human or AI delegatees) need assurance they get paid for accepted deliverables.
- **The ecosystem** needs a neutral, transparent settlement and coordination layer.

## Architecture

![System Architecture](docs/diagrams/architecture.png)

On-chain: `TaskEscrowFactory` deploys `TaskEscrow` instances on Base (Ethereum L2). Each escrow enforces a nine-state lifecycle with role-gated transitions, deadline enforcement, dispute resolution, and timeout safety nets. Supports both ETH and ERC20 tokens (USDC, etc.).

Off-chain: Single Go binary serving an MCP server (primary agent interface), JSON REST API, and background event indexer. SQLite storage, no external dependencies beyond an RPC endpoint.

The design principle: **settle on-chain, everything else off-chain**. Bidding, matching, reputation, task decomposition, and agent orchestration remain off-chain where they can iterate independently.

For internal component wiring, see the [detailed architecture diagram](docs/diagrams/architecture-detail.png) and [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

## Escrow Lifecycle

The happy path above is five steps. But real delegation needs failure handling -- what if the worker disappears? What if the buyer disputes? What if nobody responds? The full state machine covers all of it:

![Escrow State Machine](docs/diagrams/state-machine.png)

Nine states, multiple resolution paths: buyer disputes, verifier rejections, worker silence escalation, arbitrator resolution, and timeout-based refunds. Every path eventually settles or refunds -- funds never get stuck.

![Lifecycle Sequence](docs/diagrams/lifecycle-sequence.png)

## Live Demo

Deployed on Base Sepolia with verified ETH and USDC escrow lifecycles -- full create-fund-submit-approve-settle flows with real transactions.

Factory: [`0x798830e2d3C25cF9296fe06a46D808CFB550e880`](https://sepolia.basescan.org/address/0x798830e2d3C25cF9296fe06a46D808CFB550e880). Full transaction details: [`docs/DEMO_RUN.md`](docs/DEMO_RUN.md)

## Paper Mapping

Each design decision traces to the paper. The settlement kernel (V1) covers the foundational layer; subsequent versions build toward the paper's full vision.

| Paper Pillar | V1 (Settlement) | V2 (Market) | V3 (Intelligence) |
|---|---|---|---|
| Dynamic Assessment (§4.1-4.2) | Fixed roles + task spec hash | Bidding (Task_RFQ + Bid_Object) | Contract-first decomposition tooling |
| Adaptive Execution (§4.4) | Timeouts + escalation paths | Milestones + backup agents | Checkpoint/resume for agent swaps |
| Structural Transparency (§4.5, 4.8) | Events + hash commitments | Attestation chains | ZK verification |
| Scalable Market Coordination (§4.3, 4.6) | Designated verifier/arbitrator | Reputation + credentials | Market stability mechanisms |
| Systemic Resilience (§4.7, 4.9) | Role gates + reentrancy guard | DCTs + Sybil resistance | Tiered service levels |

Full mapping with paper section references: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) and [`docs/ROADMAP.md`](docs/ROADMAP.md).

## Agent Integration

### MCP Tools

The MCP server is the primary interface for AI agents. Any MCP-compatible client (Claude, GPT, custom agents) can use escrow without Solidity or wallet libraries -- the server handles chain interaction.

| Tool | Description |
|---|---|
| `create_escrow` | Create task + escrow via factory (ETH or ERC20) |
| `fund_escrow` | Buyer funds escrow |
| `submit_work` | Worker submits hash + URI |
| `approve_work` | Buyer or verifier approves |
| `dispute_work` | Buyer disputes or verifier rejects |
| `resolve_dispute` | Arbitrator resolves with BPS split |
| `get_escrow` | Read escrow state |
| `list_escrows` | Filter by role, address, or status |

### HTTP API

```text
GET  /api/v1/health               Health check
POST /api/v1/escrows              Create escrow
GET  /api/v1/escrows              List (query: role, address, status)
GET  /api/v1/escrows/{id}         Get escrow details
POST /api/v1/escrows/{id}/fund    Fund escrow
POST /api/v1/escrows/{id}/submit  Submit work
POST /api/v1/escrows/{id}/approve Approve submission
POST /api/v1/escrows/{id}/dispute Dispute submission
POST /api/v1/escrows/{id}/resolve Resolve dispute
```

## Implementation Status

**V1 -- Settlement Kernel**: Complete. 9-state escrow contracts, full test suite (unit, fuzz, invariant), Go server with MCP + HTTP + indexer, live on Base Sepolia.

**V2 -- Market Primitives**: Nearly complete (10/11 items). ERC20/USDC payments, worker stake, milestone-based escrow, backup agent clause, on-chain reputation, complexity floor, bidding protocol (Task_RFQ + Bid_Object), A2A settlement adapter, AP2 mandate-to-escrow bridge, real-time event subscriptions (SSE/WebSocket + MCP polling). Remaining: emergency response protocol.

**V3 -- Delegation Intelligence**: Planned. DCTs, ZK verification, checkpoint/resume, tiered service levels, multi-verifier quorum, attestation chains.

**V4 -- Ethical Safeguards**: Planned. Curriculum-aware task routing, liability firebreaks, governance safety floors.

Full roadmap with paper traceability: [`docs/ROADMAP.md`](docs/ROADMAP.md).

## Project Structure

```text
src/                    Solidity contracts (TaskEscrowFactory, TaskEscrow)
test/                   Foundry tests (unit, fuzz, invariant)
script/                 Deployment scripts
go-server/
  cmd/server/           Entrypoint
  internal/
    chain/              go-ethereum client, ABI bindings
    storage/            SQLite schema, queries, models
    indexer/            Event polling → DB reconciliation
    mcpserver/          MCP server + tool handlers
    api/                HTTP JSON API + middleware
  abi/                  Embedded ABI artifacts
docs/
  diagrams/             PlantUML sources + generated PNGs
  ARCHITECTURE.md       System design, paper grounding, scalability analysis
  SPEC.md               Contract specification: state machine, interfaces, invariants
  ROADMAP.md            Delivery phases, paper framework mapping
  SETUP.md              Environment setup, configuration reference
  DEMO_RUN.md           Live demo — transactions on Base Sepolia
  DEPLOYMENTS.md        Deployed contract addresses
  DEPLOY.md             Deployment guide and lifecycle walkthrough
```

## Documentation

| Document | Contents |
|---|---|
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | System design, paper grounding, scalability analysis |
| [`docs/SPEC.md`](docs/SPEC.md) | Contract specification: state machine, interfaces, invariants, security |
| [`docs/ROADMAP.md`](docs/ROADMAP.md) | Implementation phases, paper framework mapping, success metrics |
| [`docs/SETUP.md`](docs/SETUP.md) | Environment setup, configuration reference |
| [`docs/DEMO_RUN.md`](docs/DEMO_RUN.md) | Live demo run with on-chain transactions |
| [`docs/DEPLOYMENTS.md`](docs/DEPLOYMENTS.md) | Deployed contract addresses |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Contribution guidelines |

## Quick Start

### Prerequisites

- [Foundry](https://book.getfoundry.sh/getting-started/installation) (Solidity toolchain)
- Go 1.26+

### Build and Test

```bash
make build          # compile Solidity contracts
make test           # Foundry unit + edge case tests
make test-invariant # invariant / fuzz tests

make go-abi         # copy ABI artifacts from Foundry output
make go-build       # compile Go binary
make go-test        # Go tests

make test-all       # everything at once
```

### Run the Server

```bash
export RPC_URL=https://sepolia.base.org
export FACTORY_ADDRESS=0x...
export PRIVATE_KEY=0x...
make go-run
```

The server starts the HTTP API on port 8080 and the event indexer in the background. Set `MCP_TRANSPORT=stdio` to also enable the MCP server for agent integration.

See [`docs/SETUP.md`](docs/SETUP.md) for deployment and the full configuration reference.

## Citation

This project implements the framework described in:

> Tomašev, N., Franklin, M., & Osindero, S. (2026). Intelligent AI Delegation. *arXiv preprint* [arXiv:2602.11865](https://arxiv.org/abs/2602.11865). Google DeepMind.

## License

[MIT](LICENSE)
