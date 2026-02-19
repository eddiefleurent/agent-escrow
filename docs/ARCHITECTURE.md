# Architecture

## Overview

This project implements the ["Intelligent AI Delegation"](https://arxiv.org/abs/2602.11865) paper (Tomašev, Franklin, Osindero -- Google DeepMind, 2026) as a working escrow-based delegation marketplace.

The paper defines a comprehensive framework for task decomposition, delegation, verification, and settlement in open agentic economies. This document describes the system architecture, its grounding in the paper's framework, and the scalability path from settlement kernel to full marketplace.

```
Settlement Kernel (V1)  →  Market Primitives (V2)  →  Delegation Intelligence (V3)  →  Ecosystem Maturity (V4)
     escrow + roles          bidding + reputation        DCTs + ZK + re-delegation       ethics + governance + DIDs
```

Blockchain handles settlement guarantees, auditability, and cryptoeconomic enforcement. Execution, intelligence, matching, and orchestration remain off-chain for speed and flexibility.

### Problem Statement

The paper identifies a critical gap: as AI agents become more capable, the *delegation problem* becomes primary. Not "can the agent do the task" but "how do we trust it did the task correctly, pay it fairly, and hold it accountable?" Current AI delegation flows are fragile and trust-heavy. No existing agent protocol provides conditional settlement, verifiable task completion, or dispute resolution.

- Buyers need assurance they only pay for acceptable outcomes.
- Workers (human or AI) need assurance they will be paid if they deliver.
- The agentic ecosystem needs a neutral, transparent settlement and coordination layer.

### Foundational Concepts

The paper grounds intelligent delegation in concepts from organizational theory and economics that directly inform this architecture:

**Principal-Agent Problem** (paper §2.3). A delegator assigns work to an agent whose motivations may not align with the delegator's. In AI delegation this manifests as reward misspecification, specification gaming, and misaligned sub-goals across delegation chains. Response: smart-contract escrow links payment to verified outcomes, making misalignment financially costly.

**Authority Gradient** (paper §2.3). Significant capability disparities between delegator and delegatee impede communication and produce under-specified requests. In AI systems, sycophancy and instruction-following bias compound this. Response: explicit task spec hashes, structured role boundaries, and verifier/arbitrator review gates create checkpoints that interrupt blind compliance.

**Zone of Indifference** (paper §2.3). Delegatees develop a range of instructions they execute without critical deliberation. In agentic chains (A -> B -> C), a broad zone of indifference allows subtle intent mismatches to propagate. The paper calls for engineering "dynamic cognitive friction." Response: `rejectByVerifier` and `escalateSilence` are on-chain cognitive friction mechanisms -- they force explicit decisions rather than passive acceptance.

**Transaction Cost Economics** (paper §2.3). The overhead of negotiation, monitoring, and uncertainty must be weighed against the value of delegation. Below a certain complexity floor, delegation overhead exceeds task value. Response: protocol fee snapshotting, gas-aware minimum escrow thresholds (V2), and tiered service levels (V3) that match assurance cost to task criticality.

**Contingency Theory** (paper §2.3). There is no universally optimal delegation structure; the right approach depends on task characteristics (complexity, criticality, uncertainty, duration, verifiability, reversibility). Response: the architecture supports progressive reconfiguration -- fixed roles in V1, adaptive coordination in V2, autonomous re-delegation in V3.

### Paper Framework: Five Pillars

The paper defines intelligent delegation across five pillars (Section 4), nine technical protocols, ethical considerations (Section 5), and protocol integration paths (Section 6). This project implements them across versions:

| Pillar | Paper Sections | Core Requirement | Implementation Layer |
|---|---|---|---|
| **Dynamic Assessment** | 4.1, 4.2 | Task decomposition, capability matching, smart-contract formalization | Settlement kernel (V1) → Bidding marketplace (V2) → Decomposition tooling (V3) |
| **Adaptive Execution** | 4.4 | Runtime re-delegation, failure recovery, checkpoint-based re-allocation | Timeout/escalation (V1) → Milestones + backup agents (V2) → Checkpoint/resume (V3) |
| **Structural Transparency** | 4.5, 4.8 | Monitoring (outcome + process), verifiable task completion, attestation chains | Events + hash commitments (V1) → Attestation chains (V2) → ZK verification (V3) |
| **Scalable Market Coordination** | 4.3, 4.6 | Multi-objective optimization, reputation, trust calibration | Designated trust (V1) → Reputation + credentials (V2) → Market stability (V3) |
| **Systemic Resilience** | 4.7, 4.9 | Permission handling, privilege attenuation, security defense-in-depth | Role gates + reentrancy guard (V1) → DCTs + Sybil resistance (V2) → Tiered assurance (V3) |

The paper also defines ethical dimensions (Section 5):

| Ethical Concern | Paper Section | Architectural Response |
|---|---|---|
| Meaningful human control | 5.1 | Buyer/verifier approval gates; cognitive friction via reject + escalation paths |
| Accountability in long chains | 5.2 | Immutable provenance via on-chain event logs; liability firebreaks per escrow |
| Reliability vs efficiency | 5.3 | Tiered service levels (V3): low-assurance (optimistic) vs high-assurance (verified) |
| Social intelligence | 5.4 | Human-compatible interfaces; MCP + HTTP dual surface; structured dispute resolution |
| Risk of de-skilling | 5.6 | Curriculum-aware task routing (V4); hybrid human-AI market support |

### V1 Paper Coverage

V1 implements the financial settlement kernel -- the paper's foundational layer on which subsequent phases build:

| Paper Concept | V1 Implementation |
|---|---|
| Transfer of authority/responsibility/accountability | Immutable buyer/worker/verifier/arbitrator roles with signed on-chain transitions |
| Task constraints and boundaries | Task spec hash, deadlines, review/dispute windows, role-gated actions |
| Verifiability + reversibility axes (§2.2) | Hash commitments, verifier checks, dispute-mediated payout/refund paths |
| Criticality/risk calibration | High-control fixed-role assignments before open market matching |
| Monitoring requirements (§4.5) | Canonical events for every transition, off-chain indexer |
| Trust calibration (§4.6) | Designated verifier/arbitrator identities; financial outcomes auditable |
| Adaptive coordination (§4.4) | Arbitrator timeout prevents permanent fund lock |
| Dynamic cognitive friction (§2.3) | `rejectByVerifier` + `escalateSilence` force explicit decisions |
| Smart contract as settlement (§4.2) | Escrow holds funds; verification clause gates release |
| Dispute resolution (§4.8) | Arbitrator resolves disputes; split payout via basis points |

### Protocol Integration Strategy

The paper's insight (Section 6): extend existing agent protocols rather than compete with them.

| Protocol | Gap Identified by Paper | Settlement Layer Integration | Phase |
|---|---|---|---|
| **MCP** (Anthropic) | Liability, reputation, trust, conditional settlement | MCP server with escrow tools; future: monitoring stream, DCT-scoped permissions | V1 (complete) |
| **A2A** (Google) | Verification, escrow, conditional settlement | Settlement adapter agent card with `verification_policy` + `escrow_trigger` | V2 |
| **AP2** (Google) | Conditional settlement, milestone releases, clawback | AP2 mandate-to-escrow funding bridge with dispute resolution | V2 |
| **UCP** | Delegation-specific settlement for computational tasks | UCP fulfillment provider exposing escrow lifecycle | V3 |

The paper proposes specific protocol extensions (§6.1) that map to the roadmap: `verification_policy` fields on A2A Task objects, monitoring stream extensions for MCP (L0-L3 granularity), Task_RFQ + Bid_Object schemas, Delegation Capability Tokens (DCTs) based on Macaroons/Biscuits, and checkpoint artifact schemas for adaptive re-delegation.

### Integration Paths

Three integration paths, in priority order:

**Path A: MCP settlement server (V1, complete).** Any MCP-compatible agent can use escrow by connecting to the server. The agent does not need Solidity or wallet libraries -- the MCP server handles chain interaction.

**Path B: A2A settlement adapter (V2).** An A2A-compatible agent whose capability is on-chain settlement of delegated work. Other agents discover it via agent cards; it holds funds in escrow and releases them when work is verified.

**Path C: Reference implementation (ongoing).** The paper proposes protocol extensions as "illustrative examples of the kinds of functionalities that would be possible to include in agentic protocols" (§6.1). A working implementation that demonstrates these ideas in practice can serve as a reference for the ecosystem.

---

## System Architecture

```
┌─────────────────────────────────────────────────────┐
│                   Go Server Binary                  │
│                                                     │
│  ┌─────────────┐  ┌──────────┐  ┌───────────────┐  │
│  │  MCP Server  │  │ HTTP API │  │ Event Indexer │  │
│  │   (stdio)    │  │ (JSON)   │  │  (background) │  │
│  └──────┬───────┘  └────┬─────┘  └───────┬───────┘  │
│         │               │                │          │
│         └───────────┬───┘                │          │
│                     │                    │          │
│              ┌──────┴──────┐    ┌────────┴───────┐  │
│              │ Chain Client│    │   SQLite DB    │  │
│              │ (go-ethereum)│    │                │  │
│              └──────┬──────┘    └────────────────┘  │
└─────────────────────┼───────────────────────────────┘
                      │
              ┌───────┴────────┐
              │  Base Sepolia  │
              │  Contracts     │
              └────────────────┘
```

### Scope Boundary

**On-chain** (source of financial truth):
- Escrow creation, funding, role assignment
- Deadlines, review/dispute windows
- Submission hash recording
- Approval/rejection/dispute actions
- Final payout/refund decisions
- Protocol fee collection
- Immutable event log

**Off-chain** (execution and intelligence):
- Task content, prompts, large outputs
- Verification logic and rubric execution
- Agent runtime/orchestration
- Matching, search, bidding
- Reputation/risk scoring
- Notifications and dashboards

### Chain Selection: Base (Ethereum L2)

Ethereum mainnet settlement costs $5-$50+ per transaction at typical gas prices. A full happy-path escrow lifecycle requires four transactions (create, fund, submit, approve). On mainnet that is $20-$200 in gas alone before the task has economic value -- making delegation unviable for tasks under hundreds of dollars.

Base is an Optimism OP Stack rollup that inherits Ethereum's security guarantees while reducing transaction costs by approximately 100-1000x. The same four-transaction lifecycle costs roughly $0.01-$0.05 on Base, making escrow viable for tasks worth $1+. The contracts, tooling, and security model are identical -- same Solidity, same EVM, same go-ethereum client library. The difference is cost and finality latency (~2s block time on Base vs ~12s on L1, with L1 finality inherited via the rollup's fraud proof window).

The properties required for settlement -- trustless custody, immutable audit trail, permissionless access, no counterparty risk -- are fully preserved on L2. What L2 sacrifices (longer path to L1-equivalent finality, dependence on the sequencer for liveness) is acceptable for a delegation marketplace where tasks already have multi-hour review windows.

Estimated gas costs per operation on Base (as of early 2026):

| Operation | Estimated Gas | Cost at 0.01 gwei L2 gas |
|---|---|---|
| `createEscrow` (factory) | ~350,000 | < $0.01 |
| `fund` | ~65,000 | < $0.01 |
| `submit` | ~85,000 | < $0.01 |
| `approve` + settle | ~120,000 | < $0.01 |
| **Happy-path total** | **~620,000** | **< $0.02** |
| Dispute + resolve path | ~800,000 | < $0.03 |

The **complexity floor** planned for V2 (roadmap item 12) formalizes this: a minimum escrow amount that ensures the task value justifies the gas + protocol fee overhead. On Base, that floor can be low enough ($1-$5) to support micro-delegation between AI agents.

**Chain portability.** The contracts are standard EVM Solidity with no Base-specific dependencies. Deploying to other L2s (Arbitrum, Optimism mainnet, zkSync) or L1 requires only a new RPC URL and chain ID. The Go server's `CHAIN_ID` and `RPC_URL` configuration makes this a deployment decision, not a code change.

### Scalability Assessment

The current architecture -- single Go binary, SQLite, one factory contract -- is deliberately simple for V1. This section assesses which components scale as-is, which require evolution, and what the migration paths look like.

**Components that scale without changes:**

- **On-chain contracts.** Each escrow is an independent contract instance with no shared state bottleneck. The factory is a thin deployer. ERC20 support (V2) is additive. Milestone escrow (V2) and ZK verification slots (V3) extend the escrow contract without changing the factory pattern.
- **On-chain/off-chain boundary.** The design principle -- settle on-chain, everything else off-chain -- is the correct long-term split. Bidding, matching, reputation scoring, task decomposition, and agent orchestration all remain off-chain where they can iterate independently.
- **Go as the server language.** Go's concurrency model, low memory footprint, and single-binary deployment are well-suited through V4+. The go-ethereum client, the MCP SDK, and the HTTP server all scale to high concurrency without architectural changes.
- **MCP + HTTP dual interface.** MCP is the primary agent integration surface; HTTP serves dashboards, external integrations, and tooling. This dual-surface pattern holds through V4 and beyond.

**Components requiring evolution:**

| Component | Current State | Pressure Point | Migration Path | Phase |
|---|---|---|---|---|
| **SQLite** | Single-file embedded DB | Write contention under concurrent marketplace load (bidding, reputation, event indexing) | PostgreSQL. The storage layer uses `database/sql` with hand-written queries -- no ORM lock-in. Swap the driver and adjust SQL dialect differences. | V2 |
| **Event indexer** | Polling every 15s, sequential | Hundreds of active escrows with overlapping lifecycle events; polling lag becomes visible | WebSocket subscription (`eth_subscribe`) for real-time events; parallel per-escrow indexing; event bus for internal pub/sub | V2 |
| **Single process** | MCP + API + indexer in one binary | Indexer CPU/memory spikes affecting API latency; MCP stdio transport limits to one connected agent per process | Separate the indexer into its own process. Run multiple MCP server instances behind streamable-HTTP transport. API remains stateless and horizontally scalable. | V2-V3 |
| **Contract deployment** | One `TaskEscrow` per escrow via `CREATE` | Contract deployment gas overhead at high volume; bytecode duplication | Minimal proxy pattern (EIP-1167 clones). The factory deploys proxy contracts pointing to a single `TaskEscrow` implementation. Reduces per-escrow deploy cost by ~90%. | V2 |
| **Nonce management** | Sequential nonce tracking | Concurrent transaction submission causes nonce collisions | Nonce manager with mutex or pending-pool awareness; or separate signing service | V2 |
| **Authentication** | None (server holds a single private key) | Multi-tenant marketplace where different participants sign their own transactions | Relayer/meta-transaction model, or users submit directly with the server indexing only. ERC-4337 account abstraction is another path. | V2-V3 |
| **Reputation storage** | Not yet implemented | On-chain reputation seed (V2) generates read-heavy query load; behavioral metrics (V3) produce high-write analytical workload | Reputation reads from a materialized view or dedicated read replica. Behavioral metrics use a time-series store or append-only analytics table. | V2-V3 |
| **ZK verification** | Not yet implemented | ZK proof verification on-chain is expensive even on L2 (~300k-500k gas for groth16) | Verify proofs off-chain by default; post only the proof hash on-chain. Optional on-chain verification for high-assurance tasks via a dedicated verifier contract. Consider recursive proofs to amortize cost. | V3 |
| **Cross-chain** | Single-chain (Base) | Agents and liquidity on different L2s | Bridge adapter contracts or cross-chain messaging (LayerZero, Hyperlane). The Go server already parameterizes `CHAIN_ID` and `RPC_URL`, so multi-chain indexing is configuration, not architecture. Settlement contracts deploy per-chain. | V4+ |

**Projected architecture at V4 scale:**

```
                    ┌──────────────────────────────────┐
                    │        Load Balancer              │
                    └──────────┬───────────────────────┘
                               │
              ┌────────────────┼────────────────┐
              │                │                │
    ┌─────────┴──────┐ ┌──────┴───────┐ ┌──────┴───────┐
    │  API Server(s) │ │ MCP Server(s)│ │  A2A Adapter │
    │  (stateless)   │ │ (streamable) │ │  (stateless) │
    └────────┬───────┘ └──────┬───────┘ └──────┬───────┘
             │                │                │
             └────────────────┼────────────────┘
                              │
              ┌───────────────┼───────────────┐
              │               │               │
    ┌─────────┴──────┐ ┌─────┴──────┐ ┌──────┴──────┐
    │   PostgreSQL   │ │  Indexer(s) │ │  Analytics  │
    │  (transactional│ │ (per-chain) │ │ (reputation,│
    │    + replica)  │ │             │ │  metrics)   │
    └────────────────┘ └──────┬──────┘ └─────────────┘
                              │
                    ┌─────────┼─────────┐
                    │         │         │
              ┌─────┴───┐ ┌──┴────┐ ┌──┴────┐
              │  Base   │ │ Arb   │ │  OP   │
              │Contracts│ │Clones │ │Clones │
              └─────────┘ └───────┘ └───────┘
```

None of these migrations require a full rewrite. Each is an incremental change to a specific component behind a stable interface. The storage layer uses `database/sql` -- swap the driver. The chain client is behind an interface -- add chains. The API handlers are stateless -- scale horizontally. The contracts are standard EVM -- deploy anywhere.

The riskiest V4+ item is **cross-chain settlement**, which introduces bridge trust assumptions that partially undermine the trustless escrow guarantee. The mitigation is to keep settlement per-chain (no cross-chain escrow transfers) and handle cross-chain coordination at the off-chain layer.

---

## On-Chain Architecture

### Contracts

- **`TaskEscrowFactory`** (`src/TaskEscrowFactory.sol`) -- creates escrow instances, stores protocol-level configuration (fee basis points, treasury, pause state).
- **`TaskEscrow`** (`src/TaskEscrow.sol`) -- holds escrowed ETH, enforces the lifecycle state machine with role-gated transitions.

### State Machine

```
Created ──fund()──> Funded ──submit()──> Submitted
  │                   │                    │  │  │
  │cancelBeforeFunding│claimTimeoutRefund  │  │  │
  v                   v                    │  │  │
Cancelled          Refunded <──────────────┘  │  │
                      ^                       │  │
                      │claimArbitratorTimeout │  │
                      │                       │  │
                   Disputed <─────────────────┘  │
                      │    (dispute/reject/      │
                      │     escalateSilence)     │
                      │                          │
                   resolveDispute()              │approve()
                      │                          │
                      v                          v
                   Resolved                   Approved
                      │                          │
                      └───────────┬──────────────┘
                                  │
                                  v
                               Settled
```

Nine states: Created, Funded, Submitted, Approved, Disputed, Resolved, Settled, Refunded, Cancelled.

### Roles

| Role | Responsibility |
|---|---|
| `buyer` | Funds escrow, approves or disputes, receives refund on failure |
| `worker` | Submits delivery, receives payout on approval |
| `verifier` | Checks submission quality, can approve or reject |
| `arbitrator` | Final authority in disputed cases |
| `treasury` | Receives protocol fee from successful payouts |

Roles are immutable per escrow in V1.

### Economics

- Protocol fee: basis points on successful payout (snapshotted at escrow creation to prevent governance races).
- ETH-only in V1.
- `workerStake` field reserved (set to 0) for future anti-Sybil bond.

### Trust Model

- Smart contract is custodian (not a marketplace operator).
- Verifier/arbitrator identities are the trust substrate in V1.
- All critical state transitions are auditable and replayable via events.

Full contract specification: [`docs/SPEC_V1.md`](SPEC_V1.md)

---

## Off-Chain Architecture (Go Server)

Single Go binary serving three concerns in one process.

### Package Structure

```
go-server/
  cmd/server/main.go              Entrypoint: wires components, manages lifecycle
  internal/
    config/config.go               Environment variable loading
    chain/
      abi.go                       ABI parsing from embedded Foundry artifacts
      client.go                    go-ethereum ethclient wrapper
      factory.go                   Factory contract binding
      escrow.go                    Escrow contract bindings
    storage/
      db.go                        SQLite open + embedded migration
      models.go                    Go structs
      queries.go                   CRUD operations (database/sql, no ORM)
      migrations/
        001_create_tables.sql      Schema
    indexer/indexer.go             Event polling -> DB reconciliation
    mcpserver/
      server.go                    MCP server setup
      tools.go                     8 tool handlers
    api/
      router.go                    HTTP mux with middleware
      handlers.go                  JSON request/response handlers
      middleware.go                Logging, recovery, CORS
  abi/
    embed.go                       //go:embed for ABI JSON files
    TaskEscrowFactory.json         Copied from Foundry out/ at build time
    TaskEscrow.json
```

### Chain Client

Wraps `go-ethereum/ethclient` with:
- Private key management and EIP-155 transaction signing
- Nonce tracking with lazy initialization
- Gas estimation per transaction
- ABI-encoded method calls for all factory and escrow functions
- Read-only contract calls for status queries

ABIs are embedded at compile time from Foundry artifacts via `//go:embed`.

### Storage

SQLite via `modernc.org/sqlite` (pure Go, no CGO).

| Table | Purpose |
|---|---|
| `tasks` | Task metadata (title, description, spec hash) |
| `escrows` | Escrow records mirroring on-chain state |
| `submissions` | Worker submission records |
| `disputes` | Dispute and resolution records |
| `chain_logs` | Raw chain event log (idempotent by tx_hash + log_index) |
| `chain_cursors` | Indexer block cursor per chain |

### Event Indexer

Background goroutine polling every 15 seconds:

1. Get current block number from RPC
2. Load cursor from DB (or default: current - 250 blocks)
3. Fetch `EscrowCreated` events from factory address
4. For each known escrow, fetch all lifecycle events
5. Map events to status updates
6. Create submission/dispute records from relevant events
7. Idempotent: skip events already in `chain_logs`
8. Update cursor

After any write transaction (via MCP or API), `RunOnce()` is called synchronously for immediate event pickup.

### MCP Tools (Primary Interface)

| Tool | Inputs | Chain Method |
|---|---|---|
| `create_escrow` | title, roles, amount, deadlines | `Factory.createEscrow` |
| `fund_escrow` | escrow_id | `Escrow.fund` |
| `submit_work` | escrow_id, submission_uri | `Escrow.submit` |
| `approve_work` | escrow_id, role | `Escrow.approveByBuyer/Verifier` |
| `dispute_work` | escrow_id, role, reason_uri | `Escrow.dispute/rejectByVerifier/escalateSilence` |
| `resolve_dispute` | escrow_id, worker_award_bps, resolution_uri | `Escrow.resolveDispute` |
| `get_escrow` | escrow_id | DB read |
| `list_escrows` | role, address, status | DB query |

### HTTP API (Secondary Interface)

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/health` | Health check |
| POST | `/api/v1/escrows` | Create escrow |
| GET | `/api/v1/escrows` | List (query: role, address, status) |
| GET | `/api/v1/escrows/{id}` | Get escrow |
| POST | `/api/v1/escrows/{id}/fund` | Fund |
| POST | `/api/v1/escrows/{id}/submit` | Submit work |
| POST | `/api/v1/escrows/{id}/approve` | Approve (body: role) |
| POST | `/api/v1/escrows/{id}/dispute` | Dispute (body: role, reason_uri) |
| POST | `/api/v1/escrows/{id}/resolve` | Resolve (body: worker_award_bps, resolution_uri) |

### Configuration

| Env Var | Required | Default | Description |
|---|---|---|---|
| `RPC_URL` | Yes (for chain ops) | -- | Ethereum JSON-RPC URL |
| `PRIVATE_KEY` | Yes (for writes) | -- | Hex-encoded private key |
| `FACTORY_ADDRESS` | Yes | -- | Deployed factory contract address |
| `CHAIN_ID` | No | `84532` | Chain ID (Base Sepolia) |
| `PORT` | No | `8080` | HTTP server port |
| `DATABASE_URL` | No | `delegation.db` | SQLite database path |
| `MCP_TRANSPORT` | No | -- | Set to `stdio` to enable MCP server |

### Design Decisions

- **Single binary**: MCP + API + indexer share one process. No message queue or process manager required for V1.
- **Pure Go SQLite**: `modernc.org/sqlite` avoids CGO, simplifying cross-compilation and CI.
- **No ORM**: Six tables, stable schema. `database/sql` with hand-written queries.
- **ABI embedding**: `//go:embed` from files copied at build time (`make go-abi`).
- **Shared logic**: MCP tools and HTTP handlers call the same chain + storage + indexer methods.
- **Synchronous indexer after writes**: `RunOnce()` triggered after transaction submission for immediate event pickup.
