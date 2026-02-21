# Contract Specification

## 1) Purpose

This document specifies the **design intent** behind the escrow contracts: the state machine, settlement math, role semantics, and invariants. It exists so that someone reading the paper can understand *why* the contracts work the way they do without reading Solidity.

It does **not** duplicate information that lives authoritatively elsewhere:
- Solidity interfaces, struct definitions, events, and error types → the contracts themselves (`src/`)
- Off-chain storage schema, API surface, MCP tools → `docs/ARCHITECTURE.md` and the Go code
- Deployment procedures → `docs/SETUP.md`
- Testing requirements → the test files (`test/`)

## 2) Paper Traceability

Implements ["Intelligent AI Delegation"](https://arxiv.org/abs/2602.11865) (Tomašev, Franklin, Osindero -- Google DeepMind, 2026). V1 implements the settlement kernel: financial accountability and bounded authority. V2 adds market primitives. Adaptive delegation intelligence is deferred to subsequent phases.

### Requirement Mapping

| Paper Concept | Section | Implementation |
|---|---|---|
| Transfer of authority, responsibility, accountability | §2.1 | Immutable per-escrow roles (`buyer`, `worker`, `verifier`, `arbitrator`) with signed on-chain transitions |
| Task constraints and boundaries | §2.2 | Deadlines, review/dispute windows, strict state-machine transitions |
| Verifiability axis | §2.2(h) | On-chain submission hash commitments; explicit verifier approval path |
| Reversibility axis | §2.2(i) | Refund and split-settlement outcomes for failed/disputed tasks |
| Dynamic cognitive friction | §2.3 | `rejectByVerifier` and `escalateSilence` force explicit decisions rather than passive acceptance |
| Principal-agent alignment | §2.3 | Escrow links payment to verified outcomes, making misalignment financially costly |
| Transaction cost economics | §2.3 | Protocol fee snapshotting; complexity floor (V2) ensures delegation overhead doesn't exceed task value |
| Monitoring requirements | §4.5 | Canonical events for every state transition; off-chain indexer as machine-readable oversight surface |
| Trust calibration | §4.6 | Designated verifier/arbitrator identities; financial outcomes auditable on-chain |
| Adaptive coordination | §4.4 | Milestone-based escrow with intermediate checkpoints, partial payouts, and abort-on-failure; arbitrator timeout prevents permanent fund lock |
| Smart contract as settlement | §4.2 | Escrow holds funds; verification clause gates release |
| Verifiable task completion | §4.8 | Hash commitments transform provisional output into settled fact; verification gates payout |
| Delegatee stake / Sybil resistance | §4.8 | Worker deposits anti-Sybil bond before submission; stake returned on success, forfeited proportionally on failure |
| Partial compensation | §6.1 | Per-milestone payouts enable compensation proportional to verified completion |
| Privilege attenuation | §4.7 | Role-gated actions enforce least privilege per escrow |

### Explicitly Deferred

- Dynamic capability lookup/matching (§4.1-4.2)
- Adaptive multi-agent delegation policies (§4.4 advanced)
- Distributed reputation markets as primary trust substrate (§4.6)
- Delegation Capability Tokens / Macaroons (§4.7)
- ZK verification slots (§4.8)
- Hybrid human/AI oversight optimization at scale (§5)

## 3) Roles

| Role | Responsibility |
|---|---|
| `buyer` | Funds escrow, approves or disputes submissions, receives refund on failure |
| `worker` | Submits delivery, deposits stake (if required), receives payout on approval |
| `verifier` | Checks submission quality; can approve or reject within review window |
| `arbitrator` | Final authority in disputed cases; resolves with proportional split |
| `treasury` | Receives protocol fee from successful payouts |

Role assignment is immutable per escrow. This is a V1 constraint reflecting the paper's high-control fixed-role approach (§4.6) before open market matching.

## 4) State Machine

### Single-Shot Escrow

```text
Created ──fund()──> Funded ──submit()──> Submitted
  │                   │                    │  │  │
  │                   │                    │  │  └─ approve ──> Approved ──> Settled
  │                   │                    │  │
  │                   │                    │  └─ dispute/reject/escalate ──> Disputed
  │                   │                    │                                    │
  │                   │                    │                              resolveDispute
  │                   │                    │                                    │
  │                   │                    │                                    v
  │                   │                    │                                 Resolved ──> Settled
  │                   │                    │
  cancelBeforeFunding │                    └─ (timeout path via Funded)
  │                   │
  v                   └─ claimTimeoutRefund ──> Refunded
Cancelled                                        ^
                                                 │
                              claimArbitratorTimeout (from Disputed)
```

Nine states: Created, Funded, Submitted, Approved, Disputed, Resolved, Settled, Refunded, Cancelled.

Terminal states (Settled, Refunded, Cancelled) are mutually exclusive and irreversible.

### Transition Rules

| Transition | Who | From State | Guard |
|---|---|---|---|
| `fund` | buyer | Created | Exact amount (ETH or ERC20) |
| `depositStake` | worker | Funded | `workerStake > 0`, not already deposited |
| `cancelBeforeFunding` | buyer | Created | -- |
| `submit` | worker | Funded | Before deadline; stake deposited if required |
| `approveByBuyer` | buyer | Submitted | Within review window |
| `approveByVerifier` | verifier | Submitted | Within review window |
| `rejectByVerifier` | verifier | Submitted | Within review window |
| `dispute` | buyer | Submitted | Within review + dispute window |
| `escalateSilence` | worker | Submitted | After review window lapse without action |
| `resolveDispute` | arbitrator | Disputed | `workerAwardBps` in [0, 10000] |
| `claimTimeoutRefund` | buyer | Funded | Past submission deadline |
| `claimArbitratorTimeout` | buyer | Disputed | Past `disputedAt + arbitratorTimeoutSeconds` |

Invalid transitions revert with custom errors.

### Milestone-Based Escrow (V2)

Milestones transform the single-shot escrow into a staged contract with intermediate verification checkpoints and partial payouts. This implements the paper's adaptive coordination (§4.4): "delegation agreements encoded as smart contracts may also contain pre-agreed executable clauses for adaptive coordination."

Escrow-level lifecycle: `Created → Funded → [milestone cycling] → Settled or Refunded`.

Each milestone progresses independently through: `Pending → Submitted → Approved/Disputed → Resolved`. The buyer can abort remaining milestones after a terminal failure on any milestone.

Key properties:
- Milestones are defined at creation and are immutable. Maximum 16.
- Processed in strict order (`milestoneIndex == currentMilestone`).
- Each has its own amount, submission deadline, and review cycle.
- Approved milestones pay out immediately (partial settlement).
- Disputed milestones follow the same arbitrator resolution flow as single-shot.
- Worker stake is held for the full escrow duration and settled once at the end.
- Single-milestone escrows behave identically to V1 (backward compatibility).

## 5) Settlement Math

### Approval (Single-Shot)

```
grossWorker  = amount
fee          = grossWorker * protocolFeeBpsSnapshot / 10000
workerNet    = grossWorker - fee
stakeReturn  = workerStaked ? workerStake : 0
```

Transfers: `workerNet + stakeReturn` → worker, `fee` → treasury.

### Dispute Resolution (Single-Shot)

```
workerGross    = amount * workerAwardBps / 10000
buyerRefund    = amount - workerGross
fee            = workerGross * protocolFeeBpsSnapshot / 10000
workerNet      = workerGross - fee
stakeReturn    = workerStaked ? workerStake * workerAwardBps / 10000 : 0
stakeForfeited = workerStaked ? workerStake - stakeReturn : 0
```

Transfers: `workerNet + stakeReturn` → worker, `buyerRefund + stakeForfeited` → buyer, `fee` → treasury.

`workerAwardBps` semantics: 0 = full refund to buyer, 10000 = full payout to worker, intermediate = proportional split.

### Timeout Refund

Applies when the worker doesn't submit by the deadline, or when the arbitrator doesn't resolve within the timeout period.

```
stakeForfeited = workerStaked ? workerStake : 0
```

Transfers: `amount + stakeForfeited` → buyer.

### Milestone Approval

Per-milestone, same formula as single-shot approval but using `milestone.amount` instead of `amount`. No stake movement -- stake is settled at escrow end.

### Milestone Dispute Resolution

Per-milestone, same formula as single-shot dispute resolution but using `milestone.amount`. No stake movement per-milestone.

### Milestone Worker Stake Settlement

Settled once when all milestones reach terminal states:

```
workerAwarded = Σ (Approved milestones: milestone.amount)
              + Σ (Resolved milestones: milestone.amount * milestone.awardBps / 10000)
total         = Σ all milestone amounts
stakeReturn   = workerStake * workerAwarded / total    (if workerAwarded > 0 and < total)
              = workerStake                            (if workerAwarded == total)
              = 0                                      (if workerAwarded == 0)
stakeForfeited = workerStake - stakeReturn
```

Cancelled/aborted milestones contribute to `total` but not `workerAwarded`, so their weight dilutes the stake return proportionally.

### Abort Refund

Sum of all `Pending` milestone amounts returned to buyer. Those milestones are set to `Cancelled`.

### Rounding

All divisions floor toward zero (Solidity default). Remainders stay with the buyer path due to subtraction order.

## 6) Invariants

These must hold for every escrow at all times:

1. **Balance conservation**: escrow balance is either `0` (after terminal settlement/refund) or `amount` (or `amount + workerStake` when stake deposited) during active states.
2. **Terminal exclusivity**: Settled, Refunded, and Cancelled are mutually exclusive. No function can transition from a terminal state to a non-terminal state.
3. **Fund conservation**: total funds distributed never exceeds `amount + workerStake`.
4. **Fee bound**: protocol fee never exceeds worker gross award.
5. **Stake lifecycle**: returned fully on approval, split proportionally on dispute, forfeited on timeout.
6. **Milestone ordering**: milestones are processed sequentially; `milestoneIndex` must equal `currentMilestone`.
7. **Milestone fund conservation**: sum of all milestone payouts + refunds + fees + remaining stake = `amount + workerStake`.

## 7) Economic Parameters

Global (factory-level):
- `protocolFeeBps` (0-10000) -- snapshotted at escrow creation to prevent governance race conditions mid-task.
- `treasury` address -- snapshotted at creation.

Per-escrow:
- `amount` -- total escrow amount (ETH or ERC20). For milestone escrows, equals sum of all milestone amounts.
- `workerStake` -- anti-Sybil bond (paper §4.8). `0` means no stake required.
- `submissionDeadline` -- unix timestamp (single-shot) or per-milestone deadlines.
- `reviewPeriodSeconds` -- window for approval/rejection after submission.
- `disputePeriodSeconds` -- window for buyer dispute after review period.
- `arbitratorTimeoutSeconds` -- maximum time for arbitrator to resolve before buyer can claim refund.

Default values: review = 86,400s (24h), dispute = 172,800s (48h).

Rationale (paper §4.3, §4.4): 24h/48h windows balance oversight with capital efficiency. Explicit reject avoids passive acceptance under ambiguity (criticality and accountability). Silence-escalation prevents indefinite lock under asymmetric power/inattention (monitoring and authority gradients).

## 8) Edge Cases

- **Late submission**: reverts if `block.timestamp > submissionDeadline`.
- **Approval/dispute race**: first confirmed transition wins; subsequent calls revert by status guard.
- **Buyer inactivity after submission**: verifier can still approve within review window. Worker can escalate silence to Disputed after review window lapse; arbitrator remains final payout authority.
- **Arbitrator inactivity**: buyer can claim full refund via `claimArbitratorTimeout()` after the configured timeout period.
- **ERC20 vs ETH**: `token == address(0)` means ETH-denominated. All settlement math is token-agnostic; the transfer mechanism differs.
- **Zero worker stake**: `depositStake()` reverts. Submit proceeds without stake check.
- **Abort eligibility**: `abortRemainingMilestones()` requires the current milestone to be in a terminal failure state (Resolved or Cancelled). Cannot abort while a milestone is actively in progress with time remaining.

## 9) Security Model

- `nonReentrant` on payable state-changing functions that transfer ETH.
- Checks-effects-interactions ordering throughout.
- Revert on failed ETH transfers.
- No unbounded loops (milestone count capped at 16).
- Custom errors instead of string reverts.
- Events emitted for all state transitions and administrative actions.
- Fee and treasury snapshotted at creation to prevent governance manipulation mid-lifecycle.
