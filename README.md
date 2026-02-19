# Agent Escrow

A reference implementation of [**"Intelligent AI Delegation"**](https://arxiv.org/abs/2602.11865) (Tomašev, Franklin, Osindero -- Google DeepMind, 2026), a framework for task decomposition, delegation, verification, and settlement in open agentic economies.

The paper defines five framework pillars (dynamic assessment, adaptive execution, structural transparency, scalable market coordination, systemic resilience), nine technical protocols, ethical considerations, and protocol integration paths. This project implements the financial settlement kernel and builds toward a full delegation marketplace.

## Motivation

As AI agents become more capable, the delegation problem becomes primary: not "can the agent do the task" but "how do we trust it did the task correctly, pay it fairly, and hold it accountable?"

Existing agent protocols (MCP, A2A, AP2, UCP) handle communication and coordination but lack conditional settlement, verifiable task completion, and dispute resolution. This project addresses that gap with an escrow-based settlement layer.

- **Buyers** require assurance they only pay for acceptable outcomes.
- **Workers** (human or AI) require assurance they will be paid for accepted deliverables.
- **The ecosystem** requires a neutral, transparent settlement and coordination layer.

## Architecture

Smart-contract escrow for AI task delegation. Buyers fund escrow, workers deliver, verifiers check quality, arbitrators resolve disputes. Settlement occurs on-chain; everything else remains off-chain.

**On-chain**: `TaskEscrowFactory` + `TaskEscrow` on Base (Ethereum L2). Nine-state lifecycle with role-gated transitions, deadline enforcement, dispute resolution, and timeout safety nets.

**Off-chain**: Single Go binary serving an MCP server (primary agent interface), JSON REST API, and background event indexer. SQLite storage, no external dependencies.

```
┌─────────────────────────────────────────────────────┐
│                   Go Server Binary                  │
│                                                     │
│  ┌─────────────┐  ┌──────────┐  ┌───────────────┐  │
│  │  MCP Server  │  │ HTTP API │  │ Event Indexer │  │
│  │   (stdio)    │  │ (JSON)   │  │  (background) │  │
│  └──────┬───────┘  └────┬─────┘  └───────┬───────┘  │
│         └───────────┬───┘                │          │
│              ┌──────┴──────┐    ┌────────┴───────┐  │
│              │ Chain Client│    │   SQLite DB    │  │
│              └──────┬──────┘    └────────────────┘  │
└─────────────────────┼───────────────────────────────┘
                      │
              ┌───────┴────────┐
              │  Base Sepolia  │
              └────────────────┘
```

## Paper Mapping

Each design decision traces to the paper. The settlement kernel (V1) covers the foundational layer; subsequent versions build toward the paper's full vision.

| Paper Pillar | V1 (Settlement) | V2 (Market) | V3 (Intelligence) |
|---|---|---|---|
| Dynamic Assessment (§4.1-4.2) | Fixed roles + task spec hash | Bidding (Task_RFQ + Bid_Object) | Contract-first decomposition tooling |
| Adaptive Execution (§4.4) | Timeouts + escalation paths | Milestones + backup agents | Checkpoint/resume for agent swaps |
| Structural Transparency (§4.5, 4.8) | Events + hash commitments | Attestation chains | ZK verification |
| Scalable Market Coordination (§4.3, 4.6) | Designated verifier/arbitrator | Reputation + credentials | Market stability mechanisms |
| Systemic Resilience (§4.7, 4.9) | Role gates + reentrancy guard | DCTs + Sybil resistance | Tiered service levels |

Full mapping: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) and [`docs/ROADMAP.md`](docs/ROADMAP.md).

## Quick Start

### Prerequisites

- [Foundry](https://book.getfoundry.sh/getting-started/installation) (Solidity toolchain)
- Go 1.26+

### Build and Test

```bash
make build          # compile Solidity contracts
make test           # run Foundry unit + edge case tests
make test-invariant # run invariant tests

make go-abi         # copy ABI artifacts from Foundry output
make go-build       # compile Go binary
make go-test        # run Go tests

make test-all       # everything at once
```

### Run the Server

```bash
export RPC_URL=https://sepolia.base.org
export FACTORY_ADDRESS=0x...
export PRIVATE_KEY=0x...
make go-run
```

Optional environment variables: `CHAIN_ID` (default: 84532), `PORT` (default: 8080), `DATABASE_URL` (default: delegation.db), `MCP_TRANSPORT` (set to `stdio` to enable MCP server).

### Deploy to Base Sepolia

```bash
export PRIVATE_KEY=0x...
export TREASURY=0x...
export OWNER=0x...
export PROTOCOL_FEE_BPS=100
export BASE_SEPOLIA_RPC_URL=https://sepolia.base.org
make deploy-base-sepolia
```

## MCP Tools (Agent Interface)

| Tool | Description |
|---|---|
| `create_escrow` | Create task + escrow via factory |
| `fund_escrow` | Buyer funds escrow |
| `submit_work` | Worker submits hash + URI |
| `approve_work` | Buyer/verifier approves |
| `dispute_work` | Buyer disputes or verifier rejects |
| `resolve_dispute` | Arbitrator resolves with BPS split |
| `get_escrow` | Read escrow state |
| `list_escrows` | Filter by role/status |

## HTTP API

```
GET  /api/v1/health               Health check
POST /api/v1/escrows              Create escrow
GET  /api/v1/escrows              List (query: role, address, status)
GET  /api/v1/escrows/{id}         Get escrow
POST /api/v1/escrows/{id}/fund    Fund
POST /api/v1/escrows/{id}/submit  Submit work
POST /api/v1/escrows/{id}/approve Approve
POST /api/v1/escrows/{id}/dispute Dispute
POST /api/v1/escrows/{id}/resolve Resolve
```

## Project Structure

```
src/                    Solidity contracts
test/                   Foundry tests
script/                 Deploy scripts
go-server/              Go server (MCP + API + indexer)
  cmd/server/           Entrypoint
  internal/             Chain client, storage, indexer, MCP, API
docs/                   Detailed documentation
```

## Documentation

| Document | Contents |
|---|---|
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | System design, paper grounding, scalability analysis |
| [`docs/SPEC_V1.md`](docs/SPEC_V1.md) | Contract specification: state machine, interfaces, invariants, security |
| [`docs/ROADMAP.md`](docs/ROADMAP.md) | Implementation status, delivery phases, paper framework mapping |
| [`docs/SETUP.md`](docs/SETUP.md) | Environment setup, Solidity version notes, configuration reference |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Contribution guidelines |

## Citation

This project implements the framework described in:

> Tomašev, N., Franklin, M., & Osindero, S. (2026). Intelligent AI Delegation. *arXiv preprint* [arXiv:2602.11865](https://arxiv.org/abs/2602.11865). Google DeepMind.

## License

[MIT](LICENSE)
