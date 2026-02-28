# Contract Specification

## 1) Purpose

This document specifies the **design intent** behind the escrow contracts: the state machine, settlement math, role semantics, and invariants. It exists so that someone reading the paper can understand *why* the contracts work the way they do without reading Solidity.

It does **not** duplicate information that lives authoritatively elsewhere:
- Solidity interfaces, struct definitions, events, and error types → the contracts themselves (`src/`)
- Off-chain storage schema, API surface, MCP tools → `docs/ARCHITECTURE.md` and the Go code
- Deployment procedures → `docs/SETUP.md`
- Testing requirements → the test files (`test/`)

Current off-chain integration surfaces (Skills + `escrow-cli`, MCP, HTTP API) do not change this contract-level specification. They are delivery interfaces for the same on-chain state machine and settlement math.

## 2) Paper Traceability

Implements ["Intelligent AI Delegation"](https://arxiv.org/abs/2602.11865) (Tomašev, Franklin, Osindero -- Google DeepMind, 2026). V1 implements the settlement kernel: financial accountability and bounded authority. V2 adds market primitives. Adaptive delegation intelligence is deferred to subsequent phases.

### Requirement Mapping

| Paper Concept | Section | Implementation |
|---|---|---|
| Transfer of authority, responsibility, accountability | §2.1 | Immutable per-escrow roles (`buyer`, `worker`, `verifierPanel[]`, `arbitrator`) with signed on-chain transitions |
| Task constraints and boundaries | §2.2 | Deadlines, review/dispute windows, strict state-machine transitions |
| Verifiability axis | §2.2(h) | On-chain submission + proof-hash commitments; quorum-based verifier voting including optional on-chain proof verification votes |
| Reversibility axis | §2.2(i) | Refund and split-settlement outcomes for failed/disputed tasks |
| Dynamic cognitive friction | §2.3 | Quorum verifier votes (`castVerifierVote`) and `escalateSilence` force explicit decisions rather than passive acceptance |
| Principal-agent alignment | §2.3 | Escrow links payment to verified outcomes, making misalignment financially costly |
| Transaction cost economics | §2.3 | Protocol fee snapshotting; complexity floor (V2) ensures delegation overhead doesn't exceed task value |
| Monitoring requirements | §4.5 | Canonical events for every state transition; off-chain indexer as machine-readable oversight surface |
| Trust calibration | §4.6 | Designated verifier panel + quorum/arbitrator identities; financial outcomes auditable on-chain |
| Adaptive coordination | §4.4 | Milestone-based escrow with intermediate checkpoints, partial payouts, and abort-on-failure; arbitrator timeout prevents permanent fund lock |
| Smart contract as settlement | §4.2 | Escrow holds funds; verification clause gates release |
| Verifiable task completion | §4.8 | Submission/proof hash commitments transform provisional output into settled fact; optional on-chain proof verification gates payout |
| Delegatee stake / Sybil resistance | §4.8 | Worker deposits anti-Sybil bond before submission; stake returned on success, forfeited proportionally on failure |
| Partial compensation | §6.1 | Per-milestone payouts enable compensation proportional to verified completion |
| Backup agent re-allocation | §4.4 | Pre-designated fallback worker activated by buyer on primary default; deadline extension and stake forfeiture |
| Privilege attenuation | §4.7 | Role-gated actions enforce least privilege per escrow |
| Emergency response | §4.9 | Credential revocation (freeze addresses), contract freeze, emergency resolve |

### Explicitly Deferred

- Dynamic capability lookup/matching (§4.1-4.2)
- Adaptive multi-agent delegation policies (§4.4 advanced)
- Distributed reputation markets as primary trust substrate (§4.6)
- Hybrid human/AI oversight optimization at scale (§5)

## 3) Roles

| Role | Responsibility |
|---|---|
| `buyer` | Funds escrow, approves or disputes submissions, receives refund on failure |
| `worker` | Submits delivery, deposits stake (if required), receives payout on approval |
| `verifierPanel[]` | Quorum verifiers check submission quality and cast approve/reject votes; quorum finalizes approval/dispute |
| `arbitrator` | Final authority in disputed cases; resolves with proportional split |
| `treasury` | Receives protocol fee from successful payouts |
| `backupWorker` | Optional pre-designated fallback worker; activated by buyer if primary defaults (paper §4.4) |

Role assignment is immutable per escrow (including `backupWorker`). This is a V1 constraint reflecting the paper's high-control fixed-role approach (§4.6) before open market matching.

## 4) State Machine

### Single-Shot Escrow

```text
Created ──fund() / fundWithAuthorization()──> Funded ──submit()──> Submitted
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
| `fundWithAuthorization` | buyer (via any caller) | Created | ERC20 only; EIP-3009 signed authorization; `from` must be buyer; balance-delta guard for fee-on-transfer |
| `depositStake` | worker | Funded | `workerStake > 0`, not already deposited |
| `cancelBeforeFunding` | buyer | Created | -- |
| `submit` | worker | Funded | Before deadline; stake deposited if required |
| `approveByBuyer` | buyer | Submitted | Within review window |
| `depositVerifierStake` | verifier panel member | Funded/Submitted | `verifierStakePerVerifier > 0`, not already deposited |
| `castVerifierVote(approve, reasonURI)` | verifier panel member | Submitted | Within review window; one vote per verifier; `verifierStakePerVerifier == 0` or verifier has deposited stake; quorum threshold or reject-threshold finalizes |
| `verifyAndApprove` | verifier panel member | Submitted | `zkVerifier != 0`, `proofHash != 0`, `keccak256(proof) == proofHash`, verifier contract returns `true`; `verifierStakePerVerifier == 0` or verifier has deposited stake |
| `dispute` | buyer | Submitted | Within review + dispute window |
| `escalateSilence` | worker | Submitted | After review window lapse without action |
| `expireNoQuorum` | buyer / worker / verifier panel member | Submitted | After review + dispute window; transitions stale no-quorum cycle to `Disputed` |
| `resolveDispute` | arbitrator | Disputed | `workerAwardBps` in [0, 10000] |
| `claimTimeoutRefund` | buyer | Funded | Past submission deadline |
| `claimArbitratorTimeout` | buyer | Disputed | Past `disputedAt + arbitratorTimeoutSeconds` |
| `activateBackup` | buyer | Funded | `backupWorker != address(0)`, not already activated |

Invalid transitions revert with custom errors.

### EIP-3009 Funding Path

For ERC20-denominated escrows, the buyer may fund via `fundWithAuthorization()` instead of `fund()`. This uses EIP-3009 `receiveWithAuthorization`: the buyer signs an authorization off-chain; any caller (e.g., a relayer or x402 facilitator) submits the signed payload on-chain. The escrow contract pulls tokens via the token's `receiveWithAuthorization` entrypoint. The `from` address in the authorization must equal the escrow's `buyer`. A balance-delta guard protects against fee-on-transfer tokens. This enables gasless funding flows where the facilitator sponsors gas on behalf of the buyer.

### Backup Agent Clause

The buyer may designate a `backupWorker` at escrow creation (paper §4.4: backup agent auto-re-allocation on failed ZK checkpoint). If the primary worker defaults, the buyer calls `activateBackup()` to:

1. Replace `activeWorker` with `backupWorker`.
2. Extend the submission deadline by `backupDeadlineExtension` seconds.
3. Forfeit any deposited worker stake to the buyer.
4. Emit `BackupActivated(previousWorker, newWorker, newDeadline)`.

The backup worker then proceeds with the normal submit/approve/dispute lifecycle. Backup activation can only occur once per escrow, and only from the `Funded` state. If no backup worker is designated (`backupWorker == address(0)`), the function reverts.

### Milestone-Based Escrow (V2)

Milestones transform the single-shot escrow into a staged contract with intermediate verification checkpoints and partial payouts. This implements the paper's adaptive coordination (§4.4): "delegation agreements encoded as smart contracts may also contain pre-agreed executable clauses for adaptive coordination."

Escrow-level lifecycle: `Created → Funded → [milestone cycling] → Settled or Refunded`.

Each milestone progresses independently through: `Pending → Submitted → Approved/Disputed → Resolved`. The buyer can abort remaining milestones after a terminal failure on any milestone.

Key properties:
- Milestones are defined at creation and are immutable. Maximum 16.
- Processed in strict order (`milestoneIndex == currentMilestone`).
- Each has its own amount, submission deadline, and review cycle.
- Each submission can include an optional `proofHash` commitment for external ZK artifacts.
- Approved milestones pay out immediately (partial settlement).
- Disputed milestones follow the same arbitrator resolution flow as single-shot.
- Worker stake is held for the full escrow duration and settled once at the end.
- Single-milestone escrows behave identically to V1 (backward compatibility).

### ZK Verification Slot

For formally verifiable tasks (paper §4.8), each submission path supports a `proofHash` commitment:

- Single-shot: `submit(submissionHash, submissionURI, proofHash)`
- Milestone: `submitMilestone(milestoneIndex, submissionHash, submissionURI, proofHash)`

Optional escrow-level verifier wiring at creation (`zkVerifier`, `circuitId`) enables strict on-chain verification:

- `verifyAndApprove(proof)` checks `keccak256(proof) == proofHash` then calls `zkVerifier.verifyProof(circuitId, proof)`.
- `verifyAndApproveMilestone(milestoneIndex, proof)` performs the same checks against milestone-local `proofHash`.

When verifier quorum is configured, `verifyAndApprove` / `verifyAndApproveMilestone` contribute one approval vote toward quorum (rather than bypassing quorum), while `proofHash` remains an immutable on-chain commitment for off-chain proof validation and dispute evidence.

## 5) Settlement Math

### Approval (Single-Shot)

```text
grossWorker  = amount
fee          = grossWorker * protocolFeeBpsSnapshot / 10000
workerNet    = grossWorker - fee
stakeReturn  = workerStaked ? workerStake : 0
```

Transfers: `workerNet + stakeReturn` → worker, `fee` → treasury.

### Dispute Resolution (Single-Shot)

```text
workerGross    = amount * workerAwardBps / 10000
buyerRefund    = amount - workerGross
fee            = workerGross * protocolFeeBpsSnapshot / 10000
workerNet      = workerGross - fee
stakeReturn    = workerStaked ? workerStake * workerAwardBps / 10000 : 0
stakeForfeited = workerStaked ? workerStake - stakeReturn : 0
```

Transfers: `workerNet + stakeReturn` → worker, `buyerRefund + stakeForfeited` → buyer, `fee` → treasury.

`workerAwardBps` semantics: 0 = full refund to buyer, 10000 = full payout to worker, intermediate = proportional split.

### Backup Activation

When `activateBackup()` is called and the original worker has deposited a stake:

```text
stakeForfeited = workerStaked ? workerStake : 0
```

Transfers: `stakeForfeited` → buyer. The backup worker must deposit their own stake (if `workerStake > 0`) before submitting.

### Timeout Refund

Applies when the worker doesn't submit by the deadline, or when the arbitrator doesn't resolve within the timeout period.

```text
stakeForfeited = workerStaked ? workerStake : 0
```

Transfers: `amount + stakeForfeited` → buyer.

### Milestone Approval

Per-milestone, same formula as single-shot approval but using `milestone.amount` instead of `amount`. No stake movement -- stake is settled at escrow end.

### Milestone Dispute Resolution

Per-milestone, same formula as single-shot dispute resolution but using `milestone.amount`. No stake movement per-milestone.

### Milestone Worker Stake Settlement

Settled once when all milestones reach terminal states:

```text
workerAwarded = Σ (Approved milestones: milestone.amount)
              + Σ (Resolved milestones: milestone.amount * milestone.awardBps / 10000)
total         = Σ all milestone amounts
stakeReturn   = workerStake * workerAwarded / total    (if workerAwarded > 0 and < total)
              = workerStake                            (if workerAwarded == total)
              = 0                                      (if workerAwarded == 0)
stakeForfeited = workerStake - stakeReturn
```

Cancelled/aborted milestones contribute to `total` but not `workerAwarded`, so their weight dilutes the stake return proportionally.

### Verifier Stake Settlement (Quorum)

When `verifierStakePerVerifier > 0`, each verifier panel member must deposit stake before voting.

- On quorum finalization, verifiers who voted with the majority outcome (approval-majority or rejection-majority) receive their full verifier stake back.
- Verifiers who voted against the majority forfeit their verifier stake to the buyer.
- Abstainers (`quorumVote == 0`) are refunded on quorum finalization.
- If a review cycle exits without quorum finalization (e.g. buyer approval in low-assurance mode, dispute/arbitrator timeout resolution, milestone timeout cancellation, explicit no-quorum expiry via `expireNoQuorum` / `expireMilestoneNoQuorum`, or emergency resolve), all currently deposited verifier stakes are refunded to their depositors before the cycle advances or settles.
- `verifyAndApprove` and `verifyAndApproveMilestone` count as approval votes for quorum and therefore participate in the same majority/minority stake settlement logic.

### Abort Refund

Sum of all `Pending` milestone amounts returned to buyer. Those milestones are set to `Cancelled`.

### Rounding

All divisions floor toward zero (Solidity default). Remainders stay with the buyer path due to subtraction order.

## 6) Invariants

These must hold for every escrow at all times:

1. **Balance conservation**:
   - At terminal completion, escrow balance is always `0`.
   - Before terminal completion, escrow balance equals remaining undistributed escrow principal plus any still-held worker stake.
   - For single-shot escrows, this is `amount` (or `amount + workerStake` when staked).
   - For milestone escrows, each approved/resolved partial payout, each milestone timeout refund, and each `abortRemainingMilestones` refund reduces the remaining undistributed escrow principal before terminal completion.
2. **Terminal exclusivity**: Settled, Refunded, and Cancelled are mutually exclusive. No function can transition from a terminal state to a non-terminal state.
3. **Fund conservation**: total funds distributed from the escrow principal and worker stake never exceeds `amount + workerStake`. Verifier stake flows are governed separately by invariant 9.
4. **Fee bound**: protocol fee never exceeds worker gross award.
5. **Stake lifecycle**: returned fully on approval, split proportionally on dispute, forfeited on timeout or backup activation.
6. **Backup exclusivity**: backup activation can occur at most once per escrow; `activeWorker` replaces the original worker for all subsequent operations.
7. **Milestone ordering**: milestones are processed sequentially; `milestoneIndex` must equal `currentMilestone`.
8. **Milestone fund conservation**: sum of all milestone payouts + refunds + fees + remaining stake = `amount + workerStake`.
9. **Verifier stake conservation**: each deposited verifier stake settles exactly once per review cycle -- either by quorum outcome (majority refund / minority slash) or by full refund when the cycle exits without quorum.

## 7) Emergency Response Protocol (Paper §4.9)

The factory owner can respond to compromised credentials or malicious behavior by freezing participation and, when necessary, force-settling escrows. This implements the paper's emergency response provisions (§4.9).

### Credential Revocation

The factory owner can freeze or unfreeze individual addresses via `freezeAddress(target)` / `unfreezeAddress(target)`. Frozen addresses cannot be used as buyer, worker, verifier panel member, or arbitrator in new escrow creation; `createEscrow` checks the `frozenAddresses` mapping and reverts if any role is frozen.

### Contract Freeze

The factory owner can freeze or unfreeze individual escrows via `freezeEscrow(escrowId)` / `unfreezeEscrow(escrowId)`. A frozen escrow blocks participant-callable state-changing functions protected by `whenNotFrozen`: `fund`, `fundWithAuthorization`, `depositStake`, `depositVerifierStake`, `submit`, `verifyAndApprove`, `approveByBuyer`, `castVerifierVote`, `dispute`, `escalateSilence`, `expireNoQuorum`, `resolveDispute`, `activateBackup`, `submitMilestone`, `verifyAndApproveMilestone`, `approveMilestoneByBuyer`, `castMilestoneVerifierVote`, `disputeMilestone`, `escalateMilestoneSilence`, `expireMilestoneNoQuorum`, `resolveMilestoneDispute`, and `abortRemainingMilestones`. Timeout claim paths (`claimTimeoutRefund`, `claimArbitratorTimeout`) remain callable while frozen to preserve fund-recovery liveness. `emergencyResolve` is intentionally excluded from this participant-callable list because it is owner/factory-callable emergency control.

### Emergency Resolution

The factory owner can force-settle a frozen escrow via `emergencyResolve(escrowId, workerAwardBps)`. If the escrow is in any non-terminal state other than `Created`, settlement uses the same dispute-equivalent proportional math. `Created` is the only special case: it transitions directly to `Cancelled` (no settlement transfer), since no funds have been deposited yet. The escrow must already be frozen; otherwise `emergencyResolve` reverts with `EscrowNotFrozen`.

### Events

- Factory: `AddressFrozen`, `AddressUnfrozen`, `EscrowFrozen`, `EscrowUnfrozen`
- Escrow: `EmergencyFrozen`, `EmergencyUnfrozen`, `EmergencyResolved`

### Invariant

Emergency resolve preserves dispute-equivalent settlement math for funded/disputed/active escrows: proportional split of payment and stake based on `workerAwardBps`, with fee and remainder handling identical to `resolveDispute`. The only exception is the `Created` state, which cancels without settlement.

## 8) Reputation Recording

The factory records per-address outcome counters on-chain, implementing the paper's immutable ledger approach for reputation (§4.6, Table 3). This provides a tamper-proof, auditable record of task outcomes that other contracts and protocols can read without trusting the off-chain server.

### Outcome Categories

| Outcome | Trigger | Description |
|---|---|---|
| `completed` | Approval path (single-shot or all milestones approved) | Task delivered and accepted |
| `disputed` | Dispute resolution path, arbitrator timeout, or mixed milestone outcomes | Task required arbitration or was unresolved |
| `failed` | Timeout refund, or all milestones cancelled | Task not delivered or fully refunded |

### Recording Mechanism

The escrow contract calls `factory.recordOutcome(outcome)` on terminal state transitions that involve actual task activity. The factory validates that the caller is a registered escrow (via `escrowToId` reverse lookup), reads the buyer and active worker from stored mappings, and increments the appropriate counter in `workerReputation` and `buyerReputation`.

Pre-funding cancellation (`cancelBeforeFunding`) does **not** record an outcome since no work was attempted and no funds were at risk.

### Backup Worker Attribution

When a backup worker has been activated, reputation is attributed to the **active worker** (the backup), not the original. The original worker additionally receives a `failed` entry for having defaulted.

### Anti-Spoofing Invariant

Only addresses registered via `createEscrow` can call `recordOutcome`. The factory stores `escrowToId[escrowAddress] = escrowId + 1` during creation (using +1 so that 0 means "not registered"). Any call from an unregistered address reverts with `NotRegisteredEscrow()`.

### Scope and Limitations

V2 records raw outcome counts only. The paper warns that naive implementations are susceptible to gaming (e.g., inflating reputation by only accepting simple, low-risk tasks). Weighted scoring, anti-gaming measures, and behavioral metrics are deferred to V3.

## 9) Economic Parameters

Global (factory-level):
- `protocolFeeBps` (0-10000) -- fee for low-assurance (tier 0) escrows, snapshotted at creation to prevent governance race conditions mid-task.
- `highAssuranceFeeBps` (0-10000) -- fee for high-assurance (tier 1) escrows, snapshotted at creation. Typically higher than `protocolFeeBps` to reflect the additional verification overhead.
- `treasury` address -- snapshotted at creation.
- `complexityFloor` -- minimum escrow amount (in wei or smallest token unit) to justify delegation overhead. Owner-settable via `setComplexityFloor`. `0` means disabled (no minimum). Checked against `p.amount` (total escrow amount) at `createEscrow` time. Rationale (paper §4.3): below a certain complexity floor, transaction costs (gas + protocol fee) exceed the value of the task, rendering delegation infeasible.

Per-escrow:
- `amount` -- total escrow amount (ETH or ERC20). For milestone escrows, equals sum of all milestone amounts.
- `serviceTier` -- `0` (low-assurance / optimistic) or `1` (high-assurance / verified). Immutable after creation. Determines which fee is snapshotted and whether buyer-only approval is permitted (see §9a).
- `workerStake` -- anti-Sybil bond (paper §4.8). `0` means no stake required.
- `submissionDeadline` -- unix timestamp (single-shot) or per-milestone deadlines.
- `reviewPeriodSeconds` -- window for approval/rejection after submission.
- `disputePeriodSeconds` -- window for buyer dispute after review period.
- `arbitratorTimeoutSeconds` -- maximum time for arbitrator to resolve before buyer can claim refund.

Default values: review = 86,400s (24h), dispute = 172,800s (48h).

Rationale (paper §4.3, §4.4): 24h/48h windows balance oversight with capital efficiency. Explicit reject avoids passive acceptance under ambiguity (criticality and accountability). Silence-escalation prevents indefinite lock under asymmetric power/inattention (monitoring and authority gradients).

### 9a) Tiered Service Levels (paper §5.3)

Two service tiers ensure safety does not become a luxury good:

| Tier | Name | Fee Source | Approval Rule |
|------|------|-----------|---------------|
| 0 | Low-assurance (optimistic) | `protocolFeeBps` | Buyer **or** verifier quorum can approve |
| 1 | High-assurance (verified) | `highAssuranceFeeBps` | Verifier quorum **only** can approve; `approveByBuyer` / `approveMilestoneByBuyer` revert with `HighAssuranceRequiresVerifier()` |

**Fee snapshot**: at `createEscrow` time, the factory reads `highAssuranceFeeBps` or `protocolFeeBps` depending on `serviceTier` and writes the result into the escrow's immutable `protocolFeeBpsSnapshot`. Subsequent factory fee changes do not affect existing escrows.

**Invariants**:
- `serviceTier` is immutable (set in constructor, stored as `uint8`).
- `createEscrow` reverts with `InvalidServiceTier()` if `serviceTier > 1`.
- All other escrow behavior (dispute, timeout, settlement math, milestone flows, stake handling) is tier-agnostic -- only the approval gate and fee snapshot differ.

## 10) Edge Cases

- **Late submission**: reverts if `block.timestamp > submissionDeadline`.
- **Approval/dispute race**: first confirmed transition wins; subsequent calls revert by status guard.
- **Buyer inactivity after submission**: verifier panel can still finalize via quorum within review window. Worker can escalate silence to Disputed after review window lapse; arbitrator remains final payout authority.
- **No-quorum expiry**: if neither quorum threshold is reached before the review+dispute window ends, any lifecycle participant (buyer, active worker, or verifier panel member) can call `expireNoQuorum` / `expireMilestoneNoQuorum` to move the cycle to `Disputed` and refund all unsettled verifier stakes.
- **Arbitrator inactivity**: buyer can claim full refund via `claimArbitratorTimeout()` after the configured timeout period. This records a `disputed` outcome (not `failed`) since the arbitrator's inaction -- not the worker's performance -- caused the refund.
- **ERC20 vs ETH**: `token == address(0)` means ETH-denominated. All settlement math is token-agnostic; the transfer mechanism differs.
- **Zero worker stake**: `depositStake()` reverts. Submit proceeds without stake check.
- **Below complexity floor**: `createEscrow` reverts with `BelowComplexityFloor()` if `complexityFloor > 0` and `p.amount < complexityFloor`. The check applies to the total escrow amount, not individual milestone amounts. A floor of `0` disables the check entirely.
- **Abort eligibility**: `abortRemainingMilestones()` requires the current milestone to be in a terminal failure state (Resolved or Cancelled). Cannot abort while a milestone is actively in progress with time remaining.

## 11) Security Model

- `nonReentrant` on payable state-changing functions that transfer ETH.
- Checks-effects-interactions ordering throughout.
- Revert on failed ETH transfers.
- No unbounded loops (milestone count capped at 16).
- Custom errors instead of string reverts.
- Events emitted for all state transitions and administrative actions.
- Fee and treasury snapshotted at creation to prevent governance manipulation mid-lifecycle.
