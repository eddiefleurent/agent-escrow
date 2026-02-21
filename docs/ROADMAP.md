# Roadmap

Implementation roadmap for the ["Intelligent AI Delegation"](https://arxiv.org/abs/2602.11865) paper (Tomašev, Franklin, Osindero -- Google DeepMind, 2026).

The paper defines five framework pillars, nine technical protocols, ethical considerations, and protocol integration paths. This roadmap traces a path from the settlement kernel through a full delegation marketplace. Each phase is a prerequisite for the next.

---

## Relationship to Coinbase Developer Platform

Several [Coinbase Developer Platform](https://docs.cdp.coinbase.com/) (CDP) products operate in the same ecosystem as this project -- Base, stablecoins, agent tooling -- and address infrastructure layers the paper's framework depends on. The paper calls for extending existing protocols rather than competing with them (§6). These products are natural infrastructure to build on.

### x402 and Bazaar (payment rail and service discovery)

[x402](https://docs.cdp.coinbase.com/x402/welcome) is an open payment protocol that enables instant stablecoin payments over HTTP by reviving the HTTP 402 status code. Its companion [Bazaar](https://docs.cdp.coinbase.com/x402/bazaar) layer provides machine-readable service discovery for payable API endpoints.

x402 and this project address adjacent but distinct problems. x402 provides a stateless, single-interaction payment flow: a client requests a resource, pays, and receives it. This project implements the paper's delegation framework: a stateful, multi-party lifecycle where funds are held in conditional escrow across task assignment, execution, submission, verification, dispute resolution, and settlement. The paper identifies this gap explicitly -- existing payment protocols lack conditionality, milestone releases, clawback, and verification slots (§6).

Where x402 serves as infrastructure for this project:

- **Escrow funding**: x402's [EIP-3009](https://eips.ethereum.org/EIPS/eip-3009) gasless transfers via its facilitator can streamline the escrow funding step, replacing the manual `approve + fund` two-step. The facilitator sponsors gas and handles on-chain settlement; the escrow contract remains the custodial destination. This reduces wallet UX friction for participants who are not crypto-native.
- **Service discovery**: rather than building a standalone discovery index for Task_RFQ, escrow-backed delegation services can be registered on Bazaar alongside simple paid APIs. Bazaar provides the discoverability layer; the bidding protocol (Task_RFQ + Bid_Object) handles negotiation and escrow formalization on top.
- **AP2 mandate funding**: x402 serves as the payment mechanism within the AP2 mandate-to-escrow bridge -- it handles fund movement from mandate authorization into the escrow contract, which then governs conditional custody and release.
- **Complexity floor calibration**: the x402 facilitator fee ($0.001/tx beyond the free tier) plus on-chain gas provides a concrete lower bound for the paper's complexity floor parameter (§4.3) -- delegation overhead must exceed this threshold to justify escrow.

### AgentKit (agent wallet and on-chain identity)

[AgentKit](https://docs.cdp.coinbase.com/agent-kit/welcome) is a toolkit that gives AI agents secure wallet management and on-chain capabilities across any AI framework (LangChain, Vercel AI SDK, MCP). It is model-agnostic, framework-agnostic, and wallet-provider-agnostic, with an extensible action provider system.

The paper's permission handling requirements (§4.7) specify that agents must hold their own cryptographic credentials, that permissions must be scoped to the immediate task via least privilege, and that each participant should sign its own messages for non-repudiation (§4.9). In V1, the Go server holds a single private key and signs all transactions on behalf of every participant -- a deliberate simplification that the paper's framework would not permit at marketplace scale. AgentKit provides the migration path:

- **Agent-owned wallets**: each agent manages its own wallet and signs its own escrow transactions (fund, submit, approve, dispute). The server shifts from transaction signer to indexer-only, resolving the single-key bottleneck.
- **Escrow action provider**: the escrow lifecycle (create, fund, submit, approve, dispute, resolve) can be packaged as a custom AgentKit action provider, making delegation tools available alongside an agent's existing on-chain capabilities (transfers, swaps, contract deployments).
- **Payments MCP as client complement**: [Payments MCP](https://docs.cdp.coinbase.com/payments-mcp/welcome) combines AgentKit wallets with x402 payments in a single MCP server. Agents already running Payments MCP have a wallet and USDC balance ready to fund escrows -- a natural on-ramp into the delegation lifecycle.

### What CDP products do not cover

Everything that distinguishes delegation from payment remains this project's scope: the 9-state escrow machine with conditional release and timeout recovery; dispute resolution through verifier rejection, arbitrator escalation, and silence escalation; worker stake and Sybil resistance; milestones with partial payouts; backup agent re-allocation; on-chain reputation; Delegation Capability Tokens; attestation chains and recursive verification; ZK verification slots; checkpoint/resume for mid-task agent swaps; multi-verifier quorum; and the ethical safeguards the paper defines in Section 5.

---

## Delivery Phases

### V1 -- Settlement Kernel ✓

The foundation: specification, contracts, off-chain server, and production hardening.

- **Specification**: state machine, role semantics, threat model, invariant checklist, paper traceability
- **Contracts**: `TaskEscrowFactory` + `TaskEscrow` with the full 9-state machine, immutable roles, protocol fee snapshotting, arbitrator timeout; Foundry unit, edge case, fuzz, and invariant test suites
- **Off-chain server**: single Go binary -- MCP server + HTTP JSON API + SQLite event indexer; eight MCP tools, nine HTTP endpoints, go-ethereum chain client with ABI bindings
- **Hardening**: receipt parsing, `ChainClient` interface + `MockClient`, health check with RPC verification, structured logging, input validation, CORS, route-aware timeouts, config validation, indexer error propagation, contract reentrancy optimization, role distinctness, two-step ownership transfer

### V2 -- Market Primitives (Current)

Marketplace layer built on top of the settlement kernel.

1. **ERC20/USDC payment support** ✓ -- escrow accepts ERC20 tokens alongside ETH; token field propagated through contracts, storage, indexer, API, and MCP tools
2. **Worker stake activation** ✓ -- `workerStake` field activated as anti-Sybil bond; worker deposits stake via `depositStake()` after buyer funding and before submission; stake returned on approval, forfeited proportionally on dispute, forfeited fully on timeout/arbitrator timeout (paper §4.8: delegatee posts financial stake into escrow prior to execution)
3. **Milestone-based escrow** ✓ -- multiple submission/approval checkpoints within a single escrow with partial payouts; per-milestone submit/approve/dispute/resolve cycles; sequential processing, max 16 milestones, immutable after creation; buyer-only `abortRemainingMilestones()` after terminal failure; worker stake settled at escrow terminal state; propagated through contracts, storage, indexer, MCP tools, HTTP API, and PlantUML diagrams (paper §4.4: smart contracts with pre-agreed executable clauses for adaptive coordination)
4. **Backup agent clause** ✓ -- pre-designated fallback worker if primary defaults, with penalty coverage; `backupWorker` and `backupDeadlineExtension` fields on escrow creation, `activateBackup()` buyer action, stake forfeiture on activation, deadline extension; propagated through contracts, storage, indexer, MCP tools, HTTP API, and documentation (paper §4.4: backup agent auto-re-allocation on failed ZK checkpoint)
5. **On-chain reputation seed** -- factory-level outcome recording per address: tasks completed, disputed, failed (paper §4.6 Table 3: immutable ledger approach)
6. **Complexity floor parameter** -- minimum escrow amount to justify delegation overhead, gas + protocol fee; lower bound calibrated against x402 facilitator fee + on-chain gas (paper §4.3: complexity floor below which delegation overhead exceeds task value)
7. **Task_RFQ + Bid_Object bidding protocol** -- off-chain bidding with on-chain escrow formalization on bid acceptance; service discovery via [x402 Bazaar](https://docs.cdp.coinbase.com/x402/bazaar) (paper §6.1: Task_RFQ broadcast + signed Bid_Objects)
8. **A2A settlement adapter** -- agent card advertising escrow capability; `verification_policy` + `escrow_trigger` fields; discoverable via x402 Bazaar (paper §6: A2A Task object extension)
9. **AP2 mandate-to-escrow bridge** -- AP2 mandate authorization triggers escrow funding via [x402](https://docs.cdp.coinbase.com/x402/welcome) payment rail (EIP-3009 gasless transfer through facilitator into escrow contract) (paper §6: AP2 stake-on-bid + conditional settlement)
10. **Real-time event subscriptions** -- WebSocket/SSE stream for escrow lifecycle events (paper §4.5: configurable granularity L0-L3)
11. **Emergency response protocol** -- credential revocation propagation, contract freeze with fund recovery path (paper §4.9: rapid incident response, recursive credential revocation across chains)

### V3 -- Delegation Intelligence

Full marketplace intelligence and the paper's advanced coordination mechanisms.

12. **Delegation Capability Tokens (DCTs)** -- attenuated permission tokens based on Macaroons/Biscuits, scoped to escrow lifecycle; invalidated on settlement/refund (paper §6.1: restriction chaining across delegation chains)
13. **Verifiable credentials for bidding** -- agents present signed capability attestations during Task_RFQ; delegator filters by domain-specific credentials; credential metadata surfaced via Bazaar discovery extensions (paper §4.6 Table 3: Web of Trust with DIDs)
14. **Attestation chains** -- recursive verification across delegation chains (A -> B -> C); each link produces signed attestation of sub-task completion (paper §4.8: transitive liability, chain of custody)
15. **ZK verification slot** -- optional `proofHash` field on submission for formally verifiable tasks; supports zk-SNARKs/groth16 (paper §4.8: cryptographic verification for trustless automated verification)
16. **Checkpoint/resume** -- standardized state snapshots for mid-task agent swaps; periodic `state_snapshot` commits to shared storage (paper §6.1: checkpoint artifacts + partial compensation clauses)
17. **Tiered service levels** -- low-assurance (optimistic) vs high-assurance (verified) delegation paths with different fee/verification structures (paper §5.3: ensure safety does not become a luxury good)
18. **Market stability mechanisms** -- cooldown periods for re-bidding, damping factors on reputation updates, increasing fees on frequent re-delegation (paper §4.4: prevent oscillation and cascading re-allocations)
19. **Contract-first decomposition tooling** -- MCP tool that helps agents decompose tasks to match available market capabilities; recursive decomposition until sub-tasks match verification capabilities (paper §4.1: decompose until units match formal proofs or automated tests)
20. **Multi-verifier quorum** -- multiple independent verifiers for high-criticality tasks; Schelling point consensus (paper §4.8: game-theoretic verification consensus)
21. **UCP fulfillment provider** -- expose escrow lifecycle as UCP-compatible fulfillment backend for commercial agent transactions (paper §6: UCP architecture extension for abstract computational tasks)

### V4 -- Ethical Safeguards and Ecosystem Maturity

The paper's ethical dimensions (Section 5) and ecosystem-level concerns.

22. **Curriculum-aware task routing** -- track human skill progression; strategically allocate tasks at the boundary of expanding skill sets; AI co-execution with progressive withdrawal (paper §5.6: zone of proximal development, prevent de-skilling)
23. **Liability firebreaks** -- pre-defined contractual stop-gaps in long delegation chains where an agent must assume full non-transitive liability or halt and request updated authority (paper §5.2: prevent accountability vacuum)
24. **Cognitive friction calibration** -- context-aware friction: seamless execution for low-criticality tasks, mandatory justification and manual intervention for high-uncertainty scenarios; alarm fatigue mitigation (paper §5.1: balance cognitive friction against alarm fatigue)
25. **Decentralized identifiers (DIDs)** -- each agent and human participant holds a DID for signing all messages; non-repudiation of communications and contractual agreements (paper §4.9: cryptographic identity layer)
26. **Insurance/liability framework** -- insurance providers safeguard human participation in agentic markets for damages not preempted by technical mechanisms (paper §4.9: insurance for human participants)
27. **Governance layer** -- safety floors for specific task classes (financial transactions, health data) that cannot be bypassed for efficiency; mandatory verification steps (paper §5.3: governance enforces safety floors)

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

**V2**: Milestone-based escrow -- on-chain progress checkpoints with per-milestone verification and partial payouts (paper §4.5: "smart contracts can be used to make the delegatee agent commit to publishing key progress milestones"). Real-time event subscriptions (paper's L0-L3 granularity levels).

**V3**: Attestation chains across delegation links -- each link produces signed attestation of sub-task completion (paper §4.8). ZK verification integration for formally verifiable tasks. Multi-verifier quorum for game-theoretic consensus.

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

| Protocol | Gap (per paper) | Integration | Version |
|---|---|---|---|
| **MCP** | No liability, reputation, trust, or conditional settlement | MCP server with 8 escrow tools; future: monitoring stream (L0-L3), DCT-scoped tool permissions | V1 (complete), V2-V3 |
| **x402** | Stateless payment; no conditionality, dispute resolution, or verification | Gasless escrow funding rail (EIP-3009 via facilitator); AP2 mandate funding mechanism; complexity floor calibration | V2 |
| **x402 Bazaar** | Discovery only; no bidding, negotiation, or capability matching | Service discovery substrate for Task_RFQ; credential metadata via Bazaar extensions | V2-V3 |
| **A2A** | No verification slots, no escrow, assumes trust | Settlement adapter agent card; `verification_policy` + `escrow_trigger` on A2A Task objects; Bazaar-discoverable | V2 |
| **AP2** | No conditional settlement, no milestone releases, no clawback | Mandate-to-escrow funding bridge via x402 payment rail; stake-on-bid Sybil resistance | V2 |
| **UCP** | Optimized for commercial intent, not abstract computational delegation | UCP fulfillment provider exposing escrow lifecycle | V3 |
| **AgentKit** | Agent wallet and on-chain actions; no delegation lifecycle | Agent-owned wallet signing (§4.7 least privilege, §4.9 cryptographic identity); escrow actions as custom action provider | V2-V3 |

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
- Wallet UX friction for non-crypto-native participants (partially mitigated by x402 gasless funding via facilitator in V2)
- Off-chain/on-chain state drift if indexing is unreliable
- Safety becoming a luxury good if high-assurance delegation is too expensive (mitigated by tiered service levels + governance safety floors)
- De-skilling risk for human participants who lose proficiency through reduced engagement (mitigated by curriculum-aware routing in V4)
- Cognitive monoculture if the ecosystem over-depends on a limited number of foundation models (paper §4.9)
