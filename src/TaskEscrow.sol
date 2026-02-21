// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {IERC20} from "./interfaces/IERC20.sol";

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

    event EscrowFunded(address indexed buyer, uint256 amount);
    event SubmissionMade(address indexed worker, bytes32 submissionHash, string submissionURI);
    event Approved(address indexed approver, uint64 approvedAt);
    event Rejected(address indexed verifier, string reasonURI, uint64 rejectedAt);
    event Disputed(address indexed raisedBy, string reasonURI, uint64 disputedAt);
    event SilenceEscalated(address indexed worker, string reasonURI, uint64 escalatedAt);
    event DisputeResolved(address indexed arbitrator, uint16 workerAwardBps, string resolutionURI);
    event ArbitratorTimeoutClaimed(address indexed buyer, uint64 claimedAt);
    event Settled(uint256 workerNet, uint256 buyerRefund, uint256 protocolFee);
    event Refunded(uint256 amount);
    event Cancelled();

    /// @dev address(0) means ETH-denominated escrow; non-zero means ERC20
    address public immutable token;

    address public immutable buyer;
    address public immutable worker;
    address public immutable verifier;
    address public immutable arbitrator;
    uint256 public immutable amount;
    uint64 public immutable submissionDeadline;
    uint64 public immutable reviewPeriodSeconds;
    uint64 public immutable disputePeriodSeconds;
    bytes32 public immutable taskSpecHash;
    uint16 public immutable protocolFeeBpsSnapshot;
    address public immutable treasurySnapshot;
    uint64 public immutable arbitratorTimeoutSeconds;

    uint64 public submittedAt;
    uint64 public approvedAt;
    uint64 public disputedAt;
    Status public status;
    bytes32 public submissionHash;
    string public submissionURI;
    string public disputeReasonURI;

    error RolesNotDistinct();

    struct Params {
        address buyer;
        address worker;
        address verifier;
        address arbitrator;
        uint256 amount;
        uint64 submissionDeadline;
        uint64 reviewPeriodSeconds;
        uint64 disputePeriodSeconds;
        bytes32 taskSpecHash;
        uint16 protocolFeeBpsSnapshot;
        address treasurySnapshot;
        uint64 arbitratorTimeoutSeconds;
        address token;
    }

    // Uses 1/2 pattern instead of 0/1 to avoid zero-to-nonzero SSTORE cost (20k vs 5k gas)
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
        if (p.amount == 0) revert InvalidAmount();
        if (p.submissionDeadline <= block.timestamp) revert InvalidDeadline();
        if (p.protocolFeeBpsSnapshot > 10_000) revert InvalidAwardBps();

        buyer = p.buyer;
        worker = p.worker;
        verifier = p.verifier;
        arbitrator = p.arbitrator;
        amount = p.amount;
        submissionDeadline = p.submissionDeadline;
        reviewPeriodSeconds = p.reviewPeriodSeconds;
        disputePeriodSeconds = p.disputePeriodSeconds;
        taskSpecHash = p.taskSpecHash;
        protocolFeeBpsSnapshot = p.protocolFeeBpsSnapshot;
        treasurySnapshot = p.treasurySnapshot;
        arbitratorTimeoutSeconds = p.arbitratorTimeoutSeconds;
        token = p.token;
        status = Status.Created;
    }

    modifier nonReentrant() {
        if (_locked == _ENTERED) revert Reentrancy();
        _locked = _ENTERED;
        _;
        _locked = _NOT_ENTERED;
    }

    function fund() external payable nonReentrant {
        if (msg.sender != buyer) revert Unauthorized();
        if (status != Status.Created) revert InvalidState();

        if (token == address(0)) {
            if (msg.value != amount) revert InvalidAmount();
        } else {
            if (msg.value != 0) revert ETHNotAccepted();
            uint256 balanceBefore = IERC20(token).balanceOf(address(this));
            _safeTransferFrom(IERC20(token), msg.sender, address(this), amount);
            if (IERC20(token).balanceOf(address(this)) - balanceBefore != amount) revert InsufficientReceived();
        }

        status = Status.Funded;
        emit EscrowFunded(msg.sender, amount);
    }

    function cancelBeforeFunding() external {
        if (msg.sender != buyer) revert Unauthorized();
        if (status != Status.Created) revert InvalidState();
        status = Status.Cancelled;
        emit Cancelled();
    }

    function submit(bytes32 _submissionHash, string calldata _submissionURI) external {
        if (msg.sender != worker) revert Unauthorized();
        if (status != Status.Funded) revert InvalidState();
        if (block.timestamp > submissionDeadline) revert WindowExpired();
        if (_submissionHash == bytes32(0)) revert InvalidHash();

        submissionHash = _submissionHash;
        submissionURI = _submissionURI;
        submittedAt = uint64(block.timestamp);
        status = Status.Submitted;
        emit SubmissionMade(msg.sender, _submissionHash, _submissionURI);
    }

    function approveByBuyer() external nonReentrant {
        if (msg.sender != buyer) revert Unauthorized();
        _approve(msg.sender);
    }

    function approveByVerifier() external nonReentrant {
        if (msg.sender != verifier) revert Unauthorized();
        _approve(msg.sender);
    }

    function rejectByVerifier(string calldata reasonURI) external {
        if (msg.sender != verifier) revert Unauthorized();
        if (status != Status.Submitted) revert InvalidState();
        if (block.timestamp > _reviewWindowEnds()) revert WindowExpired();

        disputeReasonURI = reasonURI;
        disputedAt = uint64(block.timestamp);
        status = Status.Disputed;

        emit Rejected(msg.sender, reasonURI, uint64(block.timestamp));
        emit Disputed(msg.sender, reasonURI, uint64(block.timestamp));
    }

    function dispute(string calldata reasonURI) external {
        if (msg.sender != buyer) revert Unauthorized();
        if (status != Status.Submitted) revert InvalidState();
        if (block.timestamp > _disputeWindowEnds()) revert WindowExpired();

        disputeReasonURI = reasonURI;
        disputedAt = uint64(block.timestamp);
        status = Status.Disputed;
        emit Disputed(msg.sender, reasonURI, uint64(block.timestamp));
    }

    function escalateSilence(string calldata reasonURI) external {
        if (msg.sender != worker) revert Unauthorized();
        if (status != Status.Submitted) revert InvalidState();
        if (block.timestamp <= _reviewWindowEnds()) revert WindowNotOpen();
        if (block.timestamp > _disputeWindowEnds()) revert WindowExpired();

        disputeReasonURI = reasonURI;
        disputedAt = uint64(block.timestamp);
        status = Status.Disputed;

        emit SilenceEscalated(msg.sender, reasonURI, uint64(block.timestamp));
        emit Disputed(msg.sender, reasonURI, uint64(block.timestamp));
    }

    function resolveDispute(uint16 workerAwardBps, string calldata resolutionURI) external nonReentrant {
        if (msg.sender != arbitrator) revert Unauthorized();
        if (status != Status.Disputed) revert InvalidState();
        if (workerAwardBps > 10_000) revert InvalidAwardBps();

        status = Status.Resolved;
        emit DisputeResolved(msg.sender, workerAwardBps, resolutionURI);
        _settleResolved(workerAwardBps);
    }

    function claimTimeoutRefund() external nonReentrant {
        if (msg.sender != buyer) revert Unauthorized();
        if (status != Status.Funded) revert InvalidState();
        if (block.timestamp <= submissionDeadline) revert WindowNotOpen();

        status = Status.Refunded;
        _send(buyer, amount);
        emit Refunded(amount);
    }

    function claimArbitratorTimeout() external nonReentrant {
        if (msg.sender != buyer) revert Unauthorized();
        if (status != Status.Disputed) revert InvalidState();
        if (block.timestamp <= uint256(disputedAt) + uint256(arbitratorTimeoutSeconds)) {
            revert ArbitratorTimeoutNotReached();
        }

        status = Status.Refunded;
        _send(buyer, amount);
        emit ArbitratorTimeoutClaimed(msg.sender, uint64(block.timestamp));
        emit Refunded(amount);
    }

    function _approve(address approver) internal {
        if (status != Status.Submitted) revert InvalidState();
        if (block.timestamp > _reviewWindowEnds()) revert WindowExpired();

        approvedAt = uint64(block.timestamp);
        status = Status.Approved;
        emit Approved(approver, approvedAt);
        _settleApproved();
    }

    function _settleApproved() internal {
        uint256 grossWorker = amount;
        uint256 fee = (grossWorker * protocolFeeBpsSnapshot) / 10_000;
        uint256 workerNet = grossWorker - fee;

        status = Status.Settled;
        _send(worker, workerNet);
        if (fee > 0) _send(treasurySnapshot, fee);
        emit Settled(workerNet, 0, fee);
    }

    function _settleResolved(uint16 workerAwardBps) internal {
        uint256 workerGross = (amount * workerAwardBps) / 10_000;
        uint256 buyerRefund = amount - workerGross;
        uint256 fee = (workerGross * protocolFeeBpsSnapshot) / 10_000;
        uint256 workerNet = workerGross - fee;

        status = Status.Settled;
        if (workerNet > 0) _send(worker, workerNet);
        if (buyerRefund > 0) _send(buyer, buyerRefund);
        if (fee > 0) _send(treasurySnapshot, fee);
        emit Settled(workerNet, buyerRefund, fee);
    }

    /// @dev Transfers ETH or ERC20 depending on the escrow's token mode.
    function _send(address to, uint256 value) internal {
        if (token == address(0)) {
            (bool ok,) = payable(to).call{value: value}("");
            if (!ok) revert TransferFailed();
        } else {
            _safeTransfer(IERC20(token), to, value);
        }
    }

    /// @dev Safe ERC20 transfer that handles tokens not returning a bool (e.g. USDT).
    function _safeTransfer(IERC20 _token, address to, uint256 value) internal {
        (bool success, bytes memory data) =
            address(_token).call(abi.encodeWithSelector(_token.transfer.selector, to, value));
        if (!success || (data.length > 0 && !abi.decode(data, (bool)))) revert TransferFailed();
    }

    /// @dev Safe ERC20 transferFrom that handles tokens not returning a bool (e.g. USDT).
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
}
