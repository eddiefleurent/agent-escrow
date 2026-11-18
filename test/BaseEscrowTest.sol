// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Test} from "forge-std/Test.sol";
import {TaskEscrowFactory} from "../src/TaskEscrowFactory.sol";
import {TaskEscrow} from "../src/TaskEscrow.sol";
import {IERC20} from "../src/interfaces/IERC20.sol";

/// @notice Shared fixture for escrow tests: common actors, factory deployment,
/// an escrow builder with sensible defaults, and role-aware lifecycle helpers.
/// @dev Builder pattern: start from `_defaultParams()`, mutate the returned memory
/// struct (or use the `_with*` helpers), then call `_create(p)`.
abstract contract BaseEscrowTest is Test {
    TaskEscrowFactory internal factory;

    address internal owner = makeAddr("owner");
    address internal buyer = makeAddr("buyer");
    address internal worker = makeAddr("worker");
    address internal verifier = makeAddr("verifier");
    address internal arbitrator = makeAddr("arbitrator");
    address internal treasury = makeAddr("treasury");
    address internal backup = makeAddr("backup");

    uint256 internal constant AMOUNT = 1 ether;
    uint256 internal constant STAKE = 0.1 ether;
    uint16 internal constant FEE_BPS = 100; // 1%
    uint64 internal constant REVIEW = 86_400; // 1 day
    uint64 internal constant DISPUTE = 172_800; // 2 days
    uint64 internal constant ARB_TIMEOUT = 7 days;
    uint64 internal constant BACKUP_EXTENSION = 7 days;
    uint64 internal constant DEFAULT_DEADLINE = 7 days;

    bytes32 internal constant SUBMISSION_HASH = keccak256("submission");
    string internal constant SUBMISSION_URI = "ipfs://result";

    function setUp() public virtual {
        factory = new TaskEscrowFactory(FEE_BPS, FEE_BPS, treasury, owner);
    }

    // ── Builder ──

    /// @dev Single-shot ETH escrow, 1-of-1 verifier panel, no stakes, no backup, no ZK, low-assurance tier.
    function _defaultParams() internal view returns (TaskEscrowFactory.CreateParams memory p) {
        p.buyer = buyer;
        p.worker = worker;
        p.verifierPanel[0] = verifier;
        p.quorumThreshold = 1;
        p.quorumVerifierCount = 1;
        p.arbitrator = arbitrator;
        p.amount = AMOUNT;
        p.submissionDeadline = uint64(block.timestamp + DEFAULT_DEADLINE);
        p.reviewPeriodSeconds = REVIEW;
        p.disputePeriodSeconds = DISPUTE;
        p.taskSpecHash = keccak256("spec");
        p.arbitratorTimeoutSeconds = ARB_TIMEOUT;
        p.milestones = new TaskEscrowFactory.CreateMilestoneParams[](0);
    }

    /// @dev `n` equal milestones of `each`, deadlines spaced `spacing` apart starting at now + spacing.
    /// Sets `amount` to the milestone sum and `submissionDeadline` to the last milestone deadline.
    function _withMilestones(TaskEscrowFactory.CreateParams memory p, uint256 n, uint256 each, uint64 spacing)
        internal
        view
        returns (TaskEscrowFactory.CreateParams memory)
    {
        p.milestones = _milestones(n, each, spacing);
        p.amount = n * each;
        p.submissionDeadline = p.milestones[n - 1].submissionDeadline;
        return p;
    }

    function _milestones(uint256 n, uint256 each, uint64 spacing)
        internal
        view
        returns (TaskEscrowFactory.CreateMilestoneParams[] memory ms)
    {
        ms = new TaskEscrowFactory.CreateMilestoneParams[](n);
        for (uint256 i; i < n; i++) {
            ms[i] = TaskEscrowFactory.CreateMilestoneParams({
                amount: each, submissionDeadline: uint64(block.timestamp + spacing * (i + 1))
            });
        }
    }

    /// @dev Replaces the verifier panel with `panel` and sets the quorum.
    function _withQuorum(
        TaskEscrowFactory.CreateParams memory p,
        address[] memory panel,
        uint8 threshold,
        uint256 verifierStake
    ) internal pure returns (TaskEscrowFactory.CreateParams memory) {
        delete p.verifierPanel;
        for (uint256 i; i < panel.length; i++) {
            p.verifierPanel[i] = panel[i];
        }
        p.quorumVerifierCount = uint8(panel.length);
        p.quorumThreshold = threshold;
        p.verifierStakePerVerifier = verifierStake;
        return p;
    }

    function _withBackup(TaskEscrowFactory.CreateParams memory p)
        internal
        view
        returns (TaskEscrowFactory.CreateParams memory)
    {
        p.backupWorker = backup;
        p.backupDeadlineExtension = BACKUP_EXTENSION;
        return p;
    }

    function _create(TaskEscrowFactory.CreateParams memory p) internal returns (TaskEscrow) {
        (, address addr) = factory.createEscrow(p);
        return TaskEscrow(addr);
    }

    function _createWithId(TaskEscrowFactory.CreateParams memory p) internal returns (uint256 id, TaskEscrow e) {
        address addr;
        (id, addr) = factory.createEscrow(p);
        e = TaskEscrow(addr);
    }

    function _createDefault() internal returns (TaskEscrow) {
        return _create(_defaultParams());
    }

    // ── Lifecycle helpers (each pranks as the correct role) ──

    /// @dev Funds as buyer. ERC20 escrows: buyer must already hold the tokens; approval is done here.
    function _fund(TaskEscrow e) internal {
        // Read views before pranking: a prank is consumed by the next call, including staticcalls.
        address tok = e.token();
        uint256 amt = e.amount();
        if (tok == address(0)) {
            vm.prank(buyer);
            e.fund{value: amt}();
        } else {
            vm.startPrank(buyer);
            IERC20(tok).approve(address(e), amt);
            e.fund();
            vm.stopPrank();
        }
    }

    /// @dev Deposits the worker stake as the current active worker.
    function _depositStake(TaskEscrow e) internal {
        address w = e.activeWorker();
        address tok = e.token();
        uint256 stake = e.workerStake();
        if (tok == address(0)) {
            vm.prank(w);
            e.depositStake{value: stake}();
        } else {
            vm.startPrank(w);
            IERC20(tok).approve(address(e), stake);
            e.depositStake();
            vm.stopPrank();
        }
    }

    function _submit(TaskEscrow e) internal {
        vm.prank(e.activeWorker());
        e.submit(SUBMISSION_HASH, SUBMISSION_URI, bytes32(0));
    }

    function _fundAndSubmit(TaskEscrow e) internal {
        _fund(e);
        _submit(e);
    }

    function _approve(TaskEscrow e) internal {
        vm.prank(buyer);
        e.approveByBuyer();
    }

    function _dispute(TaskEscrow e) internal {
        vm.prank(buyer);
        e.dispute("ipfs://dispute");
    }

    function _resolve(TaskEscrow e, uint16 workerAwardBps) internal {
        vm.prank(e.arbitrator());
        e.resolveDispute(workerAwardBps, "ipfs://resolution");
    }

    function _submitMs(TaskEscrow e, uint8 idx) internal {
        vm.prank(e.activeWorker());
        e.submitMilestone(idx, keccak256(abi.encodePacked("ms", idx)), "ipfs://ms", bytes32(0));
    }

    function _approveMs(TaskEscrow e, uint8 idx) internal {
        vm.prank(buyer);
        e.approveMilestoneByBuyer(idx);
    }

    function _disputeMs(TaskEscrow e, uint8 idx) internal {
        vm.prank(buyer);
        e.disputeMilestone(idx, "ipfs://ms-dispute");
    }

    function _resolveMs(TaskEscrow e, uint8 idx, uint16 workerAwardBps) internal {
        vm.prank(e.arbitrator());
        e.resolveMilestoneDispute(idx, workerAwardBps, "ipfs://ms-resolution");
    }

    function _status(TaskEscrow e) internal view returns (uint256) {
        return uint256(e.status());
    }
}
