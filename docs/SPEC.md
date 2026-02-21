# Contract Specification

## 1) Scope
This specification defines the contract behavior for escrow-based task delegation on Base (Ethereum L2).

Included:
- `TaskEscrowFactory` contract
- `TaskEscrow` contract instances
- ETH and ERC20 escrow and settlement
- Roles: buyer, worker, verifier, arbitrator
- Worker stake (anti-Sybil bond)
- Hash-based submission commitment
- Dispute flow and final resolution
- Verifier reject path and worker silence-escalation path

Not yet implemented:
- On-chain reputation
- Open marketplace bidding/auctions
- Multi-verifier quorum

## 1.1 Paper Traceability
Traces to ["Intelligent AI Delegation"](https://arxiv.org/abs/2602.11865) (Tomašev, Franklin, Osindero -- Google DeepMind, 2026). V1 implements the settlement kernel: financial accountability and bounded authority. Adaptive delegation intelligence is deferred to subsequent phases.

Requirement mapping:
- Clear role boundaries: enforced via immutable per-escrow roles (`buyer`, `worker`, `verifier`, `arbitrator`).
- Transfer of authority + accountability: each lifecycle transition is role-gated and event-logged.
- Task constraints: deadlines, review/dispute periods, and strict state-machine transitions.
- Verifiability axis: on-chain submission hash commitments and explicit verifier approval path.
- Reversibility axis: refund and split-settlement outcomes for failed/disputed tasks.
- Monitoring axis: canonical events as machine-readable oversight surface.
- Trust calibration: V1 uses designated verifier/arbitrator identities rather than open trustless proving.

Explicitly deferred paper dimensions:
- Dynamic capability lookup/matching
- Adaptive multi-agent delegation policies
- Distributed reputation markets as primary trust substrate
- Hybrid human/AI oversight optimization at scale

## 2) Terminology
- Task: an off-chain work agreement linked to one escrow contract.
- Submission: worker-delivered off-chain artifact referenced by URI and hash.
- Review Window: period after submission for approval/dispute.
- Dispute Window: period in which buyer may escalate.

## 3) Chain + Tooling
- Network: Base Sepolia
- Solidity: `^0.8.34`
- Framework: Foundry
- Libraries: OpenZeppelin (`ReentrancyGuard`, `Pausable`, `Ownable`/`AccessControl`)

## 4) Roles
- `buyer`: funds escrow and receives refund if task fails.
- `worker`: submits delivery and receives payout when approved/resolved in their favor.
- `verifier`: can mark submission as pass/fail based on agreed criteria.
- `arbitrator`: final authority in disputed cases.
- `treasury`: receives protocol fee from successful payouts.

Role assignment is immutable per escrow in V1.

## 5) Economic Parameters
Global (factory-level):
- `protocolFeeBps` (0-10000)
- `treasury` address

Per-task:
- `amount` (ETH or ERC20 escrow amount)
- `workerStake` (anti-Sybil bond amount; 0 means no stake required -- paper §4.8)
- `submissionDeadline` (unix timestamp)
- `reviewPeriodSeconds`
- `disputePeriodSeconds`

V1 default values:
- `reviewPeriodSeconds = 86_400` (24 hours)
- `disputePeriodSeconds = 172_800` (48 hours)

## 6) Data Model
## 6.1 TaskEscrowFactory
- `uint256 public nextEscrowId`
- `mapping(uint256 => address) public escrowById`
- `uint16 public protocolFeeBps`
- `address public treasury`
- `bool public paused`

## 6.2 TaskEscrow
- `enum Status { Created, Funded, Submitted, Approved, Disputed, Resolved, Settled, Refunded, Cancelled }`
- `address public buyer`
- `address public worker`
- `address public verifier`
- `address public arbitrator`
- `uint256 public immutable amount`
- `uint256 public immutable workerStake` (anti-Sybil bond; 0 = no stake required)
- `bool public workerStaked` (true after worker calls `depositStake()`)
- `uint64 public submissionDeadline`
- `uint64 public reviewPeriodSeconds`
- `uint64 public disputePeriodSeconds`
- `uint64 public arbitratorTimeoutSeconds` (immutable, set at creation)
- `uint64 public submittedAt`
- `uint64 public approvedAt`
- `uint64 public disputedAt` (set when entering Disputed state)
- `Status public status`
- `bytes32 public taskSpecHash`
- `bytes32 public submissionHash`
- `string public submissionURI`
- `string public disputeReasonURI`
- `uint16 public protocolFeeBpsSnapshot`
- `address public treasurySnapshot`

Design notes:
- Fee and treasury are snapshotted at creation to prevent governance race conditions mid-task.
- Off-chain content is referenced by URI and hash only; no on-chain content storage.

## 7) Contract Interfaces
## 7.1 Factory
```solidity
struct CreateParams {
    address buyer;
    address worker;
    address verifier;
    address arbitrator;
    uint256 amount;
    uint256 workerStake;
    uint64 submissionDeadline;
    uint64 reviewPeriodSeconds;
    uint64 disputePeriodSeconds;
    bytes32 taskSpecHash;
    uint64 arbitratorTimeoutSeconds;
    address token;
}

function createEscrow(CreateParams calldata p) external returns (uint256 escrowId, address escrow);

function setProtocolFeeBps(uint16 newFeeBps) external;
function setTreasury(address newTreasury) external;
function setPaused(bool shouldPause) external;
```

## 7.2 Escrow
```solidity
function fund() external payable;
function depositStake() external payable;
function cancelBeforeFunding() external;
function submit(bytes32 _submissionHash, string calldata _submissionURI) external;
function approveByBuyer() external;
function approveByVerifier() external;
function rejectByVerifier(string calldata reasonURI) external;
function dispute(string calldata reasonURI) external;
function escalateSilence(string calldata reasonURI) external;
function resolveDispute(
    uint16 workerAwardBps,
    string calldata resolutionURI
) external;
function claimTimeoutRefund() external;
function claimArbitratorTimeout() external;
```

`workerAwardBps` split semantics in dispute:
- 0 = full refund to buyer
- 10000 = full payout to worker
- intermediate = proportional split

## 8) Events
```solidity
event EscrowCreated(
    uint256 indexed escrowId,
    address indexed escrow,
    address indexed buyer,
    address worker,
    address verifier,
    address arbitrator,
    bytes32 taskSpecHash
);

event EscrowFunded(address indexed buyer, uint256 amount);
event WorkerStakeDeposited(address indexed worker, uint256 amount);
event SubmissionMade(address indexed worker, bytes32 submissionHash, string submissionURI);
event Approved(address indexed approver, uint64 approvedAt);
event Rejected(address indexed verifier, string reasonURI, uint64 rejectedAt);
event Disputed(address indexed raisedBy, string reasonURI, uint64 disputedAt);
event SilenceEscalated(address indexed worker, string reasonURI, uint64 escalatedAt);
event DisputeResolved(address indexed arbitrator, uint16 workerAwardBps, string resolutionURI);
event Settled(uint256 workerNet, uint256 buyerRefund, uint256 protocolFee, uint256 workerStakeReturned);
event Refunded(uint256 amount, uint256 workerStakeForfeited);
event ArbitratorTimeoutClaimed(address indexed buyer, uint64 claimedAt);
event Cancelled();
```

## 9) State Machine
Allowed transitions:
- `Created -> Funded` via `fund()`
- `Created -> Cancelled` via `cancelBeforeFunding()` (buyer only)
- `Funded -> Submitted` via `submit()`
- `Submitted -> Approved` via `approveByBuyer()` or `approveByVerifier()`
- `Submitted -> Disputed` via `rejectByVerifier()` (verifier only, within review window)
- `Submitted -> Disputed` via `dispute()` (buyer only, within review/dispute window)
- `Submitted -> Disputed` via `escalateSilence()` (worker only, after review window lapse without approval/dispute)
- `Approved -> Settled` internal settlement execution
- `Disputed -> Resolved` via `resolveDispute()` (arbitrator only)
- `Resolved -> Settled` internal settlement execution
- `Funded -> Refunded` via `claimTimeoutRefund()` if submission deadline passes with no submission
- `Disputed -> Refunded` via `claimArbitratorTimeout()` if arbitrator timeout passes with no resolution

Invalid transitions MUST revert with custom errors.

## 10) Function Semantics
## 10.1 `fund()`
- Caller must be `buyer`.
- Must send exactly `amount`.
- Status must be `Created`.
- On success set status `Funded`, emit `EscrowFunded`.

## 10.2 `depositStake()`
- Caller must be `worker`.
- Status must be `Funded`.
- `workerStake > 0` (reverts if no stake required).
- Must not have already deposited (reverts with `StakeAlreadyDeposited`).
- For ETH: must send exactly `workerStake`.
- For ERC20: worker must approve token first; contract transfers `workerStake` via `transferFrom`.
- Set `workerStaked = true`, emit `WorkerStakeDeposited`.

## 10.3 `submit(...)`
- Caller must be `worker`.
- Status must be `Funded`.
- `block.timestamp <= submissionDeadline`.
- `submissionHash != bytes32(0)`.
- If `workerStake > 0`, `workerStaked` must be `true` (reverts with `StakeNotDeposited`).
- Set submission fields and `submittedAt`, status `Submitted`, emit `SubmissionMade`.

## 10.4 `approveByBuyer()` / `approveByVerifier()`
- Caller must match respective role.
- Status must be `Submitted`.
- Must be within `submittedAt + reviewPeriodSeconds`.
- Set `approvedAt`, status `Approved`, execute settlement immediately.

## 10.5 `dispute(reasonURI)`
- Caller must be `buyer`.
- Status must be `Submitted`.
- Must be within `submittedAt + reviewPeriodSeconds + disputePeriodSeconds`.
- Set `disputeReasonURI`, status `Disputed`, emit `Disputed`.

## 10.6 `rejectByVerifier(reasonURI)`
- Caller must be `verifier`.
- Status must be `Submitted`.
- Must be within `submittedAt + reviewPeriodSeconds`.
- Set `disputeReasonURI`, status `Disputed`.
- Emit `Rejected` and `Disputed`.

## 10.7 `escalateSilence(reasonURI)`
- Caller must be `worker`.
- Status must be `Submitted`.
- `block.timestamp > submittedAt + reviewPeriodSeconds`.
- No prior approval/dispute is possible due to status guard.
- Set `disputeReasonURI`, status `Disputed`.
- Emit `SilenceEscalated`.

## 10.8 `resolveDispute(workerAwardBps, resolutionURI)`
- Caller must be `arbitrator`.
- Status must be `Disputed`.
- `workerAwardBps <= 10000`.
- Set status `Resolved`, emit `DisputeResolved`, execute split settlement.

## 10.9 `claimTimeoutRefund()`
- Caller must be `buyer`.
- Status must be `Funded`.
- `block.timestamp > submissionDeadline`.
- Refund full amount to buyer, set status `Refunded`, emit `Refunded`.

## 10.10 `claimArbitratorTimeout()`
- Caller must be `buyer`.
- Status must be `Disputed`.
- `block.timestamp > disputedAt + arbitratorTimeoutSeconds`.
- Refund full amount to buyer, set status `Refunded`.
- Emit `ArbitratorTimeoutClaimed` and `Refunded`.

## 10.11 `cancelBeforeFunding()`
- Caller must be `buyer`.
- Status must be `Created`.
- Set status `Cancelled`, emit `Cancelled`.

## 11) Settlement Math
For non-dispute approval:
- `grossWorker = amount`
- `fee = grossWorker * protocolFeeBpsSnapshot / 10000`
- `workerNet = grossWorker - fee`
- `stakeReturn = workerStaked ? workerStake : 0`
- Transfer `workerNet + stakeReturn` to worker, `fee` to treasury
- Emit `Settled(workerNet, 0, fee, stakeReturn)`

For dispute resolution:
- `workerGross = amount * workerAwardBps / 10000`
- `buyerRefund = amount - workerGross`
- `fee = workerGross * protocolFeeBpsSnapshot / 10000`
- `workerNet = workerGross - fee`
- `stakeReturn = workerStaked ? workerStake * workerAwardBps / 10000 : 0`
- `stakeForfeited = workerStaked ? workerStake - stakeReturn : 0`
- Transfer `workerNet + stakeReturn` to worker, `buyerRefund + stakeForfeited` to buyer, `fee` to treasury
- Emit `Settled(workerNet, buyerRefund, fee, stakeReturn)`

For timeout refund (worker didn't submit):
- `stakeForfeited = workerStaked ? workerStake : 0`
- Transfer `amount + stakeForfeited` to buyer
- Emit `Refunded(amount, stakeForfeited)`

For arbitrator timeout:
- `stakeForfeited = workerStaked ? workerStake : 0`
- Transfer `amount + stakeForfeited` to buyer
- Emit `Refunded(amount, stakeForfeited)`

Rounding:
- All divisions floor toward zero (Solidity default).
- Any remainder remains with buyer path due to subtraction order above.

## 12) Access Control Rules
- Factory admin can update fee and treasury, and pause/unpause factory creation.
- Escrow role permissions are strictly per-task and immutable.
- Pausing factory does not freeze existing escrows in V1.

## 13) Security Requirements
- Use `nonReentrant` on payable state-changing functions that transfer ETH.
- Use checks-effects-interactions ordering.
- Revert on failed ETH transfers.
- Avoid unbounded loops.
- Emit events for all terminal and role actions.
- Use custom errors instead of string reverts where possible.

## 14) Invariants
- Escrow balance can only be:
  - `0` after terminal settlement/refund
  - `amount` (or `amount + workerStake` when stake deposited) during active funded/submitted/disputed states
- Terminal states are mutually exclusive: `Settled`, `Refunded`, `Cancelled`.
- No function can move from terminal state to non-terminal state.
- Total funds distributed from escrow never exceeds `amount + workerStake`.
- Fee never exceeds worker gross award.
- Worker stake is returned fully on approval, split proportionally on dispute, forfeited on timeout.

## 15) Edge Cases
- Late submission: reject if block time past deadline.
- Dual approval/dispute race: first confirmed transition wins; subsequent calls revert by status.
- Buyer inactivity after submission:
  - Verifier can still approve within review window.
  - Worker can escalate silence to `Disputed`; arbitrator remains final payout authority.
- Arbitrator inactivity: buyer can claim full refund via `claimArbitratorTimeout()` after the configured timeout period elapses.

## 16) Off-chain Integration Requirements
Go server must persist (SQLite):
- `chainId`
- `factoryAddress`
- `escrowAddress`
- `escrowId`
- tx hashes for each state transition

Indexer must reconcile events:
- `EscrowCreated`
- `EscrowFunded`
- `WorkerStakeDeposited`
- `SubmissionMade`
- `Approved`
- `Rejected`
- `Disputed`
- `SilenceEscalated`
- `DisputeResolved`
- `Settled`
- `Refunded`
- `ArbitratorTimeoutClaimed`
- `Cancelled`

MCP tools and HTTP API must expose on-chain status as source of truth for payment state.

## 17) Testing Requirements (Foundry)
Unit tests:
- Happy path: create -> fund -> submit -> approve -> settle
- Timeout refund path
- Dispute + split resolution path (0, 5000, 10000 bps)
- Verifier reject path
- Worker silence-escalation path
- Permission failures for every role-gated function
- State transition reverts
- Fee calculation correctness and rounding behavior

Invariant/property tests:
- Conservation of funds
- No payouts in invalid states
- Terminal state immutability

Fuzz tests:
- `workerAwardBps` bounds
- Timing boundaries at exact cutoff timestamps

## 18) Deployment + Ops (V1)
- Deploy factory with:
  - conservative fee bps
  - multisig treasury
  - multisig admin key
- Verify contracts on explorer.
- Publish ABI and addresses in repo.
- Add pause runbook and incident response checklist.

## 19) Parameter Rationale (Paper-Grounded)
- `reviewPeriodSeconds = 86_400` (24 hours)
- `disputePeriodSeconds = 172_800` (48 hours)
- Verifier explicit reject: enabled via `rejectByVerifier`.
- Worker silence escalation: enabled via `escalateSilence`.

Rationale linked to the paper's delegation axes:
- Criticality and accountability: explicit reject avoids passive acceptance under ambiguity.
- Monitoring and authority gradients: silence-escalation prevents indefinite lock under asymmetric power/inattention.
- Transaction cost and reversibility: 24h/48h windows balance oversight with capital efficiency.

## 20) Definition of Done (V1)
- Contracts implemented and deployed to Base Sepolia.
- All required tests passing in CI.
- Go server (MCP + HTTP API) can execute full lifecycle on testnet.
- Event indexer keeps off-chain state in sync with chain.
- Security checklist complete with no high-severity unresolved findings.

---

## V2 Extensions

### 21) Milestone-Based Escrow

#### 21.1 Paper Grounding

The paper identifies milestones as a core mechanism across multiple pillars:

- **Adaptive Coordination (§4.4)**: "Delegation agreements encoded as smart contracts may also contain pre-agreed executable clauses for adaptive coordination. For example, a clause in the delegation agreement can specify a backup agent, the function that would automatically re-allocate the task, and the associated payment to the backup should the primary delegatee fail to submit a valid zero-knowledge proof checkpoint by a given deadline."
- **Monitoring (§4.5)**: "Smart contracts on blockchain can be used to make the delegatee agent commit to publishing key progress milestones or checkpoints to the blockchain. These could be coupled by algorithmic triggers in response to performance degradation."
- **Verifiable Task Completion (§4.8)**: "Verification serves as the definitive event that transforms a provisional output into a settled fact within the agentic market, establishing the basis for payment release, reputation updates, and the assignment of liability."
- **Protocol Extensions (§6.1)**: "It would need to be further coupled with explicit clauses within the smart contract that enable partial compensation, and verification of the task completion percentage."

Milestones transform the single-shot escrow into a staged contract with intermediate verification checkpoints and partial payouts, enabling adaptive coordination for long-running or complex tasks.

#### 21.2 Overview

A milestone escrow divides a task into ordered stages (milestones), each with its own payment amount, submission deadline, and review cycle. The total escrow amount equals the sum of all milestone amounts. The buyer funds the full amount upfront; payouts are released incrementally as each milestone is approved.

Key properties:
- Milestones are defined at escrow creation and are immutable.
- Each milestone progresses through its own submit → review → approve/dispute cycle.
- Approved milestones pay out immediately (partial settlement).
- A disputed milestone follows the same arbitrator resolution flow as V1.
- The buyer can cancel remaining milestones after a dispute resolution or timeout, receiving a refund for uncompleted work.
- Worker stake (if any) is held for the full escrow duration and returned/forfeited at final settlement.

#### 21.3 Data Model Extension

New fields on `TaskEscrow` (milestone mode):

```solidity
struct Milestone {
    uint256 amount;           // payment for this milestone
    uint64 submissionDeadline; // unix timestamp deadline for this milestone's submission
    bytes32 submissionHash;   // set on submit
    string submissionURI;     // set on submit
    uint64 submittedAt;       // set on submit
    uint64 approvedAt;        // set on approve
    uint64 disputedAt;        // set on dispute
    string disputeReasonURI;  // set on dispute/reject
    MilestoneStatus status;   // per-milestone status
    uint16 awardBps;          // set on dispute resolution (worker's award in basis points, 0-10000)
}

enum MilestoneStatus {
    Pending,     // not yet submitted
    Submitted,   // worker submitted, awaiting review
    Approved,    // approved, payout released
    Disputed,    // under dispute
    Resolved,    // dispute resolved, payout split
    Cancelled    // cancelled by buyer (remaining milestones after abort)
}
```

- `uint256 public immutable amount` -- total escrow amount (sum of all milestone amounts). This is the funding target. For single-milestone escrows, `amount` equals the sole milestone's amount, preserving V1 compatibility.
- `uint8 public milestoneCount` -- number of milestones (1 = V1-equivalent single-shot; >1 = milestone mode).
- `uint8 public currentMilestone` -- index of the active milestone (0-based). Advanced by `_advanceMilestone()` after each milestone reaches a terminal state.
- `Milestone[] public milestones` -- milestone array, length = `milestoneCount`.
- `awardBps` on each `Milestone` records the arbitrator's resolution split (0 = full refund to buyer, 10000 = full payment to worker). Set by `resolveMilestoneDispute()` and used by `_settleWorkerStake()` to compute proportional stake return.

Escrow-level `status` semantics in milestone mode:
- `Created` → `Funded` → active milestone cycling → `Settled` (all milestones approved/resolved) or `Refunded` (remaining milestones cancelled after abort).
- The escrow-level status reflects the aggregate: `Funded` while milestones are in progress, `Settled` when all milestones reach a terminal state.

#### 21.4 Milestone Lifecycle

```text
Pending ──submit()──> Submitted
                        │  │  │
                        │  │  └─ approve() ──> Approved (payout released)
                        │  │
                        │  └─ dispute()/rejectByVerifier()/escalateSilence() ──> Disputed
                        │                                                          │
                        │                                                    resolveDispute()
                        │                                                          │
                        │                                                          v
                        │                                                       Resolved
                        │
                        └─ claimTimeoutRefund() (if deadline passed) ──> Cancelled
```

After each milestone reaches a terminal state (Approved, Resolved, or Cancelled), the escrow advances `currentMilestone`. When all milestones are terminal, the escrow-level status transitions to `Settled` or `Refunded`.

#### 21.5 Contract Interface Extension

```solidity
struct CreateMilestoneParams {
    uint256 amount;           // payment for this milestone
    uint64 submissionDeadline; // deadline for this milestone
}

struct CreateEscrowParams {
    // ... existing fields ...
    CreateMilestoneParams[] milestones; // empty array = single-shot (V1 behavior)
}
```

New/modified functions:

```solidity
// Submit work for the current milestone
function submitMilestone(uint8 milestoneIndex, bytes32 _submissionHash, string calldata _submissionURI) external;

// Approve current milestone (buyer or verifier)
function approveMilestoneByBuyer(uint8 milestoneIndex) external;
function approveMilestoneByVerifier(uint8 milestoneIndex) external;

// Dispute current milestone
function disputeMilestone(uint8 milestoneIndex, string calldata reasonURI) external;
function rejectMilestoneByVerifier(uint8 milestoneIndex, string calldata reasonURI) external;
function escalateMilestoneSilence(uint8 milestoneIndex, string calldata reasonURI) external;

// Resolve dispute on a milestone
function resolveMilestoneDispute(uint8 milestoneIndex, uint16 workerAwardBps, string calldata resolutionURI) external;

// Claim timeout refund for a milestone whose deadline passed without submission
function claimMilestoneTimeoutRefund(uint8 milestoneIndex) external;

// Abort remaining milestones after a dispute resolution or timeout on any milestone
// Refunds the sum of all Pending milestone amounts to buyer
function abortRemainingMilestones() external;
```

The `milestoneIndex` parameter prevents race conditions and ensures the caller is operating on the intended milestone.

#### 21.6 Settlement Math (Milestone Mode)

Per-milestone approval:
- `grossWorker = milestone.amount`
- `fee = grossWorker * protocolFeeBpsSnapshot / 10000`
- `workerNet = grossWorker - fee`
- Transfer `workerNet` to worker, `fee` to treasury
- Emit `MilestoneSettled(milestoneIndex, workerNet, 0, fee)`

Per-milestone dispute resolution:
- `workerGross = milestone.amount * workerAwardBps / 10000`
- `buyerRefund = milestone.amount - workerGross`
- `fee = workerGross * protocolFeeBpsSnapshot / 10000`
- `workerNet = workerGross - fee`
- Transfer `workerNet` to worker, `buyerRefund` to buyer, `fee` to treasury
- Emit `MilestoneSettled(milestoneIndex, workerNet, buyerRefund, fee)`

Worker stake settlement (escrow-level, after all milestones terminal):
- If all milestones approved: full stake returned to worker.
- If any milestones disputed/resolved: stake split proportionally based on the ratio of worker-awarded amounts to total amount.
- If aborted: stake forfeited proportionally to cancelled milestone amounts.
- Stake is settled once when the escrow reaches its terminal state, not per-milestone.

Abort refund:
- Sum of all `Pending` milestone amounts returned to buyer.
- Pending milestones set to `Cancelled`.

#### 21.7 Events (Milestone Mode)

```solidity
event MilestoneSubmitted(uint8 indexed milestoneIndex, bytes32 submissionHash, string submissionURI);
event MilestoneApproved(uint8 indexed milestoneIndex, address indexed approver, uint64 approvedAt);
event MilestoneRejected(uint8 indexed milestoneIndex, address indexed verifier, string reasonURI);
event MilestoneDisputed(uint8 indexed milestoneIndex, address indexed raisedBy, string reasonURI);
event MilestoneSilenceEscalated(uint8 indexed milestoneIndex, address indexed worker, string reasonURI);
event MilestoneDisputeResolved(uint8 indexed milestoneIndex, uint16 workerAwardBps, string resolutionURI);
event MilestoneSettled(uint8 indexed milestoneIndex, uint256 workerNet, uint256 buyerRefund, uint256 protocolFee);
event MilestoneCancelled(uint8 indexed milestoneIndex);
event RemainingMilestonesAborted(uint8 fromIndex, uint256 refundAmount);
```

#### 21.8 Constraints and Invariants

- Maximum milestone count: 16 (prevents gas exhaustion in loops; sufficient for practical task decomposition).
- Milestone amounts must sum to `amount` exactly.
- Milestones must be processed in order: `milestoneIndex` must equal `currentMilestone` for submit/approve/dispute operations.
- Each milestone's `submissionDeadline` must be strictly after the previous milestone's deadline.
- `abortRemainingMilestones()` can only be called by the buyer, and only after the current milestone reaches `Disputed` → `Resolved` or `Cancelled` (timeout). It cannot be called while a milestone is in `Pending` or `Submitted` state with time remaining.
- Conservation of funds: sum of all milestone payouts + refunds + fees + remaining stake = `amount + workerStake`.
- Single-milestone escrows (milestoneCount = 1) must behave identically to V1 escrows. This is the backward compatibility requirement.

#### 21.9 Arbitrator Timeout (Milestone Mode)

- `claimArbitratorTimeout()` applies per-milestone: if `milestone.disputedAt + arbitratorTimeoutSeconds` passes without resolution, the buyer can claim a refund for that milestone's amount.
- After an arbitrator timeout on any milestone, the buyer may call `abortRemainingMilestones()` to cancel all subsequent milestones.

#### 21.10 Off-chain Integration (Milestone Mode)

Storage:
- New `milestones` table: `escrow_id`, `milestone_index`, `amount`, `submission_deadline`, `status`, `submission_hash`, `submission_uri`, `submitted_at`, `approved_at`, `disputed_at`, `dispute_reason_uri`.
- The `escrows` table gains `milestone_count` and `current_milestone` columns.

Indexer:
- Must reconcile all milestone-specific events.
- Must track per-milestone status independently.

MCP tools:
- `create_escrow` gains an optional `milestones` array parameter.
- `submit_work`, `approve_work`, `dispute_work`, `resolve_dispute` gain a `milestone_index` parameter (defaults to 0 for single-shot escrows).
- New `abort_remaining_milestones` tool.
- `get_escrow` response includes milestone details.

HTTP API:
- `POST /api/v1/escrows` request body gains optional `milestones` array.
- `GET /api/v1/escrows/{id}` response includes milestone array with per-milestone status.
- Existing action endpoints gain optional `milestone_index` query/body parameter.
- New `POST /api/v1/escrows/{id}/abort-milestones` endpoint.

#### 21.11 Testing Requirements (Milestone Mode)

Unit tests:
- Happy path: create 3-milestone escrow → fund → submit/approve each → settle
- Partial completion: approve 2 of 3 milestones, abort remaining
- Dispute on milestone 2: resolve with split, abort milestone 3
- Timeout on milestone 1: refund, abort remaining
- Arbitrator timeout on a milestone: refund, abort remaining
- Permission failures for milestone-specific functions
- State transition reverts (wrong milestone index, wrong status)
- Worker stake settlement across mixed milestone outcomes
- Single-milestone escrow behaves identically to V1
- Fee calculation correctness per-milestone

Invariant/property tests:
- Conservation of funds across all milestones
- No payouts for cancelled milestones
- Milestone ordering is enforced
- Terminal state immutability per-milestone

Fuzz tests:
- Variable milestone counts (1-16)
- Variable `workerAwardBps` per disputed milestone
- Timing boundaries at exact milestone deadlines
