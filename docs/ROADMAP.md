# Roadmap

Implementation roadmap for the ["Intelligent AI Delegation"](https://arxiv.org/abs/2602.11865) paper (Tomasev, Franklin, Osindero -- Google DeepMind, 2026).

**Current phase: V3 closeout (item 22), with V4 execution planning active.**

---

## How To Read This Roadmap

- **Canonical mapping file:** `docs/paper-feature-map.json` is the machine-readable source of truth for paper coverage, gap tracking, and per-item design decisions.
- **Status legend:** `done` = implemented, `in_progress` = currently being delivered, `planned` = scoped but not implemented.
- **Alignment legend:**
  - `direct` = closely follows paper prescription.
  - `opinionated` = paper-aligned, but concrete mechanism is a project decision.
  - `gap_fill` = added after review because paper expectation was under-tracked.

---

## Snapshot (2026-03-01)

- **Built strongly:** settlement kernel, market primitives, DCT/authz, attestation chains, checkpoints, ZK slot, quorum, stability controls.
- **Main V3 remaining item:** `22` (UCP fulfillment provider).
- **Previously under-tracked paper requirements now explicit:** social intelligence (`29`) and user training (`30`).
- **Human delegatees:** supported in model and metadata (`delegate_preference`) but not yet policy-enforced routing.
- **Monitoring depth:** L0/L1 production; L2/L3 remains staged.
- **Opinionated implementations worth revisiting now:** items `12`, `15`, `20`, and `21`.

Recommended near-term order: `22 -> R1 -> R2 -> R3 -> R4 -> 25 -> 29 -> 30 -> 23 -> 24 -> 26 -> 28 -> 27`.

---

## Protocol Integration Principles

- Extend existing protocols (MCP, A2A, AP2, UCP, x402) instead of replacing them.
- Keep escrow as the conditional settlement/accountability layer; keep protocol transports interoperable.
- Evaluate transport compatibility across MCP, HTTP, and CLI case by case; avoid compatibility shims unless explicitly requested.
- Favor incremental integration paths that can ship safely on Base before deeper cross-chain/privacy work.

---

## Item Register: Paper Intent vs Design Decisions

### V2 -- Market Primitives

| ID | Item | Status | Paper refs | Alignment | Design decision vs paper |
|---|---|---|---|---|---|
| 1 | ERC20/USDC payment support | done | 4.2, 6 | direct | Native ETH/ERC20 support in escrow contracts instead of depending on external rails only. |
| 2 | Worker stake activation | done | 4.8 | opinionated | Stake is optional and escrow-configurable rather than mandatory for every task. |
| 3 | Milestone-based escrow | done | 4.4, 4.5, 6.1 | opinionated | Fixed sequential milestone model (max 16) chosen for deterministic settlement and bounded complexity. |
| 4 | Backup agent clause | done | 4.4 | opinionated | Pre-designated backup worker with explicit activation, not automatic market rematching. |
| 5 | On-chain reputation seed | done | 4.6 | opinionated | Started with immutable counters/events before richer trust portfolio models. |
| 6 | Complexity floor parameter | done | 4.3 | opinionated | Single configurable floor enforced on-chain/off-chain instead of dynamic per-task optimizer. |
| 7 | Task_RFQ + Bid_Object bidding protocol | done | 4.2, 6.1 | direct | Off-chain negotiation with on-chain formalization only when bid is accepted. |
| 8 | A2A settlement adapter | done | 6, 6.1 | direct | Added escrow-specific task metadata while keeping A2A transport compatibility. |
| 9 | AP2 mandate-to-escrow bridge | done | 6, 6.1 | direct | AP2 mandates routed through x402 into escrow `fundWithAuthorization()`. |
| 10 | Real-time event subscriptions | done | 4.5, 6.1 | opinionated | Delivered L0/L1 first; deferred L2/L3 for staged rollout and privacy/control concerns. |
| 11 | Emergency response protocol | done | 4.9 | opinionated | Owner-governed freeze/unfreeze/emergency resolve as initial incident-response baseline. |

### V3 -- Delegation Intelligence

| ID | Item | Status | Paper refs | Alignment | Design decision vs paper |
|---|---|---|---|---|---|
| 12 | Sealed bidding | done | 4.5, 6.1 | opinionated | Commit-reveal chosen as deployable first privacy layer before selective-disclosure systems. |
| 13 | Delegation Capability Tokens (DCTs) | done | 4.7, 6.1 | direct | Attenuated capabilities implemented off-chain with strict scope and lifecycle invalidation. |
| 13a | Canonical token profile hardening | done | 4.7, 6.1 | opinionated | Hard-cutoff to a single canonical profile (`dct-profile-v1`) to avoid legacy ambiguity. |
| 13b | Principal authorization layer for DCT ops | done | 4.7, 4.9 | direct | Default-deny authz with audited decisions across MCP/HTTP/CLI callers. |
| 14 | Verifiable credentials for bidding | done | 4.6, 6.1 | opinionated | secp256k1 attestation profile now, with optional `issuer_did` for forward DID compatibility. |
| 15 | Attestation chains | done | 4.8 | opinionated | Recursive attestation verification is off-chain to avoid contract size and gas overhead. |
| 16 | Checkpoint/resume | done | 4.4, 6.1 | opinionated | Checkpoint artifacts stored off-chain with active-worker commit rights. |
| 17 | Tiered service levels | done | 5.3 | direct | Tier and fee snapshots are immutable per escrow to prevent governance races. |
| 18 | ZK verification slot | done | 4.8 | opinionated | Optional ZK verifier path, not mandatory ZK in all settlement flows. |
| 19 | Multi-verifier quorum | done | 4.8 | opinionated | Bounded verifier panel and stake game as practical consensus mechanism. |
| 20 | Market stability mechanisms | done | 4.4 | opinionated | Concrete policy knobs: cooldowns, surcharge windows, damped overlays. |
| 21 | Contract-first decomposition tooling | done | 4.1 | opinionated | Human/AI preference and market depth signals are advisory in V3 (not hard gates). |
| 22 | UCP fulfillment provider | planned | 6 | direct | Expose escrow lifecycle as UCP-compatible fulfillment backend. |

### V4 -- Ethical Safeguards and Ecosystem Maturity (ordered by planned implementation sequence)

| ID | Item | Status | Paper refs | Alignment | Design decision vs paper |
|---|---|---|---|---|---|
| 25 | Cognitive friction calibration | planned | 5.1 | direct | Context-sensitive friction to avoid both blind automation and alert fatigue. |
| 29 | Social intelligence safeguards | planned | 5.4 | gap_fill | Added after review: explicit anti-micromanagement and authority-gradient handling in hybrid teams. |
| 30 | User training and certification tracks | planned | 5.5 | gap_fill | Added after review: operator readiness, AI literacy, and oversight certification pathways. |
| 23 | Curriculum-aware task routing | planned | 5.6 | direct | Intentionally route boundary-learning tasks to preserve human capability. |
| 24 | Liability firebreaks | planned | 5.2 | direct | Explicit contractual stop-gaps: assume non-transitive liability or halt for renewed authority. |
| 26 | Decentralized identifiers (DIDs) | planned | 4.6, 4.9 | direct | Move from address-only identity toward DID-backed message signing and trust proofs. |
| 28 | Governance layer | planned | 5.3 | direct | Mandatory safety floors for sensitive task classes. |
| 27 | Insurance/liability framework | planned | 4.9, 5.1 | direct | External risk backstop for harms outside technical prevention envelope. |

---

## Action Plan

### Phase 1: Finalize V3

#### 22: UCP fulfillment provider

- [ ] **Domain model mapping:** map escrow lifecycle states to UCP fulfillment state machine.
- [ ] **Protocol adapter:** implement UCP ingestion/response paths without duplicating business logic.
- [ ] **Interface parity:** expose functionality through MCP, HTTP, and `escrow-cli`.
- [ ] **Settlement semantics:** preserve escrow conditionality (verification, dispute, refund) through UCP mappings.
- [ ] **Tests and docs:** end-to-end tests plus request/response examples for integrators.

#### V3 closeout gates

- [ ] `22` implemented and documented.
- [ ] No interface-parity regressions (MCP/HTTP/CLI).
- [ ] `docs/paper-feature-map.json` updated with final V3 status.
- [ ] `docs/ARCHITECTURE.md` and `README.md` updated in the same PR.

### Phase 2: Revisit High-Impact Opinionated Decisions

#### R1 (item 12): Sealed bidding hardening

- [ ] Add anti-grief controls for commit-without-reveal behavior.
- [ ] Define deterministic fallback rules when top commits are not revealed.
- [ ] Evaluate transport compatibility across MCP, HTTP, and CLI case by case; avoid compatibility shims unless explicitly requested.

#### R2 (item 15): Attestation-chain anchoring

- [ ] Add optional on-chain commitment of attestation root hash on submit.
- [ ] Add proof-of-inclusion retrieval and verification helpers.
- [ ] Define dispute-time checks when submitted chain data mismatches anchored roots.

#### R3 (item 20): Market stability parameter recalibration

- [ ] Build replay/simulation harness for surcharge, cooldown, and damping settings.
- [ ] Add guardrails for parameter ranges by task class/risk profile.
- [ ] Add governance-safe rollout path for parameter updates (staged + auditable).

#### R4 (item 21): Human delegate preference enforcement modes

- [ ] Introduce policy modes (`advisory`, `require_human`, `require_ai`, `any`) scoped by risk/task class.
- [ ] Enforce policy mode during decomposition finalize and bid acceptance paths.
- [ ] Record explicit override reason/audit events for exception handling.

### Phase 3: Human Control Foundations (V4)

#### 25: Cognitive friction calibration

- [ ] Risk scoring for tasks and transitions.
- [ ] Trigger matrix for manual confirmation and escalation.
- [ ] Alarm-fatigue controls (rate limits, confidence thresholds, bundling).

#### 29: Social intelligence safeguards

- [ ] Policy profiles for AI-as-delegator to human worker interactions.
- [ ] Anti-micromanagement constraints and dignity-preserving defaults.
- [ ] Authority-gradient handling (challenge/override expectations).

#### 30: User training/certification

- [ ] Operator training modules for delegators, delegatees, and overseers.
- [ ] Certification metadata model and verification checks.
- [ ] Runtime UX hooks that adapt to operator certification level.

### Phase 4: Routing and Liability (V4)

#### 23: Curriculum-aware routing

- [ ] Skill graph and progression model.
- [ ] Routing policy that balances throughput with skill maintenance.
- [ ] Explainability surface for routing rationale.

#### 24: Liability firebreaks

- [ ] Contract clauses for non-transitive liability and halt-for-reauthorization.
- [ ] Chain-level provenance requirements at firebreak points.
- [ ] Dispute and recourse flow for downstream failures.

### Phase 5: Identity, Governance, and Insurance (V4)

#### 26: DID identity layer

- [ ] DID method support and key rotation model.
- [ ] DID-backed signatures for high-assurance protocol messages.
- [ ] Revocation/status checks integrated into authz decisions.

#### 28: Governance layer

- [ ] Task-class taxonomy (financial, health, legal, etc.).
- [ ] Non-bypassable minimum verification policies by class.
- [ ] Governance process for policy changes with audit trails.

#### 27: Insurance/liability framework

- [ ] Claim trigger model tied to escrow outcomes and incident events.
- [ ] Coverage metadata attachment to escrows/rfqs.
- [ ] Payout and adjudication integration boundaries.

---

## Beyond V4 (V5+)

Long-horizon items that remain important but are not V3/V4 blockers:

- **31. Selective-disclosure privacy layer:** private bidding, private reputation proofs, confidential delegation topology (ZK/sidecar approaches).
- **32. TEE and remote attestation integration:** stronger execution assurance for sensitive workloads.
- **33. Cross-chain coordination:** multi-chain deployments without weakening per-chain settlement guarantees.
- **34. Advanced verification games at larger scale:** broader verifier markets and anti-collusion economics.
- **35. Privacy-preserving computation expansion:** MPC/FHE where zk proofs alone are not enough.

---

## Success Metrics

- Escrow completion rate and median settlement time.
- Dispute rate, dispute-resolution latency, and post-hoc correction frequency.
- Bid-to-task ratio, time-to-match, and re-delegation frequency.
- Human-safeguard metrics (manual intervention quality, false-alarm rate, oversight load).
- Skill-preservation metrics for curriculum-aware routing.
- Cross-interface parity incidents (target: zero feature skew across MCP/HTTP/CLI).

## Key Risks

- V3 closeout delay if UCP mapping overreaches into protocol redesign.
- Human-participant safeguards lag behind technical feature velocity.
- Safety controls becoming cost-prohibitive without governance floor enforcement.
- Reputation and monitoring signals being overfit or gamed without periodic calibration.
- Privacy expectations outpacing current on-chain transparency model.
