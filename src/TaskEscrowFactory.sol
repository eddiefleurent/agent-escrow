// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {TaskEscrow} from "./TaskEscrow.sol";
import {EscrowDeployer} from "./EscrowDeployer.sol";
import {FactoryLib} from "./FactoryLib.sol";

contract TaskEscrowFactory {
    error Unauthorized();
    error InvalidAddress();
    error InvalidFeeBps();
    error InvalidAmount();
    error InvalidDeadline();
    error BelowComplexityFloor();
    error Paused();
    error NoPendingTransfer();
    error NotRegisteredEscrow();
    error InvalidOutcome();
    error FrozenAddress();
    error EscrowNotFrozen();
    error InvalidServiceTier();
    error InvalidQuorumConfiguration();

    // Service tier constants (paper §5.3: tiered service levels)
    uint8 public constant TIER_LOW_ASSURANCE = 0;
    uint8 public constant TIER_HIGH_ASSURANCE = 1;

    event EscrowCreated(
        uint256 indexed escrowId,
        address indexed escrow,
        address indexed buyer,
        address worker,
        uint8 quorumThreshold,
        uint8 quorumVerifierCount,
        address arbitrator,
        bytes32 taskSpecHash,
        address token,
        uint8 serviceTier,
        address zkVerifier,
        bytes32 circuitId
    );
    event ProtocolFeeUpdated(uint16 oldFeeBps, uint16 newFeeBps);
    event HighAssuranceFeeUpdated(uint16 oldFeeBps, uint16 newFeeBps);
    event RedelegationSurchargePolicyUpdated(
        uint16 oldSurchargeStepBps,
        uint16 newSurchargeStepBps,
        uint16 oldMaxSurchargeBps,
        uint16 newMaxSurchargeBps,
        uint64 oldFrequencyWindowSeconds,
        uint64 newFrequencyWindowSeconds
    );
    event TreasuryUpdated(address oldTreasury, address newTreasury);
    event OwnershipTransferStarted(address indexed previousOwner, address indexed newOwner);
    event OwnershipTransferred(address indexed previousOwner, address indexed newOwner);
    event BackupDesignated(uint256 indexed escrowId, address indexed backupWorker, uint64 backupDeadlineExtension);
    event ComplexityFloorUpdated(uint256 oldFloor, uint256 newFloor);
    event FactoryPaused();
    event FactoryUnpaused();
    event OutcomeRecorded(uint256 indexed escrowId, address indexed participant, string role, string outcome);
    event AddressFrozen(address indexed target);
    event AddressUnfrozen(address indexed target);
    event EscrowFrozen(uint256 indexed escrowId);
    event EscrowUnfrozen(uint256 indexed escrowId);
    event EmergencyResolved(uint256 indexed escrowId, uint16 workerAwardBps);
    event MarketStabilityFeeApplied(
        uint256 indexed escrowId,
        address indexed parentEscrow,
        uint16 baseFeeBps,
        uint16 surchargeBps,
        uint16 feeSnapshotBps,
        uint16 redelegationStreak
    );

    // Outcome enum values passed by escrow contracts via recordOutcome().
    uint8 public constant OUTCOME_COMPLETED = 1;
    uint8 public constant OUTCOME_DISPUTED = 2;
    uint8 public constant OUTCOME_FAILED = 3;

    struct ReputationRecord {
        uint32 completed;
        uint32 disputed;
        uint32 failed;
    }

    struct RedelegationFeeState {
        uint64 lastRedelegationAt;
        uint16 streak;
    }

    struct FeeComputation {
        uint16 baseFeeBps;
        uint16 surchargeBps;
        uint16 feeSnapshotBps;
        uint16 redelegationStreak;
    }

    uint256 public nextEscrowId;
    mapping(uint256 => address) public escrowById;
    uint16 public protocolFeeBps;
    uint16 public highAssuranceFeeBps;
    uint16 public redelegationSurchargeStepBps;
    uint16 public redelegationMaxSurchargeBps;
    uint64 public redelegationFrequencyWindowSeconds;
    uint256 public complexityFloor;
    address public treasury;
    address public owner;
    address public pendingOwner;
    bool public paused;
    EscrowDeployer public immutable deployer;

    mapping(address => ReputationRecord) public workerReputation;
    mapping(address => ReputationRecord) public buyerReputation;
    mapping(address => uint256) internal escrowToId;
    mapping(uint256 => address) internal escrowBuyer;
    mapping(uint256 => address) internal escrowWorker;
    mapping(address => bool) public frozenAddresses;
    mapping(address => RedelegationFeeState) public redelegationFeeState;

    constructor(uint16 _protocolFeeBps, uint16 _highAssuranceFeeBps, address _treasury, address _owner) {
        if (_protocolFeeBps > 10_000) revert InvalidFeeBps();
        if (_highAssuranceFeeBps > 10_000) revert InvalidFeeBps();
        if (_highAssuranceFeeBps < _protocolFeeBps) revert InvalidFeeBps();
        if (_treasury == address(0) || _owner == address(0)) revert InvalidAddress();
        protocolFeeBps = _protocolFeeBps;
        highAssuranceFeeBps = _highAssuranceFeeBps;
        treasury = _treasury;
        owner = _owner;
        deployer = new EscrowDeployer();
    }

    modifier onlyOwner() {
        if (msg.sender != owner) revert Unauthorized();
        _;
    }

    modifier whenNotPaused() {
        if (paused) revert Paused();
        _;
    }

    struct CreateMilestoneParams {
        uint256 amount;
        uint64 submissionDeadline;
    }

    struct CreateParams {
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
        uint64 arbitratorTimeoutSeconds;
        address token;
        uint8 serviceTier;
        address backupWorker;
        uint64 backupDeadlineExtension;
        address zkVerifier;
        bytes32 circuitId;
        address parentEscrow;
        CreateMilestoneParams[] milestones;
    }

    function createEscrow(CreateParams calldata p) external whenNotPaused returns (uint256 escrowId, address escrow) {
        if (p.amount == 0) revert InvalidAmount();
        if (complexityFloor > 0 && p.amount < complexityFloor) revert BelowComplexityFloor();
        if (p.submissionDeadline <= block.timestamp) revert InvalidDeadline();
        if (p.serviceTier > TIER_HIGH_ASSURANCE) revert InvalidServiceTier();
        if (
            p.quorumThreshold == 0 || p.quorumVerifierCount == 0 || p.quorumVerifierCount > 7
                || p.quorumThreshold > p.quorumVerifierCount
        ) revert InvalidQuorumConfiguration();
        if (
            frozenAddresses[p.buyer] || frozenAddresses[p.worker] || frozenAddresses[p.arbitrator]
                || (p.backupWorker != address(0) && frozenAddresses[p.backupWorker])
        ) revert FrozenAddress();
        for (uint8 i = 0; i < p.quorumVerifierCount; i++) {
            address panelVerifier = p.verifierPanel[i];
            if (panelVerifier == address(0)) revert InvalidAddress();
            if (frozenAddresses[panelVerifier]) revert FrozenAddress();
        }
        if (FactoryLib.rolesCollide(
                p.buyer, p.worker, p.arbitrator, p.backupWorker, p.verifierPanel, p.quorumVerifierCount
            )) revert TaskEscrow.RolesNotDistinct();
        if (p.parentEscrow != address(0)) {
            if (escrowToId[p.parentEscrow] == 0) revert NotRegisteredEscrow();
            if (p.buyer != TaskEscrow(p.parentEscrow).activeWorker()) revert Unauthorized();
        }

        FeeComputation memory fee = _computeFeeSnapshot(p.serviceTier, p.parentEscrow);

        TaskEscrow.CreateMilestoneParams[] memory escrowMilestones = _toEscrowMilestones(p.milestones);

        escrow = deployer.deploy(
            TaskEscrow.Params({
                factory: address(this),
                buyer: p.buyer,
                worker: p.worker,
                verifierPanel: p.verifierPanel,
                quorumThreshold: p.quorumThreshold,
                quorumVerifierCount: p.quorumVerifierCount,
                verifierStakePerVerifier: p.verifierStakePerVerifier,
                arbitrator: p.arbitrator,
                amount: p.amount,
                workerStake: p.workerStake,
                submissionDeadline: p.submissionDeadline,
                reviewPeriodSeconds: p.reviewPeriodSeconds,
                disputePeriodSeconds: p.disputePeriodSeconds,
                taskSpecHash: p.taskSpecHash,
                protocolFeeBpsSnapshot: fee.feeSnapshotBps,
                treasurySnapshot: treasury,
                arbitratorTimeoutSeconds: p.arbitratorTimeoutSeconds,
                token: p.token,
                serviceTier: p.serviceTier,
                backupWorker: p.backupWorker,
                backupDeadlineExtension: p.backupDeadlineExtension,
                zkVerifier: p.zkVerifier,
                circuitId: p.circuitId,
                milestones: escrowMilestones
            })
        );

        escrowId = nextEscrowId++;
        escrowById[escrowId] = escrow;
        escrowToId[escrow] = escrowId + 1; // +1 so 0 means "not registered"
        escrowBuyer[escrowId] = p.buyer;
        escrowWorker[escrowId] = p.worker;

        emit EscrowCreated(
            escrowId,
            escrow,
            p.buyer,
            p.worker,
            p.quorumThreshold,
            p.quorumVerifierCount,
            p.arbitrator,
            p.taskSpecHash,
            p.token,
            p.serviceTier,
            p.zkVerifier,
            p.circuitId
        );

        if (p.parentEscrow != address(0)) {
            emit MarketStabilityFeeApplied(
                escrowId, p.parentEscrow, fee.baseFeeBps, fee.surchargeBps, fee.feeSnapshotBps, fee.redelegationStreak
            );
        }

        if (p.backupWorker != address(0)) {
            emit BackupDesignated(escrowId, p.backupWorker, p.backupDeadlineExtension);
        }
    }

    function setProtocolFeeBps(uint16 newFeeBps) external onlyOwner {
        if (newFeeBps > 10_000) revert InvalidFeeBps();
        if (newFeeBps > highAssuranceFeeBps) revert InvalidFeeBps();
        if (uint256(newFeeBps) + uint256(redelegationMaxSurchargeBps) > 10_000) revert InvalidFeeBps();
        uint16 oldFee = protocolFeeBps;
        protocolFeeBps = newFeeBps;
        emit ProtocolFeeUpdated(oldFee, newFeeBps);
    }

    function setHighAssuranceFeeBps(uint16 newFeeBps) external onlyOwner {
        if (newFeeBps > 10_000) revert InvalidFeeBps();
        if (newFeeBps < protocolFeeBps) revert InvalidFeeBps();
        if (uint256(newFeeBps) + uint256(redelegationMaxSurchargeBps) > 10_000) revert InvalidFeeBps();
        uint16 oldFee = highAssuranceFeeBps;
        highAssuranceFeeBps = newFeeBps;
        emit HighAssuranceFeeUpdated(oldFee, newFeeBps);
    }

    function setRedelegationSurchargePolicy(
        uint16 newSurchargeStepBps,
        uint16 newMaxSurchargeBps,
        uint64 newFrequencyWindowSeconds
    ) external onlyOwner {
        if (newSurchargeStepBps > 10_000 || newMaxSurchargeBps > 10_000) {
            revert InvalidFeeBps();
        }
        if (uint256(highAssuranceFeeBps) + uint256(newMaxSurchargeBps) > 10_000) revert InvalidFeeBps();

        uint16 oldSurchargeStepBps = redelegationSurchargeStepBps;
        uint16 oldMaxSurchargeBps = redelegationMaxSurchargeBps;
        uint64 oldFrequencyWindowSeconds = redelegationFrequencyWindowSeconds;

        redelegationSurchargeStepBps = newSurchargeStepBps;
        redelegationMaxSurchargeBps = newMaxSurchargeBps;
        redelegationFrequencyWindowSeconds = newFrequencyWindowSeconds;

        emit RedelegationSurchargePolicyUpdated(
            oldSurchargeStepBps,
            newSurchargeStepBps,
            oldMaxSurchargeBps,
            newMaxSurchargeBps,
            oldFrequencyWindowSeconds,
            newFrequencyWindowSeconds
        );
    }

    function setComplexityFloor(uint256 newFloor) external onlyOwner {
        uint256 oldFloor = complexityFloor;
        complexityFloor = newFloor;
        emit ComplexityFloorUpdated(oldFloor, newFloor);
    }

    function setTreasury(address newTreasury) external onlyOwner {
        if (newTreasury == address(0)) revert InvalidAddress();
        address oldTreasury = treasury;
        treasury = newTreasury;
        emit TreasuryUpdated(oldTreasury, newTreasury);
    }

    function setPaused(bool shouldPause) external onlyOwner {
        paused = shouldPause;
        if (shouldPause) emit FactoryPaused();
        else emit FactoryUnpaused();
    }

    function transferOwnership(address newOwner) external onlyOwner {
        if (newOwner == address(0)) revert InvalidAddress();
        pendingOwner = newOwner;
        emit OwnershipTransferStarted(owner, newOwner);
    }

    function acceptOwnership() external {
        if (msg.sender != pendingOwner) revert Unauthorized();
        if (pendingOwner == address(0)) revert NoPendingTransfer();
        address oldOwner = owner;
        owner = pendingOwner;
        pendingOwner = address(0);
        emit OwnershipTransferred(oldOwner, msg.sender);
    }

    /// @notice Called by a registered escrow contract on terminal state transitions.
    /// @param outcome 1 = Completed, 2 = Disputed, 3 = Failed
    function recordOutcome(uint8 outcome) external {
        uint256 packed = escrowToId[msg.sender];
        if (packed == 0) revert NotRegisteredEscrow();
        if (outcome < OUTCOME_COMPLETED || outcome > OUTCOME_FAILED) revert InvalidOutcome();

        uint256 escrowId = packed - 1;
        address buyerAddr = escrowBuyer[escrowId];
        address workerAddr = escrowWorker[escrowId];

        // The escrow may have activated a backup worker. Read the active worker
        // from the escrow contract so reputation is attributed correctly.
        address activeWorkerAddr = TaskEscrow(msg.sender).activeWorker();

        string memory outcomeStr;
        if (outcome == OUTCOME_COMPLETED) {
            outcomeStr = "completed";
            workerReputation[activeWorkerAddr].completed++;
            buyerReputation[buyerAddr].completed++;
        } else if (outcome == OUTCOME_DISPUTED) {
            outcomeStr = "disputed";
            workerReputation[activeWorkerAddr].disputed++;
            buyerReputation[buyerAddr].disputed++;
        } else {
            outcomeStr = "failed";
            workerReputation[activeWorkerAddr].failed++;
            buyerReputation[buyerAddr].failed++;
        }

        emit OutcomeRecorded(escrowId, activeWorkerAddr, "worker", outcomeStr);
        emit OutcomeRecorded(escrowId, buyerAddr, "buyer", outcomeStr);

        // If the active worker differs from the original, also record failure for
        // the original worker (they defaulted, triggering backup activation).
        if (activeWorkerAddr != workerAddr) {
            workerReputation[workerAddr].failed++;
            emit OutcomeRecorded(escrowId, workerAddr, "worker", "failed");
        }
    }

    // ── Emergency response protocol (paper §4.9) ──

    function freezeAddress(address target) external onlyOwner {
        if (target == address(0)) revert InvalidAddress();
        frozenAddresses[target] = true;
        emit AddressFrozen(target);
    }

    function unfreezeAddress(address target) external onlyOwner {
        if (target == address(0)) revert InvalidAddress();
        frozenAddresses[target] = false;
        emit AddressUnfrozen(target);
    }

    function freezeEscrow(uint256 escrowId) external onlyOwner {
        address escrow = escrowById[escrowId];
        if (escrow == address(0)) revert InvalidAddress();
        TaskEscrow(escrow).setFrozen(true);
        emit EscrowFrozen(escrowId);
    }

    function unfreezeEscrow(uint256 escrowId) external onlyOwner {
        address escrow = escrowById[escrowId];
        if (escrow == address(0)) revert InvalidAddress();
        TaskEscrow(escrow).setFrozen(false);
        emit EscrowUnfrozen(escrowId);
    }

    function emergencyResolve(uint256 escrowId, uint16 workerAwardBps) external onlyOwner {
        address escrow = escrowById[escrowId];
        if (escrow == address(0)) revert InvalidAddress();
        if (!TaskEscrow(escrow).frozen()) revert EscrowNotFrozen();
        TaskEscrow(escrow).emergencyResolve(workerAwardBps);
        emit EmergencyResolved(escrowId, workerAwardBps);
    }

    function _toEscrowMilestones(CreateMilestoneParams[] calldata milestones)
        internal
        pure
        returns (TaskEscrow.CreateMilestoneParams[] memory escrowMilestones)
    {
        escrowMilestones = new TaskEscrow.CreateMilestoneParams[](milestones.length);
        for (uint256 i = 0; i < milestones.length; i++) {
            escrowMilestones[i] = TaskEscrow.CreateMilestoneParams({
                amount: milestones[i].amount, submissionDeadline: milestones[i].submissionDeadline
            });
        }
    }

    function _computeFeeSnapshot(uint8 serviceTier, address parentEscrow) internal returns (FeeComputation memory fee) {
        fee.baseFeeBps = serviceTier == TIER_HIGH_ASSURANCE ? highAssuranceFeeBps : protocolFeeBps;
        (fee.surchargeBps, fee.redelegationStreak) = _computeRedelegationSurcharge(parentEscrow);
        uint256 feeSnapshotRaw = uint256(fee.baseFeeBps) + uint256(fee.surchargeBps);
        if (feeSnapshotRaw > 10_000) revert InvalidFeeBps();
        fee.feeSnapshotBps = uint16(feeSnapshotRaw);
    }

    function _computeRedelegationSurcharge(address parentEscrow) internal returns (uint16 surchargeBps, uint16 streak) {
        if (parentEscrow == address(0)) {
            return (0, 0);
        }

        if (redelegationSurchargeStepBps == 0 || redelegationMaxSurchargeBps == 0) {
            return (0, 0);
        }

        RedelegationFeeState storage state = redelegationFeeState[parentEscrow];
        bool isFrequent = state.lastRedelegationAt != 0 && redelegationFrequencyWindowSeconds > 0
            && block.timestamp <= uint256(state.lastRedelegationAt) + uint256(redelegationFrequencyWindowSeconds);

        if (isFrequent && state.streak < type(uint16).max) {
            streak = state.streak + 1;
        } else if (isFrequent) {
            streak = type(uint16).max;
        } else {
            streak = 0;
        }

        state.lastRedelegationAt = uint64(block.timestamp);
        state.streak = streak;

        uint256 rawSurcharge = uint256(streak) * uint256(redelegationSurchargeStepBps);
        if (rawSurcharge > redelegationMaxSurchargeBps) {
            rawSurcharge = redelegationMaxSurchargeBps;
        }
        surchargeBps = uint16(rawSurcharge);
    }

    function getWorkerReputation(address addr)
        external
        view
        returns (uint32 completed, uint32 disputed, uint32 failed)
    {
        ReputationRecord storage r = workerReputation[addr];
        return (r.completed, r.disputed, r.failed);
    }

    function getBuyerReputation(address addr) external view returns (uint32 completed, uint32 disputed, uint32 failed) {
        ReputationRecord storage r = buyerReputation[addr];
        return (r.completed, r.disputed, r.failed);
    }
}
