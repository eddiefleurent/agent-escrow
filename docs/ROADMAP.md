# Roadmap

Implementation roadmap for the ["Intelligent AI Delegation"](https://arxiv.org/abs/2602.11865) paper (Tomašev, Franklin, Osindero -- Google DeepMind, 2026).

The paper defines five framework pillars, nine technical protocols, ethical considerations, and protocol integration paths. This roadmap traces a path from the settlement kernel through a full delegation marketplace. Each phase is a prerequisite for the next.

---

## Implementation Status

### Contracts

| Item | Status |
|---|---|
| `TaskEscrow` state machine (9 states) | Complete |
| Immutable roles (buyer/worker/verifier/arbitrator) | Complete |
| Arbitrator timeout (`claimArbitratorTimeout`) | Complete |
| Protocol fee snapshotting | Complete |
| Verifier reject + worker silence escalation | Complete |
| `cancelBeforeFunding` pre-funding exit | Complete |
| `TaskEscrowFactory` with pause/fee/treasury admin | Complete |

### Test Suites

| Suite | Coverage |
|---|---|
| Unit tests (`TaskEscrow.t.sol`) | Happy path, dispute, timeout, verifier reject, silence escalation, arbitrator timeout, role checks |
| Edge cases (`TaskEscrowEdgeCases.t.sol`) | Timing boundaries, invalid BPS, factory pause, fuzz: fund conservation + arbitrator timeout boundary, role distinctness (6 pair combos), two-step ownership transfer (7 tests) |
| Invariants (`TaskEscrowInvariants.t.sol`) | Terminal state stickiness, balance conservation |

### Go Server

| Component | Status |
|---|---|
| Chain client (`internal/chain/`) | Complete |
| `ChainClient` interface for testability | Complete |
| Storage layer (`internal/storage/`) | Complete |
| Event indexer (`internal/indexer/`) | Complete |
| MCP server (`internal/mcpserver/`) | Complete |
| HTTP API (`internal/api/`) | Complete |
| Transaction receipt parsing (EscrowAddress/EscrowID) | Complete |
| API handler tests with mock chain client | Complete (29 tests) |
| Structured logging (`log/slog`, JSON handler) | Complete |
| Chain health check (`/api/v1/health` with RPC verification) | Complete |

### Infrastructure

| Item | Status |
|---|---|
| Makefile (build, test, deploy, go-abi, go-build, go-test, test-all) | Complete |
| Deploy script (`DeployFactory.s.sol`) | Complete |
| CI workflow (`.github/workflows/ci.yml`)  | Complete |
| LICENSE (MIT) | Complete |
| Documentation suite | Complete |

---

## Delivery Phases

### Phase 0 -- Specification

Finalized state machine, role semantics, and edge-case handling. Documented threat model, invariant checklist, and paper traceability.

### Phase 1 -- Contract MVP

Implemented `TaskEscrowFactory` and `TaskEscrow`. Added arbitrator timeout (identified as the primary correctness gap from paper review). Foundry unit, edge case, fuzz, and invariant tests. Deploy script ready.

### Phase 2 -- Off-Chain Server

Single Go binary: MCP server + HTTP JSON API + event indexer. Eight MCP tools, nine HTTP endpoints, SQLite storage, chain client with go-ethereum bindings.

### Phase 3 -- Hardening (Current)

1. Transaction receipt parsing for `EscrowAddress` and `EscrowID` -- complete
2. API/MCP handler tests with mock chain client interface -- complete (22 tests, `ChainClient` interface, `MockClient`)
3. Chain health check with RPC connectivity verification -- complete (returns block number + chain ID, 503 on failure)
4. Structured logging with request context -- complete (`log/slog` with JSON handler)
5. Input validation and error propagation -- complete (ParseUint/SetString checks, time.Parse propagation, nil guards, bounds checks)
6. Deploy to Base Sepolia -- partially complete (deployment guide in [`DEPLOY_PHASE3.md`](DEPLOY_PHASE3.md); actual deployment pending)
7. Reference agent demo -- partially complete (demo walkthrough documented in [`DEPLOY_PHASE3.md`](DEPLOY_PHASE3.md) Part 2; live demo pending)

#### Remaining Hardening (Not Yet Started)

**Contract safety:**
- **Reentrancy guard gas optimization** -- complete (`_locked` uses 1/2 pattern: avoids zero-to-nonzero SSTORE cost, aligns with OpenZeppelin convention)
- **Role address distinctness checks** -- complete (buyer/worker/verifier/arbitrator must be distinct addresses; reverts `RolesNotDistinct` on overlap)
- **Two-step ownership transfer** -- complete (`transferOwnership` + `acceptOwnership` on `TaskEscrowFactory`; prevents permanent lockout on key loss/compromise)

**Server hardening:**
- **Configurable CORS origins** -- complete (`CORS_ORIGINS` env var, comma-separated; empty = wildcard for dev; restricted mode echoes matched origin with `Vary: Origin`)
- **Request timeout middleware** -- complete (route-aware `http.TimeoutHandler`: 10s default for reads via `REQUEST_TIMEOUT`, 90s for chain tx endpoints via `TX_TIMEOUT`; returns JSON `{"error":"request timeout"}` with 503 on expiry)
- **Config validation** -- complete (`Validate()` method on `Config`: requires `PRIVATE_KEY` and `FACTORY_ADDRESS` when `RPC_URL` is set; validates hex format and byte length; checks port range and timeout positivity; offline mode preserved when `RPC_URL` is empty with warning; 25 tests)
- **Indexer error propagation** -- complete (fatal errors surfaced via buffered `Err()` channel after configurable consecutive failure threshold; `main.go` selects on indexer error channel for graceful shutdown; `WithMaxConsecutiveFailures` + `WithPollInterval` options; 10 tests)
- **MockClient thread safety** -- protect mutable fields (`BlockNum`, `Logs`, `Receipt`, `StatusVal`, etc.) with mutex in read methods; currently safe for typical test patterns but would race under parallel test mutation
- **SubmissionDeadline type consistency** -- change `Escrow.SubmissionDeadline` from `string` to `int64` (Unix timestamp) for consistency with `ReviewPeriodSeconds`/`DisputePeriodSeconds`/`ArbitratorTimeoutSeconds`; requires DB schema migration and updates across handlers, MCP tools, indexer, and tests

### Phase 4 -- Market Primitives

Marketplace layer built on top of the settlement kernel.

7. **ERC20/USDC payment support** -- extend escrow to accept ERC20 tokens alongside ETH
8. **Worker stake activation** -- enable the reserved `workerStake` field as anti-Sybil bond (paper §4.8: delegatee posts financial stake into escrow prior to execution)
9. **Milestone-based escrow** -- multiple submission/approval checkpoints within a single escrow with partial payouts (paper §4.4: smart contracts with pre-agreed executable clauses for adaptive coordination)
10. **Backup agent clause** -- pre-designated fallback worker if primary defaults, with penalty coverage (paper §4.4: backup agent auto-re-allocation on failed ZK checkpoint)
11. **On-chain reputation seed** -- factory-level outcome recording per address: tasks completed, disputed, failed (paper §4.6 Table 3: immutable ledger approach)
12. **Complexity floor parameter** -- minimum escrow amount to justify delegation overhead, gas + protocol fee (paper §4.3: complexity floor below which delegation overhead exceeds task value)
13. **Task_RFQ + Bid_Object bidding protocol** -- off-chain bidding with on-chain escrow formalization on bid acceptance (paper §6.1: Task_RFQ broadcast + signed Bid_Objects)
14. **A2A settlement adapter** -- agent card advertising escrow capability; `verification_policy` + `escrow_trigger` fields (paper §6: A2A Task object extension)
15. **AP2 mandate-to-escrow bridge** -- AP2 mandate authorization triggers escrow funding (paper §6: AP2 stake-on-bid + conditional settlement)
16. **Real-time event subscriptions** -- WebSocket/SSE stream for escrow lifecycle events (paper §4.5: configurable granularity L0-L3)
17. **Emergency response protocol** -- credential revocation propagation, contract freeze with fund recovery path (paper §4.9: rapid incident response, recursive credential revocation across chains)

### Phase 5 -- Delegation Intelligence

Full marketplace intelligence and the paper's advanced coordination mechanisms.

18. **Delegation Capability Tokens (DCTs)** -- attenuated permission tokens based on Macaroons/Biscuits, scoped to escrow lifecycle; invalidated on settlement/refund (paper §6.1: restriction chaining across delegation chains)
19. **Verifiable credentials for bidding** -- agents present signed capability attestations during Task_RFQ; delegator filters by domain-specific credentials (paper §4.6 Table 3: Web of Trust with DIDs)
20. **Attestation chains** -- recursive verification across delegation chains (A -> B -> C); each link produces signed attestation of sub-task completion (paper §4.8: transitive liability, chain of custody)
21. **ZK verification slot** -- optional `proofHash` field on submission for formally verifiable tasks; supports zk-SNARKs/groth16 (paper §4.8: cryptographic verification for trustless automated verification)
22. **Checkpoint/resume** -- standardized state snapshots for mid-task agent swaps; periodic `state_snapshot` commits to shared storage (paper §6.1: checkpoint artifacts + partial compensation clauses)
23. **Tiered service levels** -- low-assurance (optimistic) vs high-assurance (verified) delegation paths with different fee/verification structures (paper §5.3: ensure safety does not become a luxury good)
24. **Market stability mechanisms** -- cooldown periods for re-bidding, damping factors on reputation updates, increasing fees on frequent re-delegation (paper §4.4: prevent oscillation and cascading re-allocations)
25. **Contract-first decomposition tooling** -- MCP tool that helps agents decompose tasks to match available market capabilities; recursive decomposition until sub-tasks match verification capabilities (paper §4.1: decompose until units match formal proofs or automated tests)
26. **Multi-verifier quorum** -- multiple independent verifiers for high-criticality tasks; Schelling point consensus (paper §4.8: game-theoretic verification consensus)
27. **UCP fulfillment provider** -- expose escrow lifecycle as UCP-compatible fulfillment backend for commercial agent transactions (paper §6: UCP architecture extension for abstract computational tasks)

### Phase 6 -- Ethical Safeguards and Ecosystem Maturity

The paper's ethical dimensions (Section 5) and ecosystem-level concerns.

28. **Curriculum-aware task routing** -- track human skill progression; strategically allocate tasks at the boundary of expanding skill sets; AI co-execution with progressive withdrawal (paper §5.6: zone of proximal development, prevent de-skilling)
29. **Liability firebreaks** -- pre-defined contractual stop-gaps in long delegation chains where an agent must assume full non-transitive liability or halt and request updated authority (paper §5.2: prevent accountability vacuum)
30. **Cognitive friction calibration** -- context-aware friction: seamless execution for low-criticality tasks, mandatory justification and manual intervention for high-uncertainty scenarios; alarm fatigue mitigation (paper §5.1: balance cognitive friction against alarm fatigue)
31. **Decentralized identifiers (DIDs)** -- each agent and human participant holds a DID for signing all messages; non-repudiation of communications and contractual agreements (paper §4.9: cryptographic identity layer)
32. **Insurance/liability framework** -- insurance providers safeguard human participation in agentic markets for damages not preempted by technical mechanisms (paper §4.9: insurance for human participants)
33. **Governance layer** -- safety floors for specific task classes (financial transactions, health data) that cannot be bypassed for efficiency; mandatory verification steps (paper §5.3: governance enforces safety floors)

---

## Paper Framework Mapping

How each version maps to the five pillars from ["Intelligent AI Delegation"](https://arxiv.org/abs/2602.11865) (Tomašev et al., 2026).

### Pillar 1: Dynamic Assessment (Sections 4.1-4.2)

**Paper**: Delegators must dynamically assess delegatee competence, match capabilities to tasks, and formalize agreements into smart contracts. Decentralized market hubs where agents advertise tasks and others bid. Contract-first decomposition makes delegation contingent on outcomes having precise verification.

**V1**: Fixed-role assignment via `createEscrow` with `taskSpecHash` commitment. No capability matching or bidding -- the settlement kernel is the necessary prerequisite.

**V2**: Task_RFQ broadcast + Bid_Object schema (paper §6.1). Factory becomes backend for accepted bids. A2A agent card integration for capability discovery. Complexity floor parameter.

**V3**: Contract-first decomposition tooling -- MCP tool for recursive task decomposition until sub-tasks match verification capabilities (formal proofs, automated tests).

### Pillar 2: Adaptive Execution (Section 4.4)

**Paper**: Static execution plans are insufficient. Adaptive coordination handles external triggers (specification changes, cancellation, resource shifts) and internal triggers (performance degradation, budget overrun, verification failure, unresponsiveness). Smart contracts should contain pre-agreed executable clauses for adaptive coordination, including backup agents and checkpoint-based re-allocation. Market-level stability measures prevent oscillation.

**V1**: `rejectByVerifier`, `escalateSilence`, `claimTimeoutRefund`, `claimArbitratorTimeout` handle failure cases and unresponsiveness. No mid-task re-delegation.

**V2**: Milestone-based escrow with partial payouts. Backup agent clause with penalty coverage. Emergency response protocol.

**V3**: Checkpoint state snapshots for mid-task agent swaps. Market stability mechanisms (cooldown, damping, re-delegation fees).

### Pillar 3: Structural Transparency (Sections 4.5, 4.8)

**Paper**: Monitoring must cover outcome-level and process-level, direct and indirect, black-box and white-box, with privacy considerations across five axes (target, observability, transparency, privacy, topology). Smart contracts commit agents to publishing key progress milestones. Verifiable task completion transforms provisional output into settled fact. Verification mechanisms include direct inspection, trusted third-party auditing, cryptographic proofs (ZK), and game-theoretic consensus. In delegation chains, verification and liability are recursive.

**V1**: Canonical events for every state transition. Submission hash commitments. Event indexer for off-chain reconciliation. MCP tools for monitoring. Verifier/buyer approval for direct outcome verification. Arbitrator for third-party auditing.

**V2**: Attestation chains across delegation links -- each link produces signed attestation of sub-task completion. Real-time event subscriptions (paper's L0-L3 granularity levels).

**V3**: ZK verification integration for formally verifiable tasks. Multi-verifier quorum for game-theoretic consensus.

### Pillar 4: Scalable Market Coordination (Sections 4.3, 4.6)

**Paper**: Multi-objective optimization across cost, quality, speed, privacy, and uncertainty. Pareto optimality. Reputation via immutable ledger (Table 3), Web of Trust (DIDs + verifiable credentials), and behavioral metrics. Trust governs autonomy granted and oversight level. Graduated authority: low-trust agents face strict constraints, high-reputation agents operate with minimal intervention. Complexity floor below which delegation overhead exceeds task value.

**V1**: Designated verifier/arbitrator identities serve as the trust substrate.

**V2**: On-chain reputation seed (immutable ledger approach). Complexity floor parameter. Worker stake as trust signal. Task_RFQ + Bid_Object bidding protocol.

**V3**: Verifiable credentials (Web of Trust model). Market stability measures. Multi-verifier quorum. Re-delegation fees. Behavioral metrics.

### Pillar 5: Systemic Resilience (Sections 4.7, 4.9)

**Paper**: Permission handling must balance efficiency with safety. Privilege attenuation in delegation chains -- sub-delegatees receive strictly scoped subsets via Delegation Capability Tokens (DCTs) based on Macaroons/Biscuits. Security threats include malicious delegatee (data exfiltration, poisoning, verification subversion), malicious delegator (harmful tasks, probing, reputation sabotage), and ecosystem-level concerns (Sybil attacks, collusion, protocol exploitation, cognitive monoculture). Defense-in-depth: TEEs, least privilege, prompt sanitization, DIDs. Rapid incident response with recursive credential revocation.

**V1**: Role-gated actions enforce least privilege per escrow. Reentrancy guard. Factory pause for emergencies. Arbitrator timeout prevents permanent fund lock.

**V2**: DCT integration -- MCP tool permissions scoped to active escrow lifecycle, invalidated on settlement/refund. Emergency response protocol with credential revocation propagation. AP2 stake-on-bid for Sybil resistance.

**V3**: Tiered service levels (low-assurance vs high-assurance). Restriction chaining across delegation chains via DCT attenuation. DID-based identity layer.

### Ethical Dimensions (Section 5)

**Paper**: Technical protocols alone cannot resolve all sociotechnical concerns. Meaningful human control must resist automation bias and the zone of indifference. Long delegation chains risk accountability vacuums. Safety must not become a luxury good. AI delegation threatens human skill preservation through the paradox of automation. Social intelligence requires agents to respect human dignity, team cohesion, and the authority gradient.

**V1**: Buyer/verifier approval gates provide cognitive friction. Immutable on-chain event logs maintain provenance.

**V2**: Structured dispute resolution with human-compatible interfaces (MCP + HTTP dual surface).

**V3**: Tiered service levels ensure minimum viable reliability for all participants. Cognitive friction calibration with context-aware intensity.

**V4**: Curriculum-aware task routing for human skill preservation. Liability firebreaks in long chains. Insurance/liability frameworks. Governance-enforced safety floors.

### Protocol Integration (Section 6)

**Paper**: Extend existing protocols rather than compete with them. MCP lacks a policy/accountability layer. A2A lacks verification and escrow. AP2 lacks conditional settlement. UCP lacks abstract task delegation support.

| Protocol | Gap (per paper) | Integration | Phase |
|---|---|---|---|
| **MCP** | No liability, reputation, trust, or conditional settlement | MCP server with 8 escrow tools; future: monitoring stream (L0-L3), DCT-scoped tool permissions | V1 (complete), V2-V3 |
| **A2A** | No verification slots, no escrow, assumes trust | Settlement adapter agent card; `verification_policy` + `escrow_trigger` on A2A Task objects | V2 |
| **AP2** | No conditional settlement, no milestone releases, no clawback | AP2 mandate-to-escrow funding bridge; stake-on-bid Sybil resistance | V2 |
| **UCP** | Optimized for commercial intent, not abstract computational delegation | UCP fulfillment provider exposing escrow lifecycle | V3 |

---

## Beyond V4

Long-horizon items the paper acknowledges as open research:

- Autonomous multi-agent delegation networks at web scale (paper §4.4)
- Full game-theoretic verification consensus at TrueBit scale (paper §4.8)
- Cross-chain settlement adapters
- Homomorphic encryption for privacy-preserving task execution (paper §4.5)
- TEE-based secure execution environments for sensitive delegation (paper §4.9)

## Success Metrics

- Escrow completion rate
- Average settlement time
- Dispute rate and resolution time
- Failed/abandoned task ratio
- Gas cost per lifecycle completion
- Marketplace liquidity (V2+): bid-to-task ratio, time-to-match
- Reputation signal quality (V2+): correlation between reputation score and task success
- Re-delegation rate (V3+): frequency of adaptive mid-task switches

## Known Risks

- Verifier/arbitrator centralization in V1 (mitigated by multi-verifier quorum in V3)
- Poor task specification causing avoidable disputes (mitigated by contract-first decomposition tooling in V3)
- Wallet UX friction for non-crypto-native participants
- Off-chain/on-chain state drift if indexing is unreliable
- Safety becoming a luxury good if high-assurance delegation is too expensive (mitigated by tiered service levels + governance safety floors)
- De-skilling risk for human participants who lose proficiency through reduced engagement (mitigated by curriculum-aware routing in V4)
- Cognitive monoculture if the ecosystem over-depends on a limited number of foundation models (paper §4.9)
