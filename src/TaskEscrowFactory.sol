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

    event EscrowCreated(
        uint256 indexed escrowId,
        address indexed escrow,
        address indexed buyer,
        address worker,
        address verifier,
        address arbitrator,
        bytes32 taskSpecHash
    );
    event ProtocolFeeUpdated(uint16 oldFeeBps, uint16 newFeeBps);
    event TreasuryUpdated(address oldTreasury, address newTreasury);
    event FactoryPaused();
    event FactoryUnpaused();

    uint256 public nextEscrowId;
    mapping(uint256 => address) public escrowById;
    uint16 public protocolFeeBps;
    address public treasury;
    address public owner;
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

    function createEscrow(
        address buyer,
        address worker,
        address verifier,
        address arbitrator,
        uint256 amount,
        uint64 submissionDeadline,
        uint64 reviewPeriodSeconds,
        uint64 disputePeriodSeconds,
        bytes32 taskSpecHash,
        uint64 arbitratorTimeoutSeconds
    ) external whenNotPaused returns (uint256 escrowId, address escrow) {
        if (amount == 0) revert InvalidAmount();
        if (submissionDeadline <= block.timestamp) revert InvalidDeadline();

        TaskEscrow instance = new TaskEscrow(
            buyer,
            worker,
            verifier,
            arbitrator,
            amount,
            submissionDeadline,
            reviewPeriodSeconds,
            disputePeriodSeconds,
            taskSpecHash,
            protocolFeeBps,
            treasury,
            arbitratorTimeoutSeconds
        );

        escrowId = nextEscrowId++;
        escrow = address(instance);
        escrowById[escrowId] = escrow;

        emit EscrowCreated(escrowId, escrow, buyer, worker, verifier, arbitrator, taskSpecHash);
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
}
