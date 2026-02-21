// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {TaskEscrow} from "./TaskEscrow.sol";
import {EscrowDeployer} from "./EscrowDeployer.sol";

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

    event EscrowCreated(
        uint256 indexed escrowId,
        address indexed escrow,
        address indexed buyer,
        address worker,
        address verifier,
        address arbitrator,
        bytes32 taskSpecHash,
        address token
    );
    event ProtocolFeeUpdated(uint16 oldFeeBps, uint16 newFeeBps);
    event TreasuryUpdated(address oldTreasury, address newTreasury);
    event OwnershipTransferStarted(address indexed previousOwner, address indexed newOwner);
    event OwnershipTransferred(address indexed previousOwner, address indexed newOwner);
    event BackupDesignated(uint256 indexed escrowId, address indexed backupWorker, uint64 backupDeadlineExtension);
    event ComplexityFloorUpdated(uint256 oldFloor, uint256 newFloor);
    event FactoryPaused();
    event FactoryUnpaused();
    event OutcomeRecorded(uint256 indexed escrowId, address indexed participant, string role, string outcome);

    // Outcome enum values passed by escrow contracts via recordOutcome().
    uint8 public constant OUTCOME_COMPLETED = 1;
    uint8 public constant OUTCOME_DISPUTED = 2;
    uint8 public constant OUTCOME_FAILED = 3;

    struct ReputationRecord {
        uint32 completed;
        uint32 disputed;
        uint32 failed;
    }

    uint256 public nextEscrowId;
    mapping(uint256 => address) public escrowById;
    uint16 public protocolFeeBps;
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

    constructor(uint16 _protocolFeeBps, address _treasury, address _owner) {
        if (_protocolFeeBps > 10_000) revert InvalidFeeBps();
        if (_treasury == address(0) || _owner == address(0)) revert InvalidAddress();
        protocolFeeBps = _protocolFeeBps;
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
        address backupWorker;
        uint64 backupDeadlineExtension;
        CreateMilestoneParams[] milestones;
    }

    function createEscrow(CreateParams calldata p) external whenNotPaused returns (uint256 escrowId, address escrow) {
        if (p.amount == 0) revert InvalidAmount();
        if (complexityFloor > 0 && p.amount < complexityFloor) revert BelowComplexityFloor();
        if (p.submissionDeadline <= block.timestamp) revert InvalidDeadline();

        TaskEscrow.CreateMilestoneParams[] memory escrowMilestones =
            new TaskEscrow.CreateMilestoneParams[](p.milestones.length);
        for (uint256 i = 0; i < p.milestones.length; i++) {
            escrowMilestones[i] = TaskEscrow.CreateMilestoneParams({
                amount: p.milestones[i].amount, submissionDeadline: p.milestones[i].submissionDeadline
            });
        }

        escrow = deployer.deploy(
            TaskEscrow.Params({
                factory: address(this),
                buyer: p.buyer,
                worker: p.worker,
                verifier: p.verifier,
                arbitrator: p.arbitrator,
                amount: p.amount,
                workerStake: p.workerStake,
                submissionDeadline: p.submissionDeadline,
                reviewPeriodSeconds: p.reviewPeriodSeconds,
                disputePeriodSeconds: p.disputePeriodSeconds,
                taskSpecHash: p.taskSpecHash,
                protocolFeeBpsSnapshot: protocolFeeBps,
                treasurySnapshot: treasury,
                arbitratorTimeoutSeconds: p.arbitratorTimeoutSeconds,
                token: p.token,
                backupWorker: p.backupWorker,
                backupDeadlineExtension: p.backupDeadlineExtension,
                milestones: escrowMilestones
            })
        );

        escrowId = nextEscrowId++;
        escrowById[escrowId] = escrow;
        escrowToId[escrow] = escrowId + 1; // +1 so 0 means "not registered"
        escrowBuyer[escrowId] = p.buyer;
        escrowWorker[escrowId] = p.worker;

        emit EscrowCreated(escrowId, escrow, p.buyer, p.worker, p.verifier, p.arbitrator, p.taskSpecHash, p.token);

        if (p.backupWorker != address(0)) {
            emit BackupDesignated(escrowId, p.backupWorker, p.backupDeadlineExtension);
        }
    }

    function setProtocolFeeBps(uint16 newFeeBps) external onlyOwner {
        if (newFeeBps > 10_000) revert InvalidFeeBps();
        uint16 oldFee = protocolFeeBps;
        protocolFeeBps = newFeeBps;
        emit ProtocolFeeUpdated(oldFee, newFeeBps);
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
