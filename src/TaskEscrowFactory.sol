// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {TaskEscrow} from "./TaskEscrow.sol";

contract TaskEscrowFactory {
    error Unauthorized();
    error InvalidAddress();
    error InvalidFeeBps();
    error InvalidAmount();
    error InvalidDeadline();
    error Paused();
    error NoPendingTransfer();

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
    event FactoryPaused();
    event FactoryUnpaused();

    uint256 public nextEscrowId;
    mapping(uint256 => address) public escrowById;
    uint16 public protocolFeeBps;
    address public treasury;
    address public owner;
    address public pendingOwner;
    bool public paused;

    constructor(uint16 _protocolFeeBps, address _treasury, address _owner) {
        if (_protocolFeeBps > 10_000) revert InvalidFeeBps();
        if (_treasury == address(0) || _owner == address(0)) revert InvalidAddress();
        protocolFeeBps = _protocolFeeBps;
        treasury = _treasury;
        owner = _owner;
    }

    modifier onlyOwner() {
        if (msg.sender != owner) revert Unauthorized();
        _;
    }

    modifier whenNotPaused() {
        if (paused) revert Paused();
        _;
    }

    struct CreateParams {
        address buyer;
        address worker;
        address verifier;
        address arbitrator;
        uint256 amount;
        uint64 submissionDeadline;
        uint64 reviewPeriodSeconds;
        uint64 disputePeriodSeconds;
        bytes32 taskSpecHash;
        uint64 arbitratorTimeoutSeconds;
        address token;
    }

    function createEscrow(CreateParams calldata p) external whenNotPaused returns (uint256 escrowId, address escrow) {
        if (p.amount == 0) revert InvalidAmount();
        if (p.submissionDeadline <= block.timestamp) revert InvalidDeadline();

        TaskEscrow instance = new TaskEscrow(
            TaskEscrow.Params({
                buyer: p.buyer,
                worker: p.worker,
                verifier: p.verifier,
                arbitrator: p.arbitrator,
                amount: p.amount,
                submissionDeadline: p.submissionDeadline,
                reviewPeriodSeconds: p.reviewPeriodSeconds,
                disputePeriodSeconds: p.disputePeriodSeconds,
                taskSpecHash: p.taskSpecHash,
                protocolFeeBpsSnapshot: protocolFeeBps,
                treasurySnapshot: treasury,
                arbitratorTimeoutSeconds: p.arbitratorTimeoutSeconds,
                token: p.token
            })
        );

        escrowId = nextEscrowId++;
        escrow = address(instance);
        escrowById[escrowId] = escrow;

        emit EscrowCreated(escrowId, escrow, p.buyer, p.worker, p.verifier, p.arbitrator, p.taskSpecHash, p.token);
    }

    function setProtocolFeeBps(uint16 newFeeBps) external onlyOwner {
        if (newFeeBps > 10_000) revert InvalidFeeBps();
        uint16 oldFee = protocolFeeBps;
        protocolFeeBps = newFeeBps;
        emit ProtocolFeeUpdated(oldFee, newFeeBps);
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
}
