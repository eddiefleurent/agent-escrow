// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

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

    // Uses 1/2 pattern instead of 0/1 to avoid zero-to-nonzero SSTORE cost (20k vs 5k gas)
    uint256 private constant _NOT_ENTERED = 1;
    uint256 private constant _ENTERED = 2;
    uint256 private _locked = _NOT_ENTERED;

    constructor(
        address _buyer,
        address _worker,
        address _verifier,
        address _arbitrator,
        uint256 _amount,
        uint64 _submissionDeadline,
        uint64 _reviewPeriodSeconds,
        uint64 _disputePeriodSeconds,
        bytes32 _taskSpecHash,
        uint16 _protocolFeeBpsSnapshot,
        address _treasurySnapshot,
        uint64 _arbitratorTimeoutSeconds
    ) {
        if (_buyer == address(0) || _worker == address(0) || _verifier == address(0) || _arbitrator == address(0)) {
            revert InvalidAddress();
        }
        if (_treasurySnapshot == address(0)) revert InvalidAddress();
        if (
            _buyer == _worker || _buyer == _verifier || _buyer == _arbitrator
                || _worker == _verifier || _worker == _arbitrator
                || _verifier == _arbitrator
        ) {
            revert RolesNotDistinct();
        }
        if (_amount == 0) revert InvalidAmount();
        if (_submissionDeadline <= block.timestamp) revert InvalidDeadline();
        if (_protocolFeeBpsSnapshot > 10_000) revert InvalidAwardBps();

        buyer = _buyer;
        worker = _worker;
        verifier = _verifier;
        arbitrator = _arbitrator;
        amount = _amount;
        submissionDeadline = _submissionDeadline;
        reviewPeriodSeconds = _reviewPeriodSeconds;
        disputePeriodSeconds = _disputePeriodSeconds;
        taskSpecHash = _taskSpecHash;
        protocolFeeBpsSnapshot = _protocolFeeBpsSnapshot;
        treasurySnapshot = _treasurySnapshot;
        arbitratorTimeoutSeconds = _arbitratorTimeoutSeconds;
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
        if (msg.value != amount) revert InvalidAmount();

        status = Status.Funded;
        emit EscrowFunded(msg.sender, msg.value);
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
        _sendETH(buyer, amount);
        emit Refunded(amount);
    }

    function claimArbitratorTimeout() external nonReentrant {
        if (msg.sender != buyer) revert Unauthorized();
        if (status != Status.Disputed) revert InvalidState();
        if (block.timestamp <= uint256(disputedAt) + uint256(arbitratorTimeoutSeconds)) {
            revert ArbitratorTimeoutNotReached();
        }

        status = Status.Refunded;
        _sendETH(buyer, amount);
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
        _sendETH(worker, workerNet);
        if (fee > 0) _sendETH(treasurySnapshot, fee);
        emit Settled(workerNet, 0, fee);
    }

    function _settleResolved(uint16 workerAwardBps) internal {
        uint256 workerGross = (amount * workerAwardBps) / 10_000;
        uint256 buyerRefund = amount - workerGross;
        uint256 fee = (workerGross * protocolFeeBpsSnapshot) / 10_000;
        uint256 workerNet = workerGross - fee;

        status = Status.Settled;
        if (workerNet > 0) _sendETH(worker, workerNet);
        if (buyerRefund > 0) _sendETH(buyer, buyerRefund);
        if (fee > 0) _sendETH(treasurySnapshot, fee);
        emit Settled(workerNet, buyerRefund, fee);
    }

    function _sendETH(address to, uint256 value) internal {
        (bool ok,) = payable(to).call{value: value}("");
        if (!ok) revert TransferFailed();
    }

    function _reviewWindowEnds() internal view returns (uint256) {
        return uint256(submittedAt) + uint256(reviewPeriodSeconds);
    }

    function _disputeWindowEnds() internal view returns (uint256) {
        return uint256(submittedAt) + uint256(reviewPeriodSeconds) + uint256(disputePeriodSeconds);
    }
}
