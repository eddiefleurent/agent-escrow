// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {BaseEscrowTest} from "./BaseEscrowTest.sol";
import {TaskEscrowFactory} from "../src/TaskEscrowFactory.sol";
import {TaskEscrow} from "../src/TaskEscrow.sol";

contract TaskEscrowReputationTest is BaseEscrowTest {
    address internal backupWorker = makeAddr("backupWorker");

    function setUp() public override {
        super.setUp();
        vm.deal(buyer, 100 ether);
        vm.deal(worker, 10 ether);
        vm.deal(backupWorker, 10 ether);
    }

    function _createEscrow() internal returns (uint256 escrowId, TaskEscrow escrow) {
        vm.prank(address(0xBEEF));
        (escrowId,) = factory.createEscrow(_defaultParams());
        escrow = TaskEscrow(factory.escrowById(escrowId));
    }

    function _createStakedEscrow() internal returns (uint256 escrowId, TaskEscrow escrow) {
        TaskEscrowFactory.CreateParams memory params = _defaultParams();
        params.workerStake = STAKE;
        vm.prank(address(0xBEEF));
        (escrowId,) = factory.createEscrow(params);
        escrow = TaskEscrow(factory.escrowById(escrowId));
    }

    function _createBackupEscrow() internal returns (uint256 escrowId, TaskEscrow escrow) {
        TaskEscrowFactory.CreateParams memory params = _defaultParams();
        params.backupWorker = backupWorker;
        params.backupDeadlineExtension = 3 days;
        vm.prank(address(0xBEEF));
        (escrowId,) = factory.createEscrow(params);
        escrow = TaskEscrow(factory.escrowById(escrowId));
    }

    // ── Happy path: approval ──

    function testApprovalRecordsCompleted() public {
        (, TaskEscrow escrow) = _createEscrow();

        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();
        vm.prank(worker);
        escrow.submit(keccak256("sub"), "ipfs://sub", bytes32(0));
        vm.prank(buyer);
        escrow.approveByBuyer();

        (uint32 wc, uint32 wd, uint32 wf) = factory.getWorkerReputation(worker);
        assertEq(wc, 1, "worker completed");
        assertEq(wd, 0, "worker disputed");
        assertEq(wf, 0, "worker failed");

        (uint32 bc, uint32 bd, uint32 bf) = factory.getBuyerReputation(buyer);
        assertEq(bc, 1, "buyer completed");
        assertEq(bd, 0, "buyer disputed");
        assertEq(bf, 0, "buyer failed");
    }

    function testApprovalByVerifierRecordsCompleted() public {
        (, TaskEscrow escrow) = _createEscrow();

        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();
        vm.prank(worker);
        escrow.submit(keccak256("sub"), "ipfs://sub", bytes32(0));
        vm.prank(verifier);
        escrow.castVerifierVote(true, "");

        (uint32 wc,,) = factory.getWorkerReputation(worker);
        assertEq(wc, 1);
        (uint32 bc,,) = factory.getBuyerReputation(buyer);
        assertEq(bc, 1);
    }

    // ── Dispute resolution ──

    function testDisputeResolutionRecordsDisputed() public {
        (, TaskEscrow escrow) = _createEscrow();

        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();
        vm.prank(worker);
        escrow.submit(keccak256("sub"), "ipfs://sub", bytes32(0));
        vm.prank(buyer);
        escrow.dispute("ipfs://reason");
        vm.prank(arbitrator);
        escrow.resolveDispute(5000, "ipfs://resolution");

        (uint32 wc, uint32 wd, uint32 wf) = factory.getWorkerReputation(worker);
        assertEq(wc, 0);
        assertEq(wd, 1, "worker disputed");
        assertEq(wf, 0);

        (uint32 bc, uint32 bd, uint32 bf) = factory.getBuyerReputation(buyer);
        assertEq(bc, 0);
        assertEq(bd, 1, "buyer disputed");
        assertEq(bf, 0);
    }

    // ── Timeout refund ──

    function testTimeoutRefundRecordsFailed() public {
        (, TaskEscrow escrow) = _createEscrow();

        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.warp(block.timestamp + 8 days);

        vm.prank(buyer);
        escrow.claimTimeoutRefund();

        (, uint32 wd, uint32 wf) = factory.getWorkerReputation(worker);
        assertEq(wd, 0);
        assertEq(wf, 1, "worker failed");

        (, uint32 bd, uint32 bf) = factory.getBuyerReputation(buyer);
        assertEq(bd, 0);
        assertEq(bf, 1, "buyer failed");
    }

    // ── Arbitrator timeout ──

    function testArbitratorTimeoutRecordsDisputed() public {
        (, TaskEscrow escrow) = _createEscrow();

        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();
        vm.prank(worker);
        escrow.submit(keccak256("sub"), "ipfs://sub", bytes32(0));
        vm.prank(buyer);
        escrow.dispute("ipfs://reason");

        vm.warp(block.timestamp + ARB_TIMEOUT + 1);

        vm.prank(buyer);
        escrow.claimArbitratorTimeout();

        (, uint32 wd, uint32 wf) = factory.getWorkerReputation(worker);
        assertEq(wd, 1, "worker disputed after arb timeout");
        assertEq(wf, 0);

        (, uint32 bd, uint32 bf) = factory.getBuyerReputation(buyer);
        assertEq(bd, 1, "buyer disputed after arb timeout");
        assertEq(bf, 0);
    }

    // ── Cancel before funding ──

    function testCancelBeforeFundingNoReputation() public {
        (, TaskEscrow escrow) = _createEscrow();

        vm.prank(buyer);
        escrow.cancelBeforeFunding();

        (uint32 wc, uint32 wd, uint32 wf) = factory.getWorkerReputation(worker);
        assertEq(wc, 0, "worker completed unchanged");
        assertEq(wd, 0, "worker disputed unchanged");
        assertEq(wf, 0, "worker failed unchanged");

        (uint32 bc, uint32 bd, uint32 bf) = factory.getBuyerReputation(buyer);
        assertEq(bc, 0, "buyer completed unchanged");
        assertEq(bd, 0, "buyer disputed unchanged");
        assertEq(bf, 0, "buyer failed unchanged");
    }

    // ── Backup activation + completion ──

    function testBackupWorkerGetsReputation() public {
        (, TaskEscrow escrow) = _createBackupEscrow();

        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        // Primary worker misses deadline
        vm.warp(block.timestamp + 8 days);

        vm.prank(buyer);
        escrow.activateBackup();

        // Backup worker submits and gets approved
        vm.prank(backupWorker);
        escrow.submit(keccak256("backup-sub"), "ipfs://backup-sub", bytes32(0));
        vm.prank(buyer);
        escrow.approveByBuyer();

        // Backup worker gets completed reputation
        (uint32 bwc,,) = factory.getWorkerReputation(backupWorker);
        assertEq(bwc, 1, "backup worker completed");

        // Original worker gets failed reputation (defaulted)
        (,, uint32 owf) = factory.getWorkerReputation(worker);
        assertEq(owf, 1, "original worker failed");

        // Buyer gets completed
        (uint32 bc,,) = factory.getBuyerReputation(buyer);
        assertEq(bc, 1, "buyer completed");
    }

    // ── Spoofing prevention ──

    function testNonEscrowCannotRecordOutcome() public {
        vm.expectRevert(TaskEscrowFactory.NotRegisteredEscrow.selector);
        factory.recordOutcome(1);
    }

    function testInvalidOutcomeReverts() public {
        (, TaskEscrow escrow) = _createEscrow();

        // Call from the escrow address (which is registered)
        vm.prank(address(escrow));
        vm.expectRevert(TaskEscrowFactory.InvalidOutcome.selector);
        factory.recordOutcome(0);

        vm.prank(address(escrow));
        vm.expectRevert(TaskEscrowFactory.InvalidOutcome.selector);
        factory.recordOutcome(4);
    }

    // ── Accumulation across multiple escrows ──

    function testReputationAccumulates() public {
        // First escrow: approved
        (, TaskEscrow escrow1) = _createEscrow();
        vm.prank(buyer);
        escrow1.fund{value: AMOUNT}();
        vm.prank(worker);
        escrow1.submit(keccak256("sub1"), "ipfs://sub1", bytes32(0));
        vm.prank(buyer);
        escrow1.approveByBuyer();

        // Second escrow: approved
        (, TaskEscrow escrow2) = _createEscrow();
        vm.prank(buyer);
        escrow2.fund{value: AMOUNT}();
        vm.prank(worker);
        escrow2.submit(keccak256("sub2"), "ipfs://sub2", bytes32(0));
        vm.prank(verifier);
        escrow2.castVerifierVote(true, "");

        // Third escrow: disputed
        (, TaskEscrow escrow3) = _createEscrow();
        vm.prank(buyer);
        escrow3.fund{value: AMOUNT}();
        vm.prank(worker);
        escrow3.submit(keccak256("sub3"), "ipfs://sub3", bytes32(0));
        vm.prank(buyer);
        escrow3.dispute("ipfs://reason");
        vm.prank(arbitrator);
        escrow3.resolveDispute(5000, "ipfs://resolution");

        (uint32 wc, uint32 wd, uint32 wf) = factory.getWorkerReputation(worker);
        assertEq(wc, 2, "worker completed twice");
        assertEq(wd, 1, "worker disputed once");
        assertEq(wf, 0);

        (uint32 bc, uint32 bd,) = factory.getBuyerReputation(buyer);
        assertEq(bc, 2, "buyer completed twice");
        assertEq(bd, 1, "buyer disputed once");
    }

    // ── OutcomeRecorded events ──

    function testOutcomeRecordedEventsEmitted() public {
        (uint256 escrowId, TaskEscrow escrow) = _createEscrow();

        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();
        vm.prank(worker);
        escrow.submit(keccak256("sub"), "ipfs://sub", bytes32(0));

        vm.expectEmit(true, true, false, true, address(factory));
        emit TaskEscrowFactory.OutcomeRecorded(escrowId, worker, "worker", "completed");
        vm.expectEmit(true, true, false, true, address(factory));
        emit TaskEscrowFactory.OutcomeRecorded(escrowId, buyer, "buyer", "completed");

        vm.prank(buyer);
        escrow.approveByBuyer();
    }

    // ── Milestone escrow: all approved -> Completed ──

    function testMilestoneAllApprovedRecordsCompleted() public {
        TaskEscrowFactory.CreateMilestoneParams[] memory ms = new TaskEscrowFactory.CreateMilestoneParams[](2);
        ms[0] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 0.5 ether, submissionDeadline: uint64(block.timestamp + 7 days)
        });
        ms[1] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 0.5 ether, submissionDeadline: uint64(block.timestamp + 14 days)
        });

        TaskEscrowFactory.CreateParams memory params = _defaultParams();
        params.submissionDeadline = uint64(block.timestamp + 14 days);
        params.milestones = ms;
        vm.prank(address(0xBEEF));
        (uint256 escrowId,) = factory.createEscrow(params);
        TaskEscrow escrow = TaskEscrow(factory.escrowById(escrowId));

        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        // Approve milestone 0
        vm.prank(worker);
        escrow.submitMilestone(0, keccak256("ms0"), "ipfs://ms0", bytes32(0));
        vm.prank(buyer);
        escrow.approveMilestoneByBuyer(0);

        // Approve milestone 1
        vm.prank(worker);
        escrow.submitMilestone(1, keccak256("ms1"), "ipfs://ms1", bytes32(0));
        vm.prank(buyer);
        escrow.approveMilestoneByBuyer(1);

        (uint32 wc, uint32 wd, uint32 wf) = factory.getWorkerReputation(worker);
        assertEq(wc, 1, "worker completed");
        assertEq(wd, 0);
        assertEq(wf, 0);
    }

    // ── Milestone escrow: abort after failure -> Disputed (mixed) ──

    function testMilestoneAbortRecordsDisputed() public {
        TaskEscrowFactory.CreateMilestoneParams[] memory ms = new TaskEscrowFactory.CreateMilestoneParams[](2);
        ms[0] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 0.5 ether, submissionDeadline: uint64(block.timestamp + 7 days)
        });
        ms[1] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 0.5 ether, submissionDeadline: uint64(block.timestamp + 14 days)
        });

        TaskEscrowFactory.CreateParams memory params = _defaultParams();
        params.submissionDeadline = uint64(block.timestamp + 14 days);
        params.milestones = ms;
        vm.prank(address(0xBEEF));
        (uint256 escrowId,) = factory.createEscrow(params);
        TaskEscrow escrow = TaskEscrow(factory.escrowById(escrowId));

        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        // Approve milestone 0
        vm.prank(worker);
        escrow.submitMilestone(0, keccak256("ms0"), "ipfs://ms0", bytes32(0));
        vm.prank(buyer);
        escrow.approveMilestoneByBuyer(0);

        // Timeout milestone 1
        vm.warp(block.timestamp + 15 days);
        vm.prank(buyer);
        escrow.claimMilestoneTimeoutRefund(1);

        // Abort remaining (none left, but escrow reaches terminal)
        // The escrow should already be settled since all milestones are terminal

        (uint32 wc, uint32 wd, uint32 wf) = factory.getWorkerReputation(worker);
        assertEq(wc, 0);
        assertEq(wd, 1, "worker disputed (mixed outcome)");
        assertEq(wf, 0);
    }
}
