// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {MilestoneLib} from "./MilestoneLib.sol";
import {TransferLib} from "./TransferLib.sol";
import {FactoryLib} from "./FactoryLib.sol";
import {IZKVerifier} from "./IZKVerifier.sol";

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
        bytes32 proofHash;
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
    error Frozen();
    error HighAssuranceRequiresVerifier();
    error NoVerifierConfigured();
    error NoProofSubmitted();
    error ProofHashMismatch();
    error ProofVerificationFailed();
    error InvalidVerifierConfiguration();
    error InvalidQuorumConfiguration();
    error NotQuorumVerifier();
    error AlreadyVoted();
    error QuorumStakeRequired();
    error QuorumStakeAlreadyDeposited();
    error QuorumFinalized();

    event EscrowFunded(address indexed buyer, uint256 amount);
    event WorkerStakeDeposited(address indexed worker, uint256 amount);
    event SubmissionMade(address indexed worker, bytes32 submissionHash, string submissionURI, bytes32 proofHash);
    event Approved(address indexed approver, uint64 approvedAt);
    event Rejected(address indexed verifier, string reasonURI, uint64 rejectedAt);
    event Disputed(address indexed raisedBy, string reasonURI, uint64 disputedAt);
    event SilenceEscalated(address indexed worker, string reasonURI, uint64 escalatedAt);
    event DisputeResolved(address indexed arbitrator, uint16 workerAwardBps, string resolutionURI);
    event ArbitratorTimeoutClaimed(address indexed buyer, uint64 claimedAt);
    event Settled(uint256 workerNet, uint256 buyerRefund, uint256 protocolFee, uint256 workerStakeReturned);
    event Refunded(uint256 amount, uint256 workerStakeForfeited);
    event Cancelled();

    event MilestoneSubmitted(
        uint8 indexed milestoneIndex, bytes32 submissionHash, string submissionURI, bytes32 proofHash
    );
    event MilestoneApproved(uint8 indexed milestoneIndex, address indexed approver, uint64 approvedAt);
    event MilestoneRejected(uint8 indexed milestoneIndex, address indexed verifier, string reasonURI);
    event MilestoneDisputed(uint8 indexed milestoneIndex, address indexed raisedBy, string reasonURI);
    event MilestoneSilenceEscalated(uint8 indexed milestoneIndex, address indexed worker, string reasonURI);
    event MilestoneDisputeResolved(uint8 indexed milestoneIndex, uint16 workerAwardBps, string resolutionURI);
    event MilestoneSettled(uint8 indexed milestoneIndex, uint256 workerNet, uint256 buyerRefund, uint256 protocolFee);
    event MilestoneCancelled(uint8 indexed milestoneIndex);
    event RemainingMilestonesAborted(uint8 fromIndex, uint256 refundAmount);
    event BackupActivated(address indexed previousWorker, address indexed newWorker, uint64 newDeadline);
    event EmergencyFrozen();
    event EmergencyUnfrozen();
    event EmergencyResolved(uint16 workerAwardBps);
    event QuorumVoteCast(address indexed verifier, bool approve, uint8 approveCount, uint8 rejectCount);
    event QuorumReached(bool approved, uint8 approveCount, uint8 rejectCount);
    event QuorumVerifierStakeDeposited(address indexed verifier, uint256 amount);

    // Service tier constants (paper §5.3)
    uint8 public constant TIER_HIGH_ASSURANCE = 1;

    address public immutable factory;
    address public immutable token;
    address public immutable buyer;
    address public immutable worker;
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
    uint8 public immutable serviceTier;
    address public immutable backupWorker;
    uint64 public immutable backupDeadlineExtension;
    // Trusted verifier contract configured at creation. If unset, ZK verification is disabled.
    address public immutable zkVerifier;
    bytes32 public immutable circuitId;

    address public activeWorker;
    bool public backupActivated;
    bool public frozen;
    uint64 public deadlineExtensionApplied;

    // Verifier panel + quorum
    address[7] public verifierPanel;
    uint8 public quorumThreshold;
    uint8 public quorumVerifierCount;
    uint256 public verifierStakePerVerifier;

    // Vote state (for the current submission/milestone review cycle)
    mapping(address => uint8) public quorumVote; // 0=unvoted 1=approve 2=reject
    mapping(address => bool) public quorumStaked;
    // Pull-based stake settlement: amounts owed after quorum finalization or refund cycles.
    mapping(address => uint256) public withdrawable;
    uint8 public quorumApproveCount;
    uint8 public quorumRejectCount;
    uint8 public quorumStakeCount;

    uint64 public submittedAt;
    uint64 public approvedAt;
    uint64 public disputedAt;
    Status public status;
    bool public workerStaked;
    bytes32 public submissionHash;
    bytes32 public proofHash;
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
        address[7] verifierPanel;
        uint8 quorumThreshold;
        uint8 quorumVerifierCount;
        uint256 verifierStakePerVerifier;
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
        uint8 serviceTier;
        address backupWorker;
        uint64 backupDeadlineExtension;
        address zkVerifier;
        bytes32 circuitId;
        CreateMilestoneParams[] milestones;
    }

    uint8 private constant OUTCOME_COMPLETED = 1;
    uint8 private constant OUTCOME_DISPUTED = 2;
    uint8 private constant OUTCOME_FAILED = 3;

    uint256 private constant _NOT_ENTERED = 1;
    uint256 private constant _ENTERED = 2;
    uint256 private _locked = _NOT_ENTERED;

    constructor(Params memory p) {
        if (p.buyer == address(0) || p.worker == address(0) || p.arbitrator == address(0)) {
            revert InvalidAddress();
        }
        if (p.treasurySnapshot == address(0)) revert InvalidAddress();
        if (
            p.quorumThreshold == 0 || p.quorumVerifierCount == 0 || p.quorumThreshold > p.quorumVerifierCount
                || p.quorumVerifierCount > 7
        ) revert InvalidQuorumConfiguration();
        if (FactoryLib.rolesCollide(
                p.buyer, p.worker, p.arbitrator, p.backupWorker, p.verifierPanel, p.quorumVerifierCount
            )) {
            revert RolesNotDistinct();
        }
        for (uint8 i = 0; i < p.quorumVerifierCount; i++) {
            if (p.verifierPanel[i] == address(0)) revert InvalidAddress();
        }
        if (p.amount == 0) revert InvalidAmount();
        if (p.submissionDeadline <= block.timestamp) revert InvalidDeadline();
        if (p.protocolFeeBpsSnapshot > 10_000) revert InvalidAwardBps();
        bool hasZKVerifier = p.zkVerifier != address(0);
        bool hasCircuitID = p.circuitId != bytes32(0);
        if (hasZKVerifier != hasCircuitID) revert InvalidVerifierConfiguration();
        if (hasZKVerifier && p.zkVerifier.code.length == 0) revert InvalidAddress();

        factory = p.factory;
        buyer = p.buyer;
        worker = p.worker;
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
        serviceTier = p.serviceTier;
        backupWorker = p.backupWorker;
        backupDeadlineExtension = p.backupDeadlineExtension;
        zkVerifier = p.zkVerifier;
        circuitId = p.circuitId;
        activeWorker = p.worker;
        status = Status.Created;
        quorumThreshold = p.quorumThreshold;
        quorumVerifierCount = p.quorumVerifierCount;
        verifierStakePerVerifier = p.verifierStakePerVerifier;
        for (uint8 i = 0; i < p.quorumVerifierCount; i++) {
            verifierPanel[i] = p.verifierPanel[i];
        }

        _initMilestones(p);
    }

    function _initMilestones(Params memory p) internal {
        uint256 len = p.milestones.length;
        if (len > 0) {
            if (len > 16) revert TooManyMilestones();
            uint256 total;
            uint64 prevDl;
            for (uint256 i; i < len;) {
                if (p.milestones[i].submissionDeadline <= block.timestamp) revert InvalidDeadline();
                if (i > 0 && p.milestones[i].submissionDeadline <= prevDl) revert InvalidMilestoneDeadlineOrder();
                prevDl = p.milestones[i].submissionDeadline;
                total += p.milestones[i].amount;
                milestones.push(_emptyMs(p.milestones[i].amount, p.milestones[i].submissionDeadline));
                unchecked {
                    ++i;
                }
            }
            if (total != p.amount) revert MilestoneAmountMismatch();
            milestoneCount = uint8(len);
        } else {
            milestoneCount = 1;
            milestones.push(_emptyMs(p.amount, p.submissionDeadline));
        }
    }

    function _emptyMs(uint256 amt, uint64 dl) internal pure returns (Milestone memory) {
        return Milestone(amt, dl, bytes32(0), "", bytes32(0), 0, 0, 0, "", MilestoneStatus.Pending, 0);
    }

    modifier nonReentrant() {
        _nonReentrantBefore();
        _;
        _locked = _NOT_ENTERED;
    }

    modifier whenNotFrozen() {
        _whenNotFrozen();
        _;
    }

    function _whenNotFrozen() internal view {
        if (frozen) revert Frozen();
    }

    function _nonReentrantBefore() internal {
        if (_locked == _ENTERED) revert Reentrancy();
        _locked = _ENTERED;
    }

    function _requireBuyer() internal view {
        if (msg.sender != buyer) revert Unauthorized();
    }

    function setFrozen(bool f) external {
        if (msg.sender != factory) revert Unauthorized();
        frozen = f;
        if (f) emit EmergencyFrozen();
        else emit EmergencyUnfrozen();
    }

    /// @notice Emergency resolve: force-settle from any non-terminal state.
    /// Callable only by the factory. Uses dispute resolution math for proportional split.
    function emergencyResolve(uint16 bps) external nonReentrant {
        if (msg.sender != factory) revert Unauthorized();
        if (bps > 10_000) revert InvalidAwardBps();
        Status s = status;
        if (s >= Status.Settled) revert InvalidState();
        if (s == Status.Created) {
            status = Status.Cancelled;
            emit Cancelled();
            return;
        }
        emit EmergencyResolved(bps);
        if (milestoneCount <= 1) _settleResolved(bps);
        else _emergencySettleMilestones(bps);
    }

    function _requireState(Status s) internal view {
        if (status != s) revert InvalidState();
    }

    // ── V1 single-shot functions ──

    function fund() external payable nonReentrant whenNotFrozen {
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
    ) external nonReentrant whenNotFrozen {
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

    function depositStake() external payable nonReentrant whenNotFrozen {
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

    function submit(bytes32 _submissionHash, string calldata _submissionURI, bytes32 _proofHash)
        external
        whenNotFrozen
    {
        if (msg.sender != activeWorker) revert Unauthorized();
        _requireState(Status.Funded);
        if (milestoneCount > 1) revert InvalidState();
        if (block.timestamp > uint256(submissionDeadline) + uint256(deadlineExtensionApplied)) revert WindowExpired();
        if (_submissionHash == bytes32(0)) revert InvalidHash();
        if (workerStake > 0 && !workerStaked) revert StakeNotDeposited();
        submissionHash = _submissionHash;
        proofHash = _proofHash;
        submissionURI = _submissionURI;
        submittedAt = uint64(block.timestamp);
        status = Status.Submitted;
        emit SubmissionMade(msg.sender, _submissionHash, _submissionURI, _proofHash);
        _syncMs0Submit(_submissionHash, _submissionURI, _proofHash);
    }

    function depositVerifierStake() external payable nonReentrant whenNotFrozen {
        if (!_isQuorumVerifier(msg.sender)) revert NotQuorumVerifier();
        if (verifierStakePerVerifier == 0) revert InvalidAmount();
        if (quorumStaked[msg.sender]) revert QuorumStakeAlreadyDeposited();

        Status s = status;
        if (s != Status.Funded && s != Status.Submitted) revert InvalidState();

        if (token == address(0)) {
            if (msg.value != verifierStakePerVerifier) revert InvalidAmount();
        } else {
            if (msg.value != 0) revert ETHNotAccepted();
            _receiveERC20(verifierStakePerVerifier);
        }

        quorumStaked[msg.sender] = true;
        quorumStakeCount++;
        emit QuorumVerifierStakeDeposited(msg.sender, verifierStakePerVerifier);
    }

    /// @notice Withdraw verifier stake owed to the caller after quorum settlement or refund.
    /// @dev CEI ordering (zero before send) prevents reentrancy without the nonReentrant modifier.
    function withdrawStake() external {
        uint256 owed = withdrawable[msg.sender];
        if (owed == 0) revert InvalidAmount();
        withdrawable[msg.sender] = 0;
        _send(msg.sender, owed);
    }

    function verifyAndApprove(bytes calldata proof) external nonReentrant whenNotFrozen {
        if (!_isQuorumVerifier(msg.sender)) revert NotQuorumVerifier();
        if (zkVerifier == address(0)) revert NoVerifierConfigured();
        if (proofHash == bytes32(0)) revert NoProofSubmitted();
        if (keccak256(proof) != proofHash) revert ProofHashMismatch();
        // Trust model: zkVerifier is immutable and validated as a deployed contract in the constructor.
        bool ok = IZKVerifier(zkVerifier).verifyProof(circuitId, proof);
        if (!ok) revert ProofVerificationFailed();
        _castSingleVote(msg.sender, true, "", verifierStakePerVerifier > 0);
    }

    function approveByBuyer() external nonReentrant whenNotFrozen {
        _requireBuyer();
        if (serviceTier == TIER_HIGH_ASSURANCE) revert HighAssuranceRequiresVerifier();
        _refundUnsettledVerifierStakes();
        _resetQuorumVoteState();
        _approve(msg.sender);
    }

    function castVerifierVote(bool approve, string calldata reasonURI) external nonReentrant whenNotFrozen {
        _castSingleVote(msg.sender, approve, reasonURI, true);
    }

    function dispute(string calldata reasonURI) external nonReentrant whenNotFrozen {
        _requireBuyer();
        _requireState(Status.Submitted);
        if (block.timestamp > _disputeWindowEnds()) revert WindowExpired();
        _refundUnsettledVerifierStakes();
        _resetQuorumVoteState();
        _setSingleDisputed(msg.sender, reasonURI, false);
    }

    function escalateSilence(string calldata reasonURI) external nonReentrant whenNotFrozen {
        if (msg.sender != activeWorker) revert Unauthorized();
        _requireState(Status.Submitted);
        if (block.timestamp <= _reviewWindowEnds()) revert WindowNotOpen();
        if (block.timestamp > _disputeWindowEnds()) revert WindowExpired();
        _refundUnsettledVerifierStakes();
        _resetQuorumVoteState();
        _setSingleDisputed(msg.sender, reasonURI, true);
    }

    /// @notice Advances an expired single-shot review cycle into dispute when quorum did not finalize in time.
    function expireNoQuorum(string calldata reasonURI) external nonReentrant whenNotFrozen {
        _requireState(Status.Submitted);
        if (!_isLifecycleParticipant(msg.sender)) revert Unauthorized();
        if (block.timestamp <= _disputeWindowEnds()) revert WindowNotOpen();
        _refundUnsettledVerifierStakes();
        _resetQuorumVoteState();
        _setSingleDisputed(msg.sender, reasonURI, false);
    }

    function resolveDispute(uint16 workerAwardBps, string calldata resolutionURI) external nonReentrant whenNotFrozen {
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
        _refundUnsettledVerifierStakes();
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
        _refundUnsettledVerifierStakes();
        uint256 sf = workerStaked ? workerStake : 0;
        status = Status.Refunded;
        if (milestoneCount == 1) milestones[0].status = MilestoneStatus.Cancelled;
        _send(buyer, amount + sf);
        emit ArbitratorTimeoutClaimed(msg.sender, uint64(block.timestamp));
        emit Refunded(amount, sf);
        _recordOutcome(OUTCOME_DISPUTED);
    }

    // ── Backup agent activation (paper §4.4) ──

    function activateBackup() external nonReentrant whenNotFrozen {
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

    function submitMilestone(
        uint8 milestoneIndex,
        bytes32 _submissionHash,
        string calldata _submissionURI,
        bytes32 _proofHash
    ) external whenNotFrozen {
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
        ms.proofHash = _proofHash;
        ms.submittedAt = uint64(block.timestamp);
        ms.status = MilestoneStatus.Submitted;
        emit MilestoneSubmitted(milestoneIndex, _submissionHash, _submissionURI, _proofHash);
    }

    function verifyAndApproveMilestone(uint8 milestoneIndex, bytes calldata proof) external nonReentrant whenNotFrozen {
        _requireMultiMsFunded();
        if (!_isQuorumVerifier(msg.sender)) revert NotQuorumVerifier();
        if (zkVerifier == address(0)) revert NoVerifierConfigured();
        if (milestoneIndex != currentMilestone) revert InvalidMilestoneIndex();
        Milestone storage ms = milestones[milestoneIndex];
        if (ms.proofHash == bytes32(0)) revert NoProofSubmitted();
        if (keccak256(proof) != ms.proofHash) revert ProofHashMismatch();
        // Trust model: zkVerifier is immutable and validated as a deployed contract in the constructor.
        bool ok = IZKVerifier(zkVerifier).verifyProof(circuitId, proof);
        if (!ok) revert ProofVerificationFailed();
        _castMilestoneVote(msg.sender, milestoneIndex, true, "", verifierStakePerVerifier > 0);
    }

    function approveMilestoneByBuyer(uint8 milestoneIndex) external nonReentrant whenNotFrozen {
        _requireBuyer();
        if (serviceTier == TIER_HIGH_ASSURANCE) revert HighAssuranceRequiresVerifier();
        _refundUnsettledVerifierStakes();
        _resetQuorumVoteState();
        _approveMilestone(milestoneIndex, msg.sender);
    }

    function disputeMilestone(uint8 milestoneIndex, string calldata reasonURI) external nonReentrant whenNotFrozen {
        _requireMultiMsFunded();
        _requireBuyer();
        if (milestoneIndex != currentMilestone) revert InvalidMilestoneIndex();
        Milestone storage ms = milestones[milestoneIndex];
        if (ms.status != MilestoneStatus.Submitted) revert InvalidState();
        if (block.timestamp > uint256(ms.submittedAt) + uint256(reviewPeriodSeconds) + uint256(disputePeriodSeconds)) {
            revert WindowExpired();
        }
        _refundUnsettledVerifierStakes();
        _resetQuorumVoteState();
        _setMilestoneDisputed(milestoneIndex, msg.sender, reasonURI, false);
    }

    function castMilestoneVerifierVote(uint8 milestoneIndex, bool approve, string calldata reasonURI)
        external
        nonReentrant
        whenNotFrozen
    {
        _castMilestoneVote(msg.sender, milestoneIndex, approve, reasonURI, true);
    }

    function escalateMilestoneSilence(uint8 milestoneIndex, string calldata reasonURI)
        external
        nonReentrant
        whenNotFrozen
    {
        _requireMultiMsFunded();
        if (msg.sender != activeWorker) revert Unauthorized();
        if (milestoneIndex != currentMilestone) revert InvalidMilestoneIndex();
        Milestone storage ms = milestones[milestoneIndex];
        if (ms.status != MilestoneStatus.Submitted) revert InvalidState();
        uint256 reviewEnd = uint256(ms.submittedAt) + uint256(reviewPeriodSeconds);
        if (block.timestamp <= reviewEnd) revert WindowNotOpen();
        if (block.timestamp > reviewEnd + uint256(disputePeriodSeconds)) revert WindowExpired();
        _refundUnsettledVerifierStakes();
        _resetQuorumVoteState();
        _setMilestoneDisputed(milestoneIndex, msg.sender, reasonURI, true);
    }

    /// @notice Advances an expired milestone review cycle into dispute when quorum did not finalize in time.
    function expireMilestoneNoQuorum(uint8 milestoneIndex, string calldata reasonURI)
        external
        nonReentrant
        whenNotFrozen
    {
        _requireMultiMsFunded();
        if (!_isLifecycleParticipant(msg.sender)) revert Unauthorized();
        if (milestoneIndex != currentMilestone) revert InvalidMilestoneIndex();
        Milestone storage ms = milestones[milestoneIndex];
        if (ms.status != MilestoneStatus.Submitted) revert InvalidState();
        uint256 disputeEnd = uint256(ms.submittedAt) + uint256(reviewPeriodSeconds) + uint256(disputePeriodSeconds);
        if (block.timestamp <= disputeEnd) revert WindowNotOpen();
        _refundUnsettledVerifierStakes();
        _resetQuorumVoteState();
        _setMilestoneDisputed(milestoneIndex, msg.sender, reasonURI, false);
    }

    function resolveMilestoneDispute(uint8 milestoneIndex, uint16 workerAwardBps, string calldata resolutionURI)
        external
        nonReentrant
        whenNotFrozen
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
        _refundUnsettledVerifierStakes();
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
        _refundUnsettledVerifierStakes();
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
        _refundUnsettledVerifierStakes();
        _advanceMilestone();
    }

    function abortRemainingMilestones() external nonReentrant whenNotFrozen {
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
        _refundUnsettledVerifierStakes();
        _settleWorkerStake();
        status = Status.Refunded;
        (, uint8 outcome) = _checkMilestonesTerminal();
        _recordOutcome(outcome);
    }

    // ── Internal ──

    function _emergencySettleMilestones(uint16 bps) internal {
        _refundUnsettledVerifierStakes();
        uint8 mc = milestoneCount;
        for (uint8 i; i < mc;) {
            MilestoneStatus ms = milestones[i].status;
            if (ms < MilestoneStatus.Approved || ms == MilestoneStatus.Disputed) {
                milestones[i].status = MilestoneStatus.Resolved;
                milestones[i].awardBps = bps;
                _doMsResolvedSettle(i, bps);
            }
            unchecked {
                ++i;
            }
        }
        _settleWorkerStake();
        status = Status.Settled;
        _recordOutcome(OUTCOME_DISPUTED);
    }

    function _requireMultiMsFunded() internal view {
        _requireState(Status.Funded);
        if (milestoneCount <= 1) revert InvalidState();
    }

    function _syncMs0Submit(bytes32 h, string calldata uri, bytes32 pHash) internal {
        milestones[0].submissionHash = h;
        milestones[0].submissionURI = uri;
        milestones[0].proofHash = pHash;
        milestones[0].submittedAt = uint64(block.timestamp);
        milestones[0].status = MilestoneStatus.Submitted;
    }

    function _syncMs0Dispute(string memory reasonURI) internal {
        milestones[0].disputeReasonURI = reasonURI;
        milestones[0].disputedAt = uint64(block.timestamp);
        milestones[0].status = MilestoneStatus.Disputed;
    }

    /// @dev Validates voter eligibility, records the vote, and emits QuorumVoteCast.
    /// Shared by single-shot and milestone voting paths to reduce bytecode duplication.
    function _recordQuorumVote(address voter, bool approve, bool enforceStake) internal {
        if (!_isQuorumVerifier(voter)) revert NotQuorumVerifier();
        if (quorumVote[voter] != 0) revert AlreadyVoted();
        if (enforceStake && verifierStakePerVerifier > 0 && !quorumStaked[voter]) revert QuorumStakeRequired();
        quorumVote[voter] = approve ? 1 : 2;
        if (approve) {
            quorumApproveCount++;
        } else {
            quorumRejectCount++;
        }
        emit QuorumVoteCast(voter, approve, quorumApproveCount, quorumRejectCount);
    }

    function _castSingleVote(address voter, bool approve, string memory reasonURI, bool enforceStake) internal {
        Status s = status;
        if (s != Status.Submitted) {
            if (s > Status.Submitted) revert QuorumFinalized();
            revert InvalidState();
        }
        if (block.timestamp > _reviewWindowEnds()) revert WindowExpired();
        _recordQuorumVote(voter, approve, enforceStake);

        if (quorumApproveCount >= quorumThreshold) {
            _quorumApprove();
            return;
        }
        if (quorumRejectCount >= _quorumRejectThreshold()) {
            _quorumReject(voter, reasonURI);
        }
    }

    function _castMilestoneVote(
        address voter,
        uint8 milestoneIndex,
        bool approve,
        string memory reasonURI,
        bool enforceStake
    ) internal {
        _requireMultiMsFunded();
        if (milestoneIndex != currentMilestone) revert InvalidMilestoneIndex();
        Milestone storage ms = milestones[milestoneIndex];
        if (ms.status != MilestoneStatus.Submitted) {
            if (uint8(ms.status) > uint8(MilestoneStatus.Submitted)) revert QuorumFinalized();
            revert InvalidState();
        }
        if (block.timestamp > uint256(ms.submittedAt) + uint256(reviewPeriodSeconds)) revert WindowExpired();
        _recordQuorumVote(voter, approve, enforceStake);

        if (quorumApproveCount >= quorumThreshold) {
            emit QuorumReached(true, quorumApproveCount, quorumRejectCount);
            _settleVerifierStakes(true);
            _approveMilestone(milestoneIndex, address(0));
            _resetQuorumVoteState();
            return;
        }
        if (quorumRejectCount >= _quorumRejectThreshold()) {
            emit QuorumReached(false, quorumApproveCount, quorumRejectCount);
            _settleVerifierStakes(false);
            _setMilestoneDisputed(milestoneIndex, voter, reasonURI, false);
            _resetQuorumVoteState();
        }
    }

    function _quorumApprove() internal {
        emit QuorumReached(true, quorumApproveCount, quorumRejectCount);
        _settleVerifierStakes(true);
        _approve(address(0));
    }

    function _quorumReject(address voter, string memory reasonURI) internal {
        emit QuorumReached(false, quorumApproveCount, quorumRejectCount);
        _settleVerifierStakes(false);
        _setSingleDisputed(voter, reasonURI, false);
    }

    function _setSingleDisputed(address raisedBy, string memory reasonURI, bool emitSilence) internal {
        uint64 ts = uint64(block.timestamp);
        disputeReasonURI = reasonURI;
        disputedAt = ts;
        status = Status.Disputed;
        if (emitSilence) emit SilenceEscalated(raisedBy, reasonURI, ts);
        emit Disputed(raisedBy, reasonURI, ts);
        if (milestoneCount == 1) _syncMs0Dispute(reasonURI);
    }

    function _setMilestoneDisputed(uint8 milestoneIndex, address raisedBy, string memory reasonURI, bool emitSilence)
        internal
    {
        Milestone storage ms = milestones[milestoneIndex];
        ms.disputeReasonURI = reasonURI;
        ms.disputedAt = uint64(block.timestamp);
        ms.status = MilestoneStatus.Disputed;
        if (emitSilence) emit MilestoneSilenceEscalated(milestoneIndex, raisedBy, reasonURI);
        emit MilestoneDisputed(milestoneIndex, raisedBy, reasonURI);
    }

    function _settleVerifierStakes(bool approvalMajority) internal {
        if (verifierStakePerVerifier == 0) return;

        for (uint8 i = 0; i < quorumVerifierCount; i++) {
            address panelVerifier = verifierPanel[i];
            if (!quorumStaked[panelVerifier]) continue;

            quorumStaked[panelVerifier] = false;
            if (quorumStakeCount > 0) quorumStakeCount--;

            uint8 vote = quorumVote[panelVerifier];
            if (vote == 0) {
                withdrawable[panelVerifier] += verifierStakePerVerifier; // refund abstainers
                continue;
            }

            bool inMajority = approvalMajority ? vote == 1 : vote == 2;
            if (inMajority) withdrawable[panelVerifier] += verifierStakePerVerifier;
            else withdrawable[buyer] += verifierStakePerVerifier;
        }
    }

    /// @dev Refunds all currently deposited verifier stakes when a review cycle exits without quorum finalization.
    function _refundUnsettledVerifierStakes() internal {
        if (verifierStakePerVerifier == 0 || quorumStakeCount == 0) return;

        for (uint8 i = 0; i < quorumVerifierCount; i++) {
            address panelVerifier = verifierPanel[i];
            if (!quorumStaked[panelVerifier]) continue;
            quorumStaked[panelVerifier] = false;
            if (quorumStakeCount > 0) quorumStakeCount--;
            withdrawable[panelVerifier] += verifierStakePerVerifier;
        }
    }

    function _resetQuorumVoteState() internal {
        for (uint8 i = 0; i < quorumVerifierCount; i++) {
            address panelVerifier = verifierPanel[i];
            if (quorumVote[panelVerifier] != 0) quorumVote[panelVerifier] = 0;
        }
        quorumApproveCount = 0;
        quorumRejectCount = 0;
    }

    function _quorumRejectThreshold() internal view returns (uint8) {
        return quorumVerifierCount - quorumThreshold + 1;
    }

    function _isQuorumVerifier(address candidate) internal view returns (bool) {
        for (uint8 i = 0; i < quorumVerifierCount; i++) {
            if (verifierPanel[i] == candidate) return true;
        }
        return false;
    }

    function _isLifecycleParticipant(address candidate) internal view returns (bool) {
        return candidate == buyer || candidate == activeWorker || _isQuorumVerifier(candidate);
    }

    function getQuorumPanel() external view returns (address[] memory panel) {
        panel = new address[](quorumVerifierCount);
        for (uint8 i = 0; i < quorumVerifierCount; i++) {
            panel[i] = verifierPanel[i];
        }
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
        _refundUnsettledVerifierStakes();
        _advanceMilestone();
    }

    function _settleApproved() internal {
        (uint256 wn, uint256 fee, uint256 sr) =
            MilestoneLib.settleApproved(amount, protocolFeeBpsSnapshot, workerStake, workerStaked);
        _refundUnsettledVerifierStakes();
        status = Status.Settled;
        _send(activeWorker, wn + sr);
        if (fee > 0) _send(treasurySnapshot, fee);
        emit Settled(wn, 0, fee, sr);
        _recordOutcome(OUTCOME_COMPLETED);
    }

    function _settleResolved(uint16 workerAwardBps) internal {
        (uint256 wn, uint256 br, uint256 fee, uint256 sr, uint256 sf) =
            MilestoneLib.settleResolved(amount, workerAwardBps, protocolFeeBpsSnapshot, workerStake, workerStaked);
        _refundUnsettledVerifierStakes();
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
        TransferLib.send(token, to, value);
    }

    function _receiveERC20(uint256 value) internal {
        TransferLib.receiveERC20(token, value);
    }

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
        TransferLib.receiveEIP3009(token, from, value, validAfter, validBefore, nonce, v, r, s);
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
