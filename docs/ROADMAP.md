# Roadmap

Implementation roadmap for the ["Intelligent AI Delegation"](https://arxiv.org/abs/2602.11865) paper (Tomašev, Franklin, Osindero -- Google DeepMind, 2026).

**Current phase: Phase 2 (R2-R4 revisits) plus the Phase 2b hardening queue (H1-H15), after V3 closeout completion. SH1 is done; SH2-SH5 of the pre-V4 security gate must complete before V4 work starts. Items marked `blocked` wait on an owner decision.**

---

## How To Read This Roadmap

- **Repo entrypoint:** start with `AGENTS.md` for the repo harness and `docs/ARCHITECTURE.md` for stable system boundaries. This roadmap owns sequencing, not the full architecture narrative.
- **Canonical mapping file:** `docs/paper-feature-map.json` is the canonical, machine-readable source of truth for paper coverage, gap tracking, and per-item design decisions. The **Item Register** tables below are human-readable summaries that must be kept in sync with that JSON.
- **Status legend:** `done` = implemented, `partial` = implemented in part, with the paper-prescribed or claimed scope incomplete (gaps listed in the notes), `in_progress` = currently being delivered, `planned` = scoped but not implemented, `blocked` = scoped but waiting on an owner decision (the JSON map records these as `planned` with `blocked_on_owner: true`).
- **Status honesty:** a `done` row means the code does what the row says today. Where the code is thinner than the claim, the row is `partial` and the notes say why, with a `file:line` pointer where useful.
- **Alignment legend:**
  - `direct` = closely follows paper prescription.
  - `opinionated` = paper-aligned, but concrete mechanism is a project decision.
  - `gap_fill` = added after review because paper expectation was under-tracked.

---

## Snapshot (2026-11-17)

- **Built strongly:** settlement kernel, market primitives (RFQ, bids, sealed bidding), milestones, stake, tiers, emergency controls with admin auth (SH1), attestation-chain validation, checkpoint storage.
- **Built thinly (status corrected on 2026-11-17 after a code audit):**
  - **Principal authentication** (`13b`, P4.7): HTTP/CLI DCT mint, delegate, and revoke are always denied; MCP trusts caller strings. Only the SH1 admin-token routes authenticate.
  - **Adaptive coordination** (P4.4): backup activation and checkpoints exist; trigger detection and automatic re-allocation do not.
  - **Verifiable completion** (P4.8): ZK proof is not bound to the task, the quorum game rewards no one, attestation roots are not anchored on-chain.
  - **Protocol adapters:** A2A (`8`) never creates or links escrows, AP2 (`9`) verifies the mandate and the paired EIP-3009 signature before funding, and the x402 client has no production callers.
  - **Transitive liability** is not implemented; `parentEscrow` only drives the re-delegation fee surcharge.
- **V3 status:** items `12`-`22` shipped, with the corrections above applied to `13b`, `16`, `18`, and `19` notes.
- **Previously under-tracked paper requirements now explicit:** social intelligence (`29`) and user training (`30`).
- **Human delegatees:** supported in model and metadata (`delegate_preference`) but not yet policy-enforced routing.
- **Monitoring depth:** L0/L1 production; L2/L3 remains staged.
- **R1 status:** complete. Sealed bidding now applies non-reveal cooldowns, deterministic revealed-bid finalization, and consistent transport semantics.
- **SH1 status:** done (PR #55). HTTP emergency and DCT admin routes require a bearer token; HTTP and local MCP emergency access are separate opt-ins; CORS defaults to deny.
- **Hardening queue (Phase 2b):** `H1`-`H15` track the audit findings. `H1` (AP2 signature verification) is done. In flight: `H2` indexer correctness, `H3` Solidity test base and invariants, `H4` CreateEscrow unification. Owner-blocked: `H8`-`H15` and `SH5`.
- **Contract change budget:** `TaskEscrow` runtime is 23,992 B against the 24,576 B limit (about 584 B headroom), so contract fixes ship as one bundled v2 redeploy rather than piecemeal.
- **Opinionated implementations worth revisiting now:** items `15` (anchoring without Solidity), `20` (guardrails only), and `21` (after `H8`).

Recommended near-term order (completed R1, SH1, and H1 omitted): `H2 -> H3 -> H4 -> SH2 -> SH3 -> SH4 -> H5 -> H6 -> H7 -> R2 -> R3`, then the owner-blocked group `H8 -> SH5 -> H9 .. H15`, then `R4 -> 25 -> 29 -> 30 -> 23 -> 24 -> 26 -> 28 -> 27`. `H2`-`H4` are already in flight, so the first unstarted item is `SH2`-`SH4` (one HTTP-hardening change).

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
| 4 | Backup agent clause | done | 4.4 | opinionated | Pre-designated backup worker with explicit activation, not automatic market rematching. Known defects (stake scoped over all milestones, only the current milestone deadline extended): `H7`. |
| 5 | On-chain reputation seed | done | 4.6 | opinionated | Started with immutable counters/events before richer trust portfolio models. Counters are ungated: `createEscrow` has no `msg.sender` check (`H10`). |
| 6 | Complexity floor parameter | done | 4.3 | opinionated | Single configurable floor enforced on-chain/off-chain instead of dynamic per-task optimizer. The floor is one raw integer compared across tokens, so it is unit-unsafe for ETH vs USDC (`H11`). |
| 7 | Task_RFQ + Bid_Object bidding protocol | done | 4.2, 6.1 | direct | Off-chain negotiation with on-chain formalization only when bid is accepted. |
| 8 | A2A settlement adapter | partial | 6, 6.1 | direct | Added escrow-specific task metadata while keeping A2A transport compatibility. Tasks are recorded only: `escrow_trigger` is stored but never acted on, the adapter never creates or links an escrow, and `UpdateA2ATaskEscrow` has no callers. |
| 9 | AP2 mandate-to-escrow bridge | partial | 6, 6.1 | direct | AP2 mandates are EIP-712 signed and bound to an escrow, and the paired EIP-3009 `ReceiveWithAuthorization` signature is checked against the token `DOMAIN_SEPARATOR()` before funding via `fundWithAuthorization()` (`H1`). Funding does not route through x402, and the x402 client has no production callers. |
| 10 | Real-time event subscriptions | done | 4.5, 6.1 | opinionated | Delivered L0/L1 first; deferred L2/L3 for staged rollout and privacy/control concerns. |
| 11 | Emergency response protocol | done | 4.9 | opinionated | Owner-governed freeze/unfreeze/emergency resolve as initial incident-response baseline. |

### V3 -- Delegation Intelligence

| ID | Item | Status | Paper refs | Alignment | Design decision vs paper |
|---|---|---|---|---|---|
| 12 | Sealed bidding | done | 4.5, 6.1 | opinionated | Commit-reveal chosen as deployable first privacy layer before selective-disclosure systems. |
| 13 | Delegation Capability Tokens (DCTs) | done | 4.7, 6.1 | direct | Attenuated capabilities implemented off-chain with strict scope and lifecycle invalidation. No escrow action requires a DCT; tokens are a standalone capability ledger (DB-backed bearer secrets). |
| 13a | Canonical token profile hardening | done | 4.7, 6.1 | opinionated | Hard-cutoff to a single canonical profile (`dct-profile-v1`) to avoid legacy ambiguity. |
| 13b | Principal authorization layer for DCT ops | partial | 4.7, 4.9 | direct | Default-deny authz with audited decisions, but no principal authentication outside the SH1 admin token. HTTP/CLI mint, delegate, and revoke are always denied (`Authenticated` is never set); MCP marks any non-empty caller string as authenticated. Fix is `H8` (blocked on owner). |
| 14 | Verifiable credentials for bidding | done | 4.6, 6.1 | opinionated | secp256k1 attestation profile now, with optional `issuer_did` for forward DID compatibility. |
| 15 | Attestation chains | done | 4.8 | opinionated | Recursive attestation verification is off-chain to avoid contract size and gas overhead. |
| 16 | Checkpoint/resume | done | 4.4, 6.1 | opinionated | Checkpoint artifacts stored off-chain. The active-worker commit rule is a string compare on the caller-supplied `committed_by`, so it is not authenticated until `H8`. |
| 17 | Tiered service levels | done | 5.3 | direct | Tier and fee snapshots are immutable per escrow to prevent governance races. In Tier 1 the buyer can still `dispute()` mid-review and wipe quorum votes (`H13`). |
| 18 | ZK verification slot | done | 4.8 | opinionated | Optional ZK verifier path, not mandatory ZK in all settlement flows. The proof is not bound to `taskSpecHash`, `submissionHash`, or the escrow, and counts as one approval vote (`H12`). |
| 19 | Multi-verifier quorum | done | 4.8 | opinionated | Bounded verifier panel and stake game as practical consensus mechanism. The game rewards no majority voter and refunds abstainers in full, so abstaining weakly dominates voting when stake > 0 (`H13`). |
| 20 | Market stability mechanisms | done | 4.4 | opinionated | Concrete policy knobs: cooldowns, surcharge windows, damped overlays. |
| 21 | Contract-first decomposition tooling | done | 4.1 | opinionated | Human/AI preference and market depth signals are advisory in V3 (not hard gates). |
| 22 | UCP fulfillment provider | done | 6 | direct | UCP adapter implemented as an interoperability envelope over shared escrow orchestration; escrow remains settlement source of truth. |

### Pre-V4 Security Gate (must ship before any V4 work)

| ID | Item | Status | Alignment | Notes |
|---|---|---|---|---|
| SH1 | Admin auth for emergency endpoints | done | gap_fill | HTTP emergency routes and DCT admin routes require a bearer token. HTTP and local MCP emergency access are independently opt-in, and HTTP CORS defaults to deny. |
| SH2 | Per-IP rate limiting on POST endpoints | planned (queued next, with SH3 and SH4) | gap_fill | Token-bucket rate limiter on all POST routes. Prevents gas drain via transaction spam and RPC quota exhaustion. No ceiling currently exists. |
| SH3 | Request body size cap | planned | gap_fill | `http.MaxBytesReader` before decode on all handlers. Prevents memory exhaustion from unbounded body reads. |
| SH4 | Request ID tracing | planned | gap_fill | `X-Request-ID` header echoed in responses and included in all structured log entries. Required for operational traceability before real traffic hits. |
| SH5 | Agent-owned wallet / client-side signing | blocked | gap_fill | **Blocked on the owner's off-chain trust-model decision.** CLI and local per-agent MCP processes sign and broadcast transactions locally using the caller's `PRIVATE_KEY` + `RPC_URL`; MCP must not accept `signing_key` as a tool argument (keys would land in transcripts and logs). Server becomes a pure coordination and indexing layer; its `PRIVATE_KEY` is for operator/admin operations only (emergency, indexer gas). Blocks real multi-agent demos and any production deployment where buyer, worker, and verifier are different principals. |

### Phase 2b -- Hardening and Truth Pass (added 2026-11-17)

Findings from an independent code audit. Contract items `H7` and `H9`-`H13` ship together as one bundled v2 redeploy, which must keep `TaskEscrow` under 24,576 B.

| ID | Item | Status | Alignment | Notes |
|---|---|---|---|---|
| H1 | AP2 mandate signature verification | done | gap_fill | EIP-712 mandate signatures recover to `signer_address` and `authorization.from`. The signed authorization includes the token, must equal the escrow amount and token, and its EIP-3009 signature is checked against the token `DOMAIN_SEPARATOR()` before any row write or relay. |
| H2 | Indexer correctness | in_progress | gap_fill | Single-flight `RunOnce`, transactional cursor, confirmation depth. Today logs are marked processed before the handler succeeds and concurrent runs can move the cursor backwards. |
| H3 | Solidity test base and expanded invariants | in_progress | gap_fill | Shared test base and mocks plus invariants across milestones, verifier stakes, ERC20, backup, and emergency. Today's invariant suite covers only single-shot ETH. Must land before `H7`. |
| H4 | CreateEscrow request unification | in_progress | gap_fill | One shared request DTO and parser replaces three duplicated copies (HTTP, MCP, UCP); floor and panel validation move into the service. Prerequisite for `H5` and `H6`. |
| H5 | Fund-recovery parity (MCP, HTTP, CLI) | planned | gap_fill | Expose timeout refund, arbitrator-timeout claim, cancel-before-funding, milestone timeouts, and `expireNoQuorum` / `expireMilestoneNoQuorum` on all three transports. Today they are reachable only via UCP, and the no-quorum expiries have no Go binding. After `H4`. |
| H6 | Generic action adapters and service extraction | planned | gap_fill | Shared HTTP and MCP action helpers; move checkpoint, RFQ, and decomposition-finalize logic into services; unify escrow ID resolution. After `H5`. |
| H7 | Contract bug-fix bundle | planned | gap_fill | No owner decision needed. Scope backup-worker stake to post-activation milestones (`TaskEscrow.sol:1095`); extend backup deadlines for all remaining milestones from `max(now, deadline)` with checked casts and extension > 0; reject 1-element milestone arrays and zero-amount milestones; minimum review, dispute, and arbitrator windows; `nonReentrant` on `withdrawStake`; checks-effects-interactions in `activateBackup`; `onlyFactory` on `EscrowDeployer.deploy`. After `H3`. |
| H8 | Principal authentication (signed requests) | blocked | gap_fill | Blocked on the owner's trust-model decision (trustless participants vs operator-trusted coordinator). Replaces body-supplied `caller`, `committed_by`, and bidder strings with signed requests. Resolves `13b`. |
| H9 | Pull-payment settlement | blocked | gap_fill | Route payouts, refunds, stake settlement, and fees through `withdrawable` so a reverting or blacklisted recipient cannot brick split dispute or quorum settlement. Changes SPEC invariant 1. Contract v2. |
| H10 | createEscrow consent model | blocked | gap_fill | Decide who must consent at creation before an escrow counts toward reputation or the surcharge streak. Contract v2. |
| H11 | Per-token complexity floor | blocked | gap_fill | Key the floor per token, or move it off-chain in USD terms. |
| H12 | ZK public-input binding | blocked | gap_fill | Bind verifier public inputs to escrow, milestone, `taskSpecHash`, `submissionHash`, and worker, or document the slot as panel-gated evidence only. |
| H13 | Quorum incentive design | blocked | gap_fill | Decide whether majority verifiers are paid and whether Tier-1 buyers may `dispute()` mid-quorum. |
| H14 | SQLite migration squash | blocked | gap_fill | Squash 28 migrations into one baseline; forces a re-index of existing testnet databases. |
| H15 | Remove dead `owner` request-body field | blocked | gap_fill | Hard cutoff for body fields as authority, sequenced with `H8`. |

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

- [x] Add anti-grief controls for commit-without-reveal behavior.
- [x] Define deterministic fallback rules when top commits are not revealed.
- [x] Ensure sealed-bid commit/reveal semantics are consistent across transports that surface sealed-bidding operations.

#### R2 (item 15): Attestation-chain anchoring

- [ ] Anchor the attestation root without Solidity changes: commit it inside `submissionHash` (`keccak256(abi.encode(deliverableHash, attestationRoot))`) and verify off-chain against the `SubmissionMade` event and the stored chain. A native on-chain root field joins the contract v2 bundle only if needed (headroom is about 584 B).
- [ ] Add proof-of-inclusion retrieval and verification helpers.
- [ ] Define dispute-time checks when submitted chain data mismatches anchored roots.

#### R3 (item 20): Market stability parameter recalibration

- [ ] Add guardrails for parameter ranges by task class/risk profile.
- [ ] Add an auditable change log for parameter updates.
- [ ] Deferred until real traffic data exists: replay/simulation harness and a staged governance rollout path.

#### R4 (item 21): Human delegate preference enforcement modes

Follows `H8`: enforcement is not meaningful while bidder identity and "human-ness" are self-asserted.

- [ ] Introduce policy modes (`advisory`, `require_human`, `require_ai`, `any`) scoped by risk/task class.
- [ ] Enforce policy mode during decomposition finalize and bid acceptance paths.
- [ ] Record explicit override reason/audit events for exception handling.

### Pre-V4 Security Gate

All five items below are **blocking gates**: V4 work does not start until all are done. SH1 is done (PR #55); SH5 is blocked on an owner decision.

#### SH1: Admin auth for emergency endpoints

- [x] Disable HTTP emergency access by default and require an `ADMIN_TOKEN` of at least 32 characters to enable it.
- [x] Require a bearer token for HTTP emergency routes, DCT emergency override, and DCT audit listing. Return `401 Unauthorized` for missing or invalid credentials.
- [x] Require separate opt-in for local MCP emergency tools and DCT audit. Read the CLI credential from `ESCROW_ADMIN_TOKEN`.
- [x] Deny cross-origin HTTP and WebSocket access by default.
- [x] Verify the defaults and credential checks in tests and document setup in `docs/SETUP.md`.

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

Blocked on the owner's off-chain trust-model decision. Coupled with `H8` (principal authentication) and `H10` (creation consent).

- [ ] Add `RPC_URL` and `PRIVATE_KEY` loading to the `escrow-cli` binary; build a per-command chain client for transaction-sending commands instead of routing through the server signer.
- [ ] Refactor all transaction-sending CLI commands (`fund`, `submit`, `approve`, `stake`, `dispute`, `resolve`, `quorum-vote`, `bid commit`, `bid reveal`, `bid accept`) to sign and broadcast locally, then POST off-chain metadata (submission URI, proof hash, etc.) to the server API.
- [ ] MCP: run a local per-agent MCP process (stdio) that signs with its own `PRIVATE_KEY` from the environment. Do **not** add a `signing_key` tool argument: keys would end up in transcripts, logs, and model context.
- [ ] Document that the server `PRIVATE_KEY` is the operator/admin key only (emergency ops, indexer gas subsidy); participant operations must originate from a correctly-keyed CLI or MCP client.
- [x] Update `demo/demo-roles.md` and skill playbooks to reflect the single-server multi-participant topology.

### Phase 2b: Hardening and Truth Pass

#### H1: AP2 mandate signature verification

- [x] Verify the mandate signature over the mandate payload and require the recovered signer to match `signer_address` and `authorization.from`.
- [x] Reject unsigned or mismatched mandates in `ValidateMandate`, covering the HTTP, MCP, and CLI paths.
- [x] Tests with a real signed mandate and a tampered one. The signed authorization includes the token, must match the escrow amount and token exactly, and the EIP-3009 signature is verified against the token `DOMAIN_SEPARATOR()` before a row is written.

#### H2: Indexer correctness

- [ ] Single-flight `RunOnce` (mutex) and a non-blocking "nudge" in request paths.
- [ ] Advance the cursor to the minimum successful block, in the same transaction as the log handler.
- [ ] Add a confirmation depth; add a race test.

#### H3: Solidity test base and expanded invariants

- [ ] Shared `BaseEscrowTest` and mocks; remove per-file mock ERC20 copies.
- [ ] Invariants for balance conservation, milestone conservation, verifier-stake single settlement, ERC20, backup, and emergency paths.

#### H4: CreateEscrow request unification

- [ ] One request DTO and parser shared by HTTP, MCP, and UCP; floor and panel validation live in the service once.
- [ ] Parity test feeding identical payloads through each transport.

#### H5: Fund-recovery parity

- [ ] Go bindings for `expireNoQuorum` and `expireMilestoneNoQuorum`.
- [ ] MCP tools, HTTP routes, and CLI commands for timeout refund, arbitrator-timeout claim, cancel-before-funding, and milestone timeouts.

#### H6: Generic action adapters and service extraction

- [ ] Shared action helpers for HTTP and MCP; remove repeated ID parsing and JSON decode.
- [ ] Move checkpoint, RFQ create, and decomposition finalize into services; unify ID resolution across transports.

#### H7: Contract bug-fix bundle

- [ ] Backup stake scoping, backup deadline fixes, milestone length and zero-amount checks, minimum windows.
- [ ] `nonReentrant` on `withdrawStake`, CEI in `activateBackup`, `onlyFactory` on the deployer.
- [ ] Contract size stays under 24,576 B (`make sizes`); tests added for each fix.

#### H8-H15: Owner-blocked

- [ ] `H8` principal authentication, `H9` pull payments, `H10` creation consent, `H11` per-token floor, `H12` ZK binding, `H13` quorum incentives, `H14` migration squash, `H15` dead `owner` field. Each waits on an owner decision; see the Phase 2b table for what is being decided.

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
- **Emergency credentials protect privileged operations.** Keep the HTTP bearer token and the local MCP stdio session limited to trusted operators.
- **Off-chain identity is not authenticated.** Until `H8`, caller, committer, and bidder identities are strings the requester supplies; treat the server as an operator-trusted coordinator and do not expose participant endpoints to untrusted callers.
- **Contract headroom is thin.** `TaskEscrow` has about 584 B left under EIP-170; every contract change competes for that budget, so batch them into one v2 redeploy.
- **Settlement can be blocked by a hostile recipient.** Push payouts let a reverting contract or blacklisted USDC address block split and quorum settlement until `H9` lands.
