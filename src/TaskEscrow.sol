// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {IERC20} from "./interfaces/IERC20.sol";
import {IEIP3009} from "./interfaces/IEIP3009.sol";
import {MilestoneLib} from "./MilestoneLib.sol";

interface ITaskEscrowFactory {
    function recordOutcome(uint8 outcome) external;
}

contract TaskEscrow {
    enum Status {
        Created,
        Funded,
        Submitted,
        Approved,
        Disputed,
        Resolved,
        Settled,
        Refunded,
        Cancelled
    }

    enum MilestoneStatus {
        Pending,
        Submitted,
        Approved,
        Disputed,
        Resolved,
        Cancelled
    }

    struct Milestone {
        uint256 amount;
        uint64 submissionDeadline;
        bytes32 submissionHash;
        string submissionURI;
        uint64 submittedAt;
        uint64 approvedAt;
        uint64 disputedAt;
        string disputeReasonURI;
        MilestoneStatus status;
        uint16 awardBps;
    }

    error Unauthorized();
    error InvalidState();
    error InvalidAmount();
    error InvalidAddress();
    error InvalidDeadline();
    error InvalidHash();
    error WindowExpired();
    error WindowNotOpen();
    error InvalidAwardBps();
    error TransferFailed();
    error Reentrancy();
    error ArbitratorTimeoutNotReached();
    error ETHNotAccepted();
    error InsufficientReceived();
    error StakeNotDeposited();
    error StakeAlreadyDeposited();
    error InvalidMilestoneIndex();
    error TooManyMilestones();
    error MilestoneAmountMismatch();
    error InvalidMilestoneDeadlineOrder();
    error BackupAlreadyActivated();
    error NoBackupDesignated();
    error FactoryCallbackFailed();

    event EscrowFunded(address indexed buyer, uint256 amount);
    event WorkerStakeDeposited(address indexed worker, uint256 amount);
    event SubmissionMade(address indexed worker, bytes32 submissionHash, string submissionURI);
    event Approved(address indexed approver, uint64 approvedAt);
    event Rejected(address indexed verifier, string reasonURI, uint64 rejectedAt);
    event Disputed(address indexed raisedBy, string reasonURI, uint64 disputedAt);
    event SilenceEscalated(address indexed worker, string reasonURI, uint64 escalatedAt);
    event DisputeResolved(address indexed arbitrator, uint16 workerAwardBps, string resolutionURI);
    event ArbitratorTimeoutClaimed(address indexed buyer, uint64 claimedAt);
    event Settled(uint256 workerNet, uint256 buyerRefund, uint256 protocolFee, uint256 workerStakeReturned);
    event Refunded(uint256 amount, uint256 workerStakeForfeited);
    event Cancelled();

    event MilestoneSubmitted(uint8 indexed milestoneIndex, bytes32 submissionHash, string submissionURI);
    event MilestoneApproved(uint8 indexed milestoneIndex, address indexed approver, uint64 approvedAt);
    event MilestoneRejected(uint8 indexed milestoneIndex, address indexed verifier, string reasonURI);
    event MilestoneDisputed(uint8 indexed milestoneIndex, address indexed raisedBy, string reasonURI);
    event MilestoneSilenceEscalated(uint8 indexed milestoneIndex, address indexed worker, string reasonURI);
    event MilestoneDisputeResolved(uint8 indexed milestoneIndex, uint16 workerAwardBps, string resolutionURI);
    event MilestoneSettled(uint8 indexed milestoneIndex, uint256 workerNet, uint256 buyerRefund, uint256 protocolFee);
    event MilestoneCancelled(uint8 indexed milestoneIndex);
    event RemainingMilestonesAborted(uint8 fromIndex, uint256 refundAmount);
    event BackupActivated(address indexed previousWorker, address indexed newWorker, uint64 newDeadline);

    address public immutable factory;
    address public immutable token;
    address public immutable buyer;
    address public immutable worker;
    address public immutable verifier;
    address public immutable arbitrator;
    uint256 public immutable amount;
    uint256 public immutable workerStake;
    uint64 public immutable submissionDeadline;
    uint64 public immutable reviewPeriodSeconds;
    uint64 public immutable disputePeriodSeconds;
    bytes32 public immutable taskSpecHash;
    uint16 public immutable protocolFeeBpsSnapshot;
    address public immutable treasurySnapshot;
    uint64 public immutable arbitratorTimeoutSeconds;
    address public immutable backupWorker;
    uint64 public immutable backupDeadlineExtension;

    address public activeWorker;
    bool public backupActivated;
    uint64 public deadlineExtensionApplied;

    uint64 public submittedAt;
    uint64 public approvedAt;
    uint64 public disputedAt;
    Status public status;
    bool public workerStaked;
    bytes32 public submissionHash;
    string public submissionURI;
    string public disputeReasonURI;

    uint8 public milestoneCount;
    uint8 public currentMilestone;
    Milestone[] public milestones;

    error RolesNotDistinct();

    struct CreateMilestoneParams {
        uint256 amount;
        uint64 submissionDeadline;
    }

    struct Params {
        address factory;
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
        uint16 protocolFeeBpsSnapshot;
        address treasurySnapshot;
        uint64 arbitratorTimeoutSeconds;
        address token;
        address backupWorker;
        uint64 backupDeadlineExtension;
        CreateMilestoneParams[] milestones;
    }

    uint8 private constant OUTCOME_COMPLETED = 1;
    uint8 private constant OUTCOME_DISPUTED = 2;
    uint8 private constant OUTCOME_FAILED = 3;

    uint256 private constant _NOT_ENTERED = 1;
    uint256 private constant _ENTERED = 2;
    uint256 private _locked = _NOT_ENTERED;

    constructor(Params memory p) {
        if (p.buyer == address(0) || p.worker == address(0) || p.verifier == address(0) || p.arbitrator == address(0)) {
            revert InvalidAddress();
        }
        if (p.treasurySnapshot == address(0)) revert InvalidAddress();
        if (
            p.buyer == p.worker || p.buyer == p.verifier || p.buyer == p.arbitrator || p.worker == p.verifier
                || p.worker == p.arbitrator || p.verifier == p.arbitrator
        ) {
            revert RolesNotDistinct();
        }
        if (p.backupWorker != address(0)) {
            if (
                p.backupWorker == p.buyer || p.backupWorker == p.worker || p.backupWorker == p.verifier
                    || p.backupWorker == p.arbitrator
            ) {
                revert RolesNotDistinct();
            }
        }
        if (p.amount == 0) revert InvalidAmount();
        if (p.submissionDeadline <= block.timestamp) revert InvalidDeadline();
        if (p.protocolFeeBpsSnapshot > 10_000) revert InvalidAwardBps();

        factory = p.factory;
        buyer = p.buyer;
        worker = p.worker;
        verifier = p.verifier;
        arbitrator = p.arbitrator;
        amount = p.amount;
        workerStake = p.workerStake;
        submissionDeadline = p.submissionDeadline;
        reviewPeriodSeconds = p.reviewPeriodSeconds;
        disputePeriodSeconds = p.disputePeriodSeconds;
        taskSpecHash = p.taskSpecHash;
        protocolFeeBpsSnapshot = p.protocolFeeBpsSnapshot;
        treasurySnapshot = p.treasurySnapshot;
        arbitratorTimeoutSeconds = p.arbitratorTimeoutSeconds;
        token = p.token;
        backupWorker = p.backupWorker;
        backupDeadlineExtension = p.backupDeadlineExtension;
        activeWorker = p.worker;
        status = Status.Created;

        _initMilestones(p);
    }

    function _initMilestones(Params memory p) internal {
        if (p.milestones.length > 0) {
            if (p.milestones.length > 16) revert TooManyMilestones();
            uint256 total;
            uint64 prevDl;
            for (uint256 i = 0; i < p.milestones.length; i++) {
                if (p.milestones[i].submissionDeadline <= block.timestamp) revert InvalidDeadline();
                if (i > 0 && p.milestones[i].submissionDeadline <= prevDl) revert InvalidMilestoneDeadlineOrder();
                prevDl = p.milestones[i].submissionDeadline;
                total += p.milestones[i].amount;
                milestones.push(
                    Milestone({
                        amount: p.milestones[i].amount,
                        submissionDeadline: p.milestones[i].submissionDeadline,
                        submissionHash: bytes32(0),
                        submissionURI: "",
                        submittedAt: 0,
                        approvedAt: 0,
                        disputedAt: 0,
                        disputeReasonURI: "",
                        status: MilestoneStatus.Pending,
                        awardBps: 0
                    })
                );
            }
            if (total != p.amount) revert MilestoneAmountMismatch();
            milestoneCount = uint8(p.milestones.length);
        } else {
            milestoneCount = 1;
            milestones.push(
                Milestone({
                    amount: p.amount,
                    submissionDeadline: p.submissionDeadline,
                    submissionHash: bytes32(0),
                    submissionURI: "",
                    submittedAt: 0,
                    approvedAt: 0,
                    disputedAt: 0,
                    disputeReasonURI: "",
                    status: MilestoneStatus.Pending,
                    awardBps: 0
                })
            );
        }
    }

    modifier nonReentrant() {
        _nonReentrantBefore();
        _;
        _locked = _NOT_ENTERED;
    }

    function _nonReentrantBefore() internal {
        if (_locked == _ENTERED) revert Reentrancy();
        _locked = _ENTERED;
    }

    function _requireBuyer() internal view {
        if (msg.sender != buyer) revert Unauthorized();
    }

    function _requireState(Status s) internal view {
        if (status != s) revert InvalidState();
    }

    // ── V1 single-shot functions ──

    function fund() external payable nonReentrant {
        _requireBuyer();
        _requireState(Status.Created);
        if (token == address(0)) {
            if (msg.value != amount) revert InvalidAmount();
        } else {
            if (msg.value != 0) revert ETHNotAccepted();
            _receiveERC20(amount);
        }
        status = Status.Funded;
        emit EscrowFunded(msg.sender, amount);
    }

    /// @notice Fund via EIP-3009 signed authorization (gasless).
    /// Any caller may submit; `from` must be the buyer.
    function fundWithAuthorization(
        address from,
        uint256 validAfter,
        uint256 validBefore,
        bytes32 nonce,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) external nonReentrant {
        if (from != buyer) revert Unauthorized();
        _requireState(Status.Created);
        if (token == address(0)) revert ETHNotAccepted();

        _receiveEIP3009(from, amount, validAfter, validBefore, nonce, v, r, s);

        status = Status.Funded;
        emit EscrowFunded(from, amount);
    }

    function cancelBeforeFunding() external {
        _requireBuyer();
        _requireState(Status.Created);
        status = Status.Cancelled;
        emit Cancelled();
    }

    function depositStake() external payable nonReentrant {
        if (msg.sender != activeWorker) revert Unauthorized();
        _requireState(Status.Funded);
        if (workerStake == 0) revert InvalidAmount();
        if (workerStaked) revert StakeAlreadyDeposited();
        if (token == address(0)) {
            if (msg.value != workerStake) revert InvalidAmount();
        } else {
            if (msg.value != 0) revert ETHNotAccepted();
            _receiveERC20(workerStake);
        }
        workerStaked = true;
        emit WorkerStakeDeposited(msg.sender, workerStake);
    }

    function submit(bytes32 _submissionHash, string calldata _submissionURI) external {
        if (msg.sender != activeWorker) revert Unauthorized();
        _requireState(Status.Funded);
        if (block.timestamp > uint256(submissionDeadline) + uint256(deadlineExtensionApplied)) revert WindowExpired();
        if (_submissionHash == bytes32(0)) revert InvalidHash();
        if (workerStake > 0 && !workerStaked) revert StakeNotDeposited();
        submissionHash = _submissionHash;
        submissionURI = _submissionURI;
        submittedAt = uint64(block.timestamp);
        status = Status.Submitted;
        emit SubmissionMade(msg.sender, _submissionHash, _submissionURI);
        if (milestoneCount == 1) _syncMs0Submit(_submissionHash, _submissionURI);
    }

    function approveByBuyer() external nonReentrant {
        _requireBuyer();
        _approve(msg.sender);
    }

    function approveByVerifier() external nonReentrant {
        if (msg.sender != verifier) revert Unauthorized();
        _approve(msg.sender);
    }

    function rejectByVerifier(string calldata reasonURI) external {
        if (msg.sender != verifier) revert Unauthorized();
        _requireState(Status.Submitted);
        if (block.timestamp > _reviewWindowEnds()) revert WindowExpired();
        disputeReasonURI = reasonURI;
        disputedAt = uint64(block.timestamp);
        status = Status.Disputed;
        emit Rejected(msg.sender, reasonURI, uint64(block.timestamp));
        emit Disputed(msg.sender, reasonURI, uint64(block.timestamp));
        if (milestoneCount == 1) _syncMs0Dispute(reasonURI);
    }

    function dispute(string calldata reasonURI) external {
        _requireBuyer();
        _requireState(Status.Submitted);
        if (block.timestamp > _disputeWindowEnds()) revert WindowExpired();
        disputeReasonURI = reasonURI;
        disputedAt = uint64(block.timestamp);
        status = Status.Disputed;
        emit Disputed(msg.sender, reasonURI, uint64(block.timestamp));
        if (milestoneCount == 1) _syncMs0Dispute(reasonURI);
    }

    function escalateSilence(string calldata reasonURI) external {
        if (msg.sender != activeWorker) revert Unauthorized();
        _requireState(Status.Submitted);
        if (block.timestamp <= _reviewWindowEnds()) revert WindowNotOpen();
        if (block.timestamp > _disputeWindowEnds()) revert WindowExpired();
        disputeReasonURI = reasonURI;
        disputedAt = uint64(block.timestamp);
        status = Status.Disputed;
        emit SilenceEscalated(msg.sender, reasonURI, uint64(block.timestamp));
        emit Disputed(msg.sender, reasonURI, uint64(block.timestamp));
        if (milestoneCount == 1) _syncMs0Dispute(reasonURI);
    }

    function resolveDispute(uint16 workerAwardBps, string calldata resolutionURI) external nonReentrant {
        if (msg.sender != arbitrator) revert Unauthorized();
        _requireState(Status.Disputed);
        if (workerAwardBps > 10_000) revert InvalidAwardBps();
        status = Status.Resolved;
        emit DisputeResolved(msg.sender, workerAwardBps, resolutionURI);
        if (milestoneCount == 1) milestones[0].status = MilestoneStatus.Resolved;
        _settleResolved(workerAwardBps);
    }

    function claimTimeoutRefund() external nonReentrant {
        _requireBuyer();
        _requireState(Status.Funded);
        if (block.timestamp <= uint256(submissionDeadline) + uint256(deadlineExtensionApplied)) revert WindowNotOpen();
        uint256 sf = workerStaked ? workerStake : 0;
        status = Status.Refunded;
        if (milestoneCount == 1) milestones[0].status = MilestoneStatus.Cancelled;
        _send(buyer, amount + sf);
        emit Refunded(amount, sf);
        _recordOutcome(OUTCOME_FAILED);
    }

    function claimArbitratorTimeout() external nonReentrant {
        _requireBuyer();
        _requireState(Status.Disputed);
        if (block.timestamp <= uint256(disputedAt) + uint256(arbitratorTimeoutSeconds)) {
            revert ArbitratorTimeoutNotReached();
        }
        uint256 sf = workerStaked ? workerStake : 0;
        status = Status.Refunded;
        if (milestoneCount == 1) milestones[0].status = MilestoneStatus.Cancelled;
        _send(buyer, amount + sf);
        emit ArbitratorTimeoutClaimed(msg.sender, uint64(block.timestamp));
        emit Refunded(amount, sf);
        _recordOutcome(OUTCOME_DISPUTED);
    }

    // ── Backup agent activation (paper §4.4) ──

    function activateBackup() external nonReentrant {
        _requireBuyer();
        _requireState(Status.Funded);
        if (backupWorker == address(0)) revert NoBackupDesignated();
        if (backupActivated) revert BackupAlreadyActivated();

        if (milestoneCount <= 1) {
            // Single-shot: primary must have missed the submission deadline
            if (block.timestamp <= uint256(submissionDeadline) + uint256(deadlineExtensionApplied)) {
                revert WindowNotOpen();
            }
        } else {
            // Milestone mode: current milestone must have timed out
            Milestone storage ms = milestones[currentMilestone];
            if (ms.status != MilestoneStatus.Pending) revert InvalidState();
            if (block.timestamp <= ms.submissionDeadline) revert WindowNotOpen();
        }

        address previousWorker = activeWorker;

        // Forfeit primary's stake to buyer if deposited
        if (workerStaked) {
            _send(buyer, workerStake);
            workerStaked = false;
        }

        activeWorker = backupWorker;
        backupActivated = true;

        if (milestoneCount <= 1) {
            deadlineExtensionApplied = uint64(uint256(deadlineExtensionApplied) + uint256(backupDeadlineExtension));
            uint64 newDeadline = uint64(uint256(submissionDeadline) + uint256(deadlineExtensionApplied));
            emit BackupActivated(previousWorker, backupWorker, newDeadline);
        } else {
            Milestone storage ms = milestones[currentMilestone];
            ms.submissionDeadline = uint64(uint256(ms.submissionDeadline) + uint256(backupDeadlineExtension));
            emit BackupActivated(previousWorker, backupWorker, ms.submissionDeadline);
        }
    }

    // ── Milestone functions (multi-milestone mode, milestoneCount > 1) ──

    function submitMilestone(uint8 milestoneIndex, bytes32 _submissionHash, string calldata _submissionURI) external {
        if (msg.sender != activeWorker) revert Unauthorized();
        _requireState(Status.Funded);
        if (milestoneCount <= 1) revert InvalidState();
        if (milestoneIndex != currentMilestone) revert InvalidMilestoneIndex();
        if (_submissionHash == bytes32(0)) revert InvalidHash();
        if (workerStake > 0 && !workerStaked) revert StakeNotDeposited();
        Milestone storage ms = milestones[milestoneIndex];
        if (ms.status != MilestoneStatus.Pending) revert InvalidState();
        if (block.timestamp > ms.submissionDeadline) revert WindowExpired();
        ms.submissionHash = _submissionHash;
        ms.submissionURI = _submissionURI;
        ms.submittedAt = uint64(block.timestamp);
        ms.status = MilestoneStatus.Submitted;
        emit MilestoneSubmitted(milestoneIndex, _submissionHash, _submissionURI);
    }

    function approveMilestoneByBuyer(uint8 milestoneIndex) external nonReentrant {
        _requireBuyer();
        _approveMilestone(milestoneIndex, msg.sender);
    }

    function approveMilestoneByVerifier(uint8 milestoneIndex) external nonReentrant {
        if (msg.sender != verifier) revert Unauthorized();
        _approveMilestone(milestoneIndex, msg.sender);
    }

    function disputeMilestone(uint8 milestoneIndex, string calldata reasonURI) external {
        _requireMultiMsFunded();
        _requireBuyer();
        if (milestoneIndex != currentMilestone) revert InvalidMilestoneIndex();
        Milestone storage ms = milestones[milestoneIndex];
        if (ms.status != MilestoneStatus.Submitted) revert InvalidState();
        if (block.timestamp > uint256(ms.submittedAt) + uint256(reviewPeriodSeconds) + uint256(disputePeriodSeconds)) {
            revert WindowExpired();
        }
        ms.disputeReasonURI = reasonURI;
        ms.disputedAt = uint64(block.timestamp);
        ms.status = MilestoneStatus.Disputed;
        emit MilestoneDisputed(milestoneIndex, msg.sender, reasonURI);
    }

    function rejectMilestoneByVerifier(uint8 milestoneIndex, string calldata reasonURI) external {
        _requireMultiMsFunded();
        if (msg.sender != verifier) revert Unauthorized();
        if (milestoneIndex != currentMilestone) revert InvalidMilestoneIndex();
        Milestone storage ms = milestones[milestoneIndex];
        if (ms.status != MilestoneStatus.Submitted) revert InvalidState();
        if (block.timestamp > uint256(ms.submittedAt) + uint256(reviewPeriodSeconds)) revert WindowExpired();
        ms.disputeReasonURI = reasonURI;
        ms.disputedAt = uint64(block.timestamp);
        ms.status = MilestoneStatus.Disputed;
        emit MilestoneRejected(milestoneIndex, msg.sender, reasonURI);
        emit MilestoneDisputed(milestoneIndex, msg.sender, reasonURI);
    }

    function escalateMilestoneSilence(uint8 milestoneIndex, string calldata reasonURI) external {
        _requireMultiMsFunded();
        if (msg.sender != activeWorker) revert Unauthorized();
        if (milestoneIndex != currentMilestone) revert InvalidMilestoneIndex();
        Milestone storage ms = milestones[milestoneIndex];
        if (ms.status != MilestoneStatus.Submitted) revert InvalidState();
        uint256 reviewEnd = uint256(ms.submittedAt) + uint256(reviewPeriodSeconds);
        if (block.timestamp <= reviewEnd) revert WindowNotOpen();
        if (block.timestamp > reviewEnd + uint256(disputePeriodSeconds)) revert WindowExpired();
        ms.disputeReasonURI = reasonURI;
        ms.disputedAt = uint64(block.timestamp);
        ms.status = MilestoneStatus.Disputed;
        emit MilestoneSilenceEscalated(milestoneIndex, msg.sender, reasonURI);
        emit MilestoneDisputed(milestoneIndex, msg.sender, reasonURI);
    }

    function resolveMilestoneDispute(uint8 milestoneIndex, uint16 workerAwardBps, string calldata resolutionURI)
        external
        nonReentrant
    {
        _requireMultiMsFunded();
        if (msg.sender != arbitrator) revert Unauthorized();
        if (milestoneIndex != currentMilestone) revert InvalidMilestoneIndex();
        if (workerAwardBps > 10_000) revert InvalidAwardBps();
        Milestone storage ms = milestones[milestoneIndex];
        if (ms.status != MilestoneStatus.Disputed) revert InvalidState();
        ms.status = MilestoneStatus.Resolved;
        ms.awardBps = workerAwardBps;
        emit MilestoneDisputeResolved(milestoneIndex, workerAwardBps, resolutionURI);
        _doMsResolvedSettle(milestoneIndex, workerAwardBps);
        _advanceMilestone();
    }

    function claimMilestoneTimeoutRefund(uint8 milestoneIndex) external nonReentrant {
        _requireMultiMsFunded();
        _requireBuyer();
        if (milestoneIndex != currentMilestone) revert InvalidMilestoneIndex();
        Milestone storage ms = milestones[milestoneIndex];
        if (ms.status != MilestoneStatus.Pending) revert InvalidState();
        if (block.timestamp <= ms.submissionDeadline) revert WindowNotOpen();
        ms.status = MilestoneStatus.Cancelled;
        emit MilestoneCancelled(milestoneIndex);
        _send(buyer, ms.amount);
        emit MilestoneSettled(milestoneIndex, 0, ms.amount, 0);
        _advanceMilestone();
    }

    function claimMilestoneArbitratorTimeout(uint8 milestoneIndex) external nonReentrant {
        _requireMultiMsFunded();
        _requireBuyer();
        if (milestoneIndex != currentMilestone) revert InvalidMilestoneIndex();
        Milestone storage ms = milestones[milestoneIndex];
        if (ms.status != MilestoneStatus.Disputed) revert InvalidState();
        if (block.timestamp <= uint256(ms.disputedAt) + uint256(arbitratorTimeoutSeconds)) {
            revert ArbitratorTimeoutNotReached();
        }
        ms.status = MilestoneStatus.Cancelled;
        emit MilestoneCancelled(milestoneIndex);
        _send(buyer, ms.amount);
        emit MilestoneSettled(milestoneIndex, 0, ms.amount, 0);
        _advanceMilestone();
    }

    function abortRemainingMilestones() external nonReentrant {
        _requireMultiMsFunded();
        _requireBuyer();

        // The most recently completed milestone must be in a terminal failure state.
        // After _advanceMilestone(), currentMilestone points to the next milestone.
        // So we check the milestone before currentMilestone, or currentMilestone itself
        // if it hasn't advanced (i.e., it's the last one).
        bool valid = false;
        if (currentMilestone > 0) {
            MilestoneStatus prev = milestones[currentMilestone - 1].status;
            if (prev == MilestoneStatus.Resolved || prev == MilestoneStatus.Cancelled) valid = true;
        }
        // Also check the current milestone itself (for the case where it's the last one
        // and _advanceMilestone couldn't advance further)
        {
            MilestoneStatus cur = milestones[currentMilestone].status;
            if (cur == MilestoneStatus.Resolved || cur == MilestoneStatus.Cancelled) valid = true;
        }
        if (!valid) revert InvalidState();

        uint256 refundTotal;
        uint8 fromIndex = currentMilestone;
        for (uint8 i = fromIndex; i < milestoneCount; i++) {
            if (milestones[i].status == MilestoneStatus.Pending) {
                milestones[i].status = MilestoneStatus.Cancelled;
                refundTotal += milestones[i].amount;
                emit MilestoneCancelled(i);
            }
        }
        if (refundTotal > 0) _send(buyer, refundTotal);
        emit RemainingMilestonesAborted(fromIndex, refundTotal);
        _settleWorkerStake();
        status = Status.Refunded;
        (, uint8 outcome) = _checkMilestonesTerminal();
        _recordOutcome(outcome);
    }

    // ── Internal ──

    function _requireMultiMsFunded() internal view {
        _requireState(Status.Funded);
        if (milestoneCount <= 1) revert InvalidState();
    }

    function _syncMs0Submit(bytes32 h, string calldata uri) internal {
        milestones[0].submissionHash = h;
        milestones[0].submissionURI = uri;
        milestones[0].submittedAt = uint64(block.timestamp);
        milestones[0].status = MilestoneStatus.Submitted;
    }

    function _syncMs0Dispute(string calldata reasonURI) internal {
        milestones[0].disputeReasonURI = reasonURI;
        milestones[0].disputedAt = uint64(block.timestamp);
        milestones[0].status = MilestoneStatus.Disputed;
    }

    function _approve(address approver) internal {
        _requireState(Status.Submitted);
        if (block.timestamp > _reviewWindowEnds()) revert WindowExpired();
        approvedAt = uint64(block.timestamp);
        status = Status.Approved;
        emit Approved(approver, approvedAt);
        if (milestoneCount == 1) {
            milestones[0].approvedAt = uint64(block.timestamp);
            milestones[0].status = MilestoneStatus.Approved;
        }
        _settleApproved();
    }

    function _approveMilestone(uint8 milestoneIndex, address approver) internal {
        _requireMultiMsFunded();
        if (milestoneIndex != currentMilestone) revert InvalidMilestoneIndex();
        Milestone storage ms = milestones[milestoneIndex];
        if (ms.status != MilestoneStatus.Submitted) revert InvalidState();
        if (block.timestamp > uint256(ms.submittedAt) + uint256(reviewPeriodSeconds)) revert WindowExpired();
        ms.approvedAt = uint64(block.timestamp);
        ms.status = MilestoneStatus.Approved;
        emit MilestoneApproved(milestoneIndex, approver, uint64(block.timestamp));
        _doMsApprovedSettle(milestoneIndex);
        _advanceMilestone();
    }

    function _settleApproved() internal {
        (uint256 wn, uint256 fee, uint256 sr) =
            MilestoneLib.settleApproved(amount, protocolFeeBpsSnapshot, workerStake, workerStaked);
        status = Status.Settled;
        _send(activeWorker, wn + sr);
        if (fee > 0) _send(treasurySnapshot, fee);
        emit Settled(wn, 0, fee, sr);
        _recordOutcome(OUTCOME_COMPLETED);
    }

    function _settleResolved(uint16 workerAwardBps) internal {
        (uint256 wn, uint256 br, uint256 fee, uint256 sr, uint256 sf) =
            MilestoneLib.settleResolved(amount, workerAwardBps, protocolFeeBpsSnapshot, workerStake, workerStaked);
        status = Status.Settled;
        if (wn + sr > 0) _send(activeWorker, wn + sr);
        if (br + sf > 0) _send(buyer, br + sf);
        if (fee > 0) _send(treasurySnapshot, fee);
        emit Settled(wn, br, fee, sr);
        _recordOutcome(OUTCOME_DISPUTED);
    }

    function _doMsApprovedSettle(uint8 idx) internal {
        MilestoneLib.SettleApprovedResult memory r =
            MilestoneLib.settleMilestoneApproved(milestones[idx].amount, protocolFeeBpsSnapshot);
        _send(activeWorker, r.workerNet);
        if (r.fee > 0) _send(treasurySnapshot, r.fee);
        emit MilestoneSettled(idx, r.workerNet, 0, r.fee);
    }

    function _doMsResolvedSettle(uint8 idx, uint16 bps) internal {
        MilestoneLib.SettleResolvedResult memory r =
            MilestoneLib.settleMilestoneResolved(milestones[idx].amount, bps, protocolFeeBpsSnapshot);
        if (r.workerNet > 0) _send(activeWorker, r.workerNet);
        if (r.buyerRefund > 0) _send(buyer, r.buyerRefund);
        if (r.fee > 0) _send(treasurySnapshot, r.fee);
        emit MilestoneSettled(idx, r.workerNet, r.buyerRefund, r.fee);
    }

    function _advanceMilestone() internal {
        if (currentMilestone + 1 < milestoneCount) currentMilestone++;
        (bool terminal, uint8 outcome) = _checkMilestonesTerminal();
        if (terminal) {
            _settleWorkerStake();
            status = Status.Settled;
            _recordOutcome(outcome);
        }
    }

    /// @dev Checks whether all milestones are terminal and determines the overall outcome
    /// in a single pass. Returns (false, 0) if any milestone is still active.
    function _checkMilestonesTerminal() internal view returns (bool, uint8) {
        bool hasApproved;
        bool hasFailed;
        for (uint8 i = 0; i < milestoneCount; i++) {
            MilestoneStatus s = milestones[i].status;
            if (s == MilestoneStatus.Approved) {
                hasApproved = true;
            } else if (s == MilestoneStatus.Resolved) {
                hasFailed = true;
                hasApproved = true; // partial -- will yield DISPUTED
            } else if (s == MilestoneStatus.Cancelled) {
                hasFailed = true;
            } else {
                return (false, 0);
            }
        }
        if (hasApproved && !hasFailed) return (true, OUTCOME_COMPLETED);
        if (hasFailed && !hasApproved) return (true, OUTCOME_FAILED);
        return (true, OUTCOME_DISPUTED);
    }

    function _settleWorkerStake() internal {
        if (!workerStaked) return;
        uint256 workerAwarded;
        uint256 total;
        for (uint8 i = 0; i < milestoneCount; i++) {
            total += milestones[i].amount;
            if (milestones[i].status == MilestoneStatus.Approved) {
                workerAwarded += milestones[i].amount;
            } else if (milestones[i].status == MilestoneStatus.Resolved) {
                workerAwarded += (milestones[i].amount * milestones[i].awardBps) / 10_000;
            }
        }
        uint256 sr;
        if (workerAwarded == total) {
            sr = workerStake;
        } else if (workerAwarded > 0) {
            sr = (workerStake * workerAwarded) / total;
        }
        uint256 sf = workerStake - sr;
        if (sr > 0) _send(activeWorker, sr);
        if (sf > 0) _send(buyer, sf);
        emit Settled(0, 0, 0, sr);
    }

    function _recordOutcome(uint8 outcome) internal {
        if (factory != address(0)) {
            (bool ok,) = factory.call(abi.encodeWithSelector(ITaskEscrowFactory.recordOutcome.selector, outcome));
            if (!ok) revert FactoryCallbackFailed();
        }
    }

    function _send(address to, uint256 value) internal {
        if (token == address(0)) {
            (bool ok,) = payable(to).call{value: value}("");
            if (!ok) revert TransferFailed();
        } else {
            _safeTransfer(IERC20(token), to, value);
        }
    }

    function _safeTransfer(IERC20 _token, address to, uint256 value) internal {
        (bool success, bytes memory data) =
            address(_token).call(abi.encodeWithSelector(_token.transfer.selector, to, value));
        if (!success || (data.length > 0 && !abi.decode(data, (bool)))) revert TransferFailed();
    }

    /// @dev Pull `value` of the escrow's ERC20 token from msg.sender via transferFrom,
    /// reverting if the received balance delta does not match (fee-on-transfer guard).
    function _receiveERC20(uint256 value) internal {
        uint256 bb = IERC20(token).balanceOf(address(this));
        _safeTransferFrom(IERC20(token), msg.sender, address(this), value);
        if (IERC20(token).balanceOf(address(this)) - bb != value) revert InsufficientReceived();
    }

    /// @dev Pull `value` via EIP-3009 receiveWithAuthorization with balance-delta guard.
    function _receiveEIP3009(
        address from,
        uint256 value,
        uint256 validAfter,
        uint256 validBefore,
        bytes32 nonce,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) internal {
        uint256 bb = IERC20(token).balanceOf(address(this));
        IEIP3009(token).receiveWithAuthorization(from, address(this), value, validAfter, validBefore, nonce, v, r, s);
        if (IERC20(token).balanceOf(address(this)) - bb != value) revert InsufficientReceived();
    }

    function _safeTransferFrom(IERC20 _token, address from, address to, uint256 value) internal {
        (bool success, bytes memory data) =
            address(_token).call(abi.encodeWithSelector(_token.transferFrom.selector, from, to, value));
        if (!success || (data.length > 0 && !abi.decode(data, (bool)))) revert TransferFailed();
    }

    function _reviewWindowEnds() internal view returns (uint256) {
        return uint256(submittedAt) + uint256(reviewPeriodSeconds);
    }

    function _disputeWindowEnds() internal view returns (uint256) {
        return uint256(submittedAt) + uint256(reviewPeriodSeconds) + uint256(disputePeriodSeconds);
    }

    function getMilestoneCount() external view returns (uint8) {
        return milestoneCount;
    }
}
