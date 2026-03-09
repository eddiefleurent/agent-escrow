# Roadmap

Implementation roadmap for the ["Intelligent AI Delegation"](https://arxiv.org/abs/2602.11865) paper (Tomašev, Franklin, Osindero -- Google DeepMind, 2026).

**Current phase: Phase 2 (R1-R4 revisits), after V3 closeout completion. Pre-V4 security gate (SH1-SH5) must complete before V4 work starts.**

---

## How To Read This Roadmap

- **Repo entrypoint:** start with `AGENTS.md` for the repo harness and `docs/ARCHITECTURE.md` for stable system boundaries. This roadmap owns sequencing, not the full architecture narrative.
- **Canonical mapping file:** `docs/paper-feature-map.json` is the canonical, machine-readable source of truth for paper coverage, gap tracking, and per-item design decisions. The **Item Register** tables below are human-readable summaries that must be kept in sync with that JSON.
- **Status legend:** `done` = implemented, `in_progress` = currently being delivered, `planned` = scoped but not implemented.
- **Alignment legend:**
  - `direct` = closely follows paper prescription.
  - `opinionated` = paper-aligned, but concrete mechanism is a project decision.
  - `gap_fill` = added after review because paper expectation was under-tracked.

---

## Snapshot (2026-03-01)

- **Built strongly:** settlement kernel, market primitives, DCT/authz, attestation chains, checkpoints, ZK slot, quorum, stability controls.
- **V3 status:** complete (items `12`-`22` implemented).
- **Previously under-tracked paper requirements now explicit:** social intelligence (`29`) and user training (`30`).
- **Human delegatees:** supported in model and metadata (`delegate_preference`) but not yet policy-enforced routing.
- **Monitoring depth:** L0/L1 production; L2/L3 remains staged.
- **Opinionated implementations worth revisiting now:** items `12`, `15`, `20`, and `21`.

Recommended near-term order: `R1 -> R2 -> R3 -> R4 -> SH1 -> SH2 -> SH3 -> SH4 -> SH5 -> 25 -> 29 -> 30 -> 23 -> 24 -> 26 -> 28 -> 27`.

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
| 22 | UCP fulfillment provider | done | 6 | direct | UCP adapter implemented as an interoperability envelope over shared escrow orchestration; escrow remains settlement source of truth. |

### Pre-V4 Security Gate (must ship before any V4 work)

| ID | Item | Status | Alignment | Notes |
|---|---|---|---|---|
| SH1 | Admin auth for emergency endpoints | planned | gap_fill | Bearer token middleware gating all `/api/v1/emergency/*` and `/api/v1/dcts/emergency-override` routes. Token configured via env var. No contract-layer guard exists for these endpoints; any caller can currently trigger emergency freeze/resolve on-chain. |
| SH2 | Per-IP rate limiting on POST endpoints | planned | gap_fill | Token-bucket rate limiter on all POST routes. Prevents gas drain via transaction spam and RPC quota exhaustion. No ceiling currently exists. |
| SH3 | Request body size cap | planned | gap_fill | `http.MaxBytesReader` before decode on all handlers. Prevents memory exhaustion from unbounded body reads. |
| SH4 | Request ID tracing | planned | gap_fill | `X-Request-ID` header echoed in responses and included in all structured log entries. Required for operational traceability before real traffic hits. |
| SH5 | Agent-owned wallet / client-side signing | planned | gap_fill | CLI and MCP tools sign and broadcast transactions locally using the caller's `PRIVATE_KEY` + `RPC_URL`. Server becomes a pure coordination and indexing layer; its `PRIVATE_KEY` is for operator/admin operations only (emergency, indexer gas). Blocks real multi-agent demos and any production deployment where buyer, worker, and verifier are different principals. |

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

### Phase 1: Finalize V3 (Complete)

#### 22: UCP fulfillment provider

- [x] **Domain model mapping:** mapped escrow lifecycle states to UCP fulfillment state machine.
- [x] **Protocol adapter:** implemented UCP ingestion/response paths on shared escrow orchestration.
- [x] **Interface parity:** exposed UCP operations across MCP, HTTP, and `escrow-cli`.
- [x] **Settlement semantics:** preserved escrow conditionality (verification, dispute, refund) through UCP mappings.
- [x] **Tests and docs:** added UCP-focused service/handler tests and integrated docs updates.

#### V3 closeout gates

- [x] `22` implemented and documented.
- [x] No interface-parity regressions for features designated as multi-transport (MCP/HTTP/CLI).
- [x] `docs/paper-feature-map.json` updated with final V3 status.
- [x] `docs/ARCHITECTURE.md` and `README.md` updated in the same PR.

### Phase 2: Revisit High-Impact Opinionated Decisions

#### R1 (item 12): Sealed bidding hardening

- [ ] Add anti-grief controls for commit-without-reveal behavior.
- [ ] Define deterministic fallback rules when top commits are not revealed.
- [ ] Ensure sealed-bid commit/reveal semantics are consistent across transports that surface sealed-bidding operations.

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

### Pre-V4 Security Gate

All five items below are **blocking gates**: V4 work does not start until all are done.

#### SH1: Admin auth for emergency endpoints

- [ ] Add `ADMIN_TOKEN` env var (required when `EMERGENCY_ENABLED=true`; startup fails if absent).
- [ ] Add middleware that checks `Authorization: Bearer <ADMIN_TOKEN>` on all `/api/v1/emergency/*` and `/api/v1/dcts/emergency-override` routes before routing to handlers.
- [ ] Return `401 Unauthorized` with no body leakage on mismatch.
- [ ] Add middleware unit tests covering present/absent/wrong token cases.
- [ ] Update `docs/SETUP.md` with `ADMIN_TOKEN` provisioning instructions.

#### SH2: Per-IP rate limiting on POST endpoints

- [ ] Implement token-bucket rate limiter in middleware (stdlib only; no external deps).
- [ ] Apply to all POST routes; exempt GET and SSE/WebSocket endpoints.
- [ ] Configurable via `RATE_LIMIT_RPS` and `RATE_LIMIT_BURST` env vars with sane defaults (e.g. 10 RPS, burst 20).
- [ ] Return `429 Too Many Requests` with `Retry-After` header.
- [ ] Add middleware unit tests for limit enforcement and recovery.

#### SH3: Request body size cap

- [ ] Wrap `r.Body` with `http.MaxBytesReader(w, r.Body, maxBytes)` in a middleware applied to all POST/PATCH routes.
- [ ] Default cap: `65536` bytes (64 KB); configurable via `MAX_BODY_BYTES` env var.
- [ ] Return `413 Request Entity Too Large` on exceeded limit.
- [ ] Add test covering oversized body rejection.

#### SH4: Request ID tracing

- [ ] Generate `X-Request-ID` (UUID v4) in middleware if not provided by caller; echo it in the response header.
- [ ] Thread request ID through all `slog` log entries for the request lifetime via `context`.
- [ ] Include request ID in error response bodies for client-side correlation.
- [ ] Add test verifying header round-trip and log field presence.

#### SH5: Agent-owned wallet / client-side signing

- [ ] Add `RPC_URL` and `PRIVATE_KEY` loading to the `escrow-cli` binary; build a per-command chain client for transaction-sending commands instead of routing through the server signer.
- [ ] Refactor all transaction-sending CLI commands (`fund`, `submit`, `approve`, `stake`, `dispute`, `resolve`, `quorum-vote`, `bid commit`, `bid reveal`, `bid accept`) to sign and broadcast locally, then POST off-chain metadata (submission URI, proof hash, etc.) to the server API.
- [ ] MCP tools: accept optional `signing_key` parameter on all transaction-sending tools; build an ephemeral per-call signer rather than using the server's persistent key.
- [ ] Document that the server `PRIVATE_KEY` is the operator/admin key only (emergency ops, indexer gas subsidy); participant operations must originate from a correctly-keyed CLI or MCP client.
- [x] Update `demo/demo-roles.md` and skill playbooks to reflect the single-server multi-participant topology.

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
- Cross-interface parity incidents (target: zero feature skew for features designated as multi-transport across MCP/HTTP/CLI; intentionally single-transport features are exempt per the case-by-case transport compatibility principle).

## Key Risks

- V3 closeout delay if UCP mapping overreaches into protocol redesign.
- Human-participant safeguards trail technical feature velocity.
- Safety controls becoming cost-prohibitive without governance floor enforcement.
- Reputation and monitoring signals being overfit or gamed without periodic calibration.
- Privacy expectations outpacing current on-chain transparency model.
- **Emergency endpoints have no HTTP-layer auth today**: any caller who can reach the server can freeze addresses or force-resolve escrows on-chain. SH1 is a hard gate before public exposure or V4 work.
