// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Test} from "forge-std/Test.sol";
import {TaskEscrowFactory} from "../src/TaskEscrowFactory.sol";
import {TaskEscrow} from "../src/TaskEscrow.sol";

contract TaskEscrowMilestoneTest is Test {
    TaskEscrowFactory internal factory;

    address internal owner = makeAddr("owner");
    address internal buyer = makeAddr("buyer");
    address internal worker = makeAddr("worker");
    address internal verifier = makeAddr("verifier");
    address internal arbitrator = makeAddr("arbitrator");
    address internal treasury = makeAddr("treasury");

    uint256 internal constant TOTAL = 3 ether;
    uint256 internal constant STAKE = 0.3 ether;
    uint16 internal constant FEE_BPS = 100; // 1%
    uint64 internal constant REVIEW = 86_400;
    uint64 internal constant DISPUTE = 172_800;
    uint64 internal constant ARB_TIMEOUT = 7 days;

    function setUp() public {
        factory = new TaskEscrowFactory(FEE_BPS, treasury, owner);
        vm.deal(buyer, 100 ether);
        vm.deal(worker, 10 ether);
    }

    function _milestoneParams3() internal view returns (TaskEscrowFactory.CreateMilestoneParams[] memory ms) {
        ms = new TaskEscrowFactory.CreateMilestoneParams[](3);
        ms[0] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 1 ether, submissionDeadline: uint64(block.timestamp + 7 days)
        });
        ms[1] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 1 ether, submissionDeadline: uint64(block.timestamp + 14 days)
        });
        ms[2] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 1 ether, submissionDeadline: uint64(block.timestamp + 21 days)
        });
    }

    function _create3MilestoneEscrow() internal returns (TaskEscrow) {
        return _create3MilestoneEscrowWithStake(0);
    }

    function _create3MilestoneEscrowWithStake(uint256 stake) internal returns (TaskEscrow) {
        (, address addr) = factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifier: verifier,
                arbitrator: arbitrator,
                amount: TOTAL,
                workerStake: stake,
                submissionDeadline: uint64(block.timestamp + 21 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec-ms"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                milestones: _milestoneParams3()
            })
        );
        return TaskEscrow(addr);
    }

    function _fundEscrow(TaskEscrow e) internal {
        vm.prank(buyer);
        e.fund{value: TOTAL}();
    }

    function _submitMilestone(TaskEscrow e, uint8 idx) internal {
        vm.prank(worker);
        e.submitMilestone(idx, keccak256(abi.encodePacked("ms-", idx)), string(abi.encodePacked("ipfs://ms-", idx)));
    }

    function _approveMilestone(TaskEscrow e, uint8 idx) internal {
        vm.prank(buyer);
        e.approveMilestoneByBuyer(idx);
    }

    // ── Happy path: 3 milestones all approved ──

    function testMilestoneHappyPath3Approved() public {
        TaskEscrow e = _create3MilestoneEscrow();
        _fundEscrow(e);

        uint256 workerBefore = worker.balance;
        uint256 treasuryBefore = treasury.balance;

        for (uint8 i = 0; i < 3; i++) {
            _submitMilestone(e, i);
            _approveMilestone(e, i);
        }

        assertEq(uint256(e.status()), uint256(TaskEscrow.Status.Settled));
        uint256 totalFee = (TOTAL * FEE_BPS) / 10_000;
        assertEq(worker.balance, workerBefore + TOTAL - totalFee);
        assertEq(treasury.balance, treasuryBefore + totalFee);
        assertEq(address(e).balance, 0);
    }

    // ── Partial completion: approve 2 of 3, abort remaining ──

    function testMilestonePartialCompletionAbort() public {
        TaskEscrow e = _create3MilestoneEscrow();
        _fundEscrow(e);

        // Approve milestones 0 and 1
        _submitMilestone(e, 0);
        _approveMilestone(e, 0);
        _submitMilestone(e, 1);
        _approveMilestone(e, 1);

        // Milestone 2 times out
        vm.warp(block.timestamp + 22 days);
        vm.prank(buyer);
        e.claimMilestoneTimeoutRefund(2);

        // Now abort remaining (none left since all are terminal)
        // Escrow should already be settled since all 3 milestones are terminal
        assertEq(uint256(e.status()), uint256(TaskEscrow.Status.Settled));
        assertEq(address(e).balance, 0);
    }

    // ── Dispute on milestone 2: resolve with split, abort milestone 3 ──

    function testMilestoneDisputeResolveAbort() public {
        TaskEscrow e = _create3MilestoneEscrow();
        _fundEscrow(e);

        // Approve milestone 0
        _submitMilestone(e, 0);
        _approveMilestone(e, 0);

        // Dispute milestone 1
        _submitMilestone(e, 1);
        vm.prank(buyer);
        e.disputeMilestone(1, "ipfs://dispute-ms1");

        // Resolve with 50/50 split
        vm.prank(arbitrator);
        e.resolveMilestoneDispute(1, 5000, "ipfs://resolve-ms1");

        // Abort remaining milestones (milestone 2 is Pending)
        vm.prank(buyer);
        e.abortRemainingMilestones();

        assertEq(uint256(e.status()), uint256(TaskEscrow.Status.Refunded));
        assertEq(address(e).balance, 0);
    }

    // ── Timeout on milestone 0: refund, abort remaining ──

    function testMilestoneTimeoutAbortRemaining() public {
        TaskEscrow e = _create3MilestoneEscrow();
        _fundEscrow(e);

        // Milestone 0 times out
        vm.warp(block.timestamp + 8 days);
        vm.prank(buyer);
        e.claimMilestoneTimeoutRefund(0);

        // Abort remaining milestones
        vm.prank(buyer);
        e.abortRemainingMilestones();

        assertEq(uint256(e.status()), uint256(TaskEscrow.Status.Refunded));
        assertEq(address(e).balance, 0);
    }

    // ── Arbitrator timeout on a milestone ──

    function testMilestoneArbitratorTimeoutAbort() public {
        TaskEscrow e = _create3MilestoneEscrow();
        _fundEscrow(e);

        _submitMilestone(e, 0);
        vm.prank(buyer);
        e.disputeMilestone(0, "ipfs://dispute");

        vm.warp(block.timestamp + ARB_TIMEOUT + 1);
        vm.prank(buyer);
        e.claimMilestoneArbitratorTimeout(0);

        // Abort remaining
        vm.prank(buyer);
        e.abortRemainingMilestones();

        assertEq(uint256(e.status()), uint256(TaskEscrow.Status.Refunded));
        assertEq(address(e).balance, 0);
    }

    // ── Permission failures ──

    function testMilestoneSubmitOnlyWorker() public {
        TaskEscrow e = _create3MilestoneEscrow();
        _fundEscrow(e);

        vm.expectRevert(TaskEscrow.Unauthorized.selector);
        vm.prank(buyer);
        e.submitMilestone(0, keccak256("test"), "ipfs://test");
    }

    function testMilestoneApproveOnlyBuyerOrVerifier() public {
        TaskEscrow e = _create3MilestoneEscrow();
        _fundEscrow(e);
        _submitMilestone(e, 0);

        vm.expectRevert(TaskEscrow.Unauthorized.selector);
        vm.prank(worker);
        e.approveMilestoneByBuyer(0);
    }

    function testMilestoneDisputeOnlyBuyer() public {
        TaskEscrow e = _create3MilestoneEscrow();
        _fundEscrow(e);
        _submitMilestone(e, 0);

        vm.expectRevert(TaskEscrow.Unauthorized.selector);
        vm.prank(worker);
        e.disputeMilestone(0, "ipfs://reason");
    }

    function testMilestoneResolveOnlyArbitrator() public {
        TaskEscrow e = _create3MilestoneEscrow();
        _fundEscrow(e);
        _submitMilestone(e, 0);
        vm.prank(buyer);
        e.disputeMilestone(0, "ipfs://reason");

        vm.expectRevert(TaskEscrow.Unauthorized.selector);
        vm.prank(buyer);
        e.resolveMilestoneDispute(0, 5000, "ipfs://resolve");
    }

    function testMilestoneAbortOnlyBuyer() public {
        TaskEscrow e = _create3MilestoneEscrow();
        _fundEscrow(e);
        _submitMilestone(e, 0);
        vm.prank(buyer);
        e.disputeMilestone(0, "ipfs://reason");
        vm.prank(arbitrator);
        e.resolveMilestoneDispute(0, 5000, "ipfs://resolve");

        vm.expectRevert(TaskEscrow.Unauthorized.selector);
        vm.prank(worker);
        e.abortRemainingMilestones();
    }

    // ── Wrong milestone index ──

    function testMilestoneWrongIndexReverts() public {
        TaskEscrow e = _create3MilestoneEscrow();
        _fundEscrow(e);

        vm.expectRevert(TaskEscrow.InvalidMilestoneIndex.selector);
        vm.prank(worker);
        e.submitMilestone(1, keccak256("test"), "ipfs://test");
    }

    // ── Cannot use V1 functions on multi-milestone escrow ──

    function testMilestoneV1SubmitReverts() public {
        TaskEscrow e = _create3MilestoneEscrow();
        _fundEscrow(e);

        // V1 submit() on a multi-milestone escrow succeeds at the escrow level
        // but only syncs milestone[0] when milestoneCount == 1. For multi-milestone
        // escrows, callers should use submitMilestone(). Verify that V1 submit
        // does NOT advance milestone state for multi-milestone escrows.
        vm.prank(worker);
        e.submit(keccak256("v1-sub"), "ipfs://v1-sub");

        // Escrow-level status transitions to Submitted
        assertEq(uint256(e.status()), uint256(TaskEscrow.Status.Submitted));

        // Milestone 0 should remain Pending because _syncMs0Submit only fires when milestoneCount == 1
        (,,,,,,,, TaskEscrow.MilestoneStatus ms0Status,) = e.milestones(0);
        assertEq(uint256(ms0Status), uint256(TaskEscrow.MilestoneStatus.Pending));

        // Milestone 1 and 2 also remain Pending
        (,,,,,,,, TaskEscrow.MilestoneStatus ms1Status,) = e.milestones(1);
        assertEq(uint256(ms1Status), uint256(TaskEscrow.MilestoneStatus.Pending));
    }

    // ── Abort requires terminal failure state ──

    function testMilestoneAbortRequiresTerminalState() public {
        TaskEscrow e = _create3MilestoneEscrow();
        _fundEscrow(e);

        // Current milestone is Pending, abort should fail
        vm.expectRevert(TaskEscrow.InvalidState.selector);
        vm.prank(buyer);
        e.abortRemainingMilestones();
    }

    function testMilestoneAbortRequiresTerminalStateSubmitted() public {
        TaskEscrow e = _create3MilestoneEscrow();
        _fundEscrow(e);
        _submitMilestone(e, 0);

        // Current milestone is Submitted, abort should fail
        vm.expectRevert(TaskEscrow.InvalidState.selector);
        vm.prank(buyer);
        e.abortRemainingMilestones();
    }

    // ── Worker stake settlement across mixed milestone outcomes ──

    function testMilestoneWorkerStakeMixedOutcomes() public {
        TaskEscrow e = _create3MilestoneEscrowWithStake(STAKE);

        _fundEscrow(e);
        vm.prank(worker);
        e.depositStake{value: STAKE}();

        uint256 buyerBefore = buyer.balance;
        uint256 workerBefore = worker.balance;
        uint256 treasuryBefore = treasury.balance;

        // Approve milestone 0
        _submitMilestone(e, 0);
        _approveMilestone(e, 0);

        // Dispute milestone 1, resolve 0 bps to worker
        _submitMilestone(e, 1);
        vm.prank(buyer);
        e.disputeMilestone(1, "ipfs://dispute");
        vm.prank(arbitrator);
        e.resolveMilestoneDispute(1, 0, "ipfs://resolve");

        // Abort milestone 2
        vm.prank(buyer);
        e.abortRemainingMilestones();

        assertEq(uint256(e.status()), uint256(TaskEscrow.Status.Refunded));

        // Stake proportional to approved amounts: 1/3 of total was approved
        // stakeReturn = STAKE * 1/3 = 0.1 ether
        uint256 totalOut =
            (worker.balance - workerBefore) + (buyer.balance - buyerBefore) + (treasury.balance - treasuryBefore);
        assertEq(totalOut, TOTAL + STAKE);
        assertEq(address(e).balance, 0);
    }

    // ── Single-milestone escrow behaves identically to V1 ──

    function testSingleMilestoneIdenticalToV1() public {
        TaskEscrowFactory.CreateMilestoneParams[] memory ms = new TaskEscrowFactory.CreateMilestoneParams[](1);
        ms[0] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 1 ether, submissionDeadline: uint64(block.timestamp + 7 days)
        });

        (, address addr) = factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifier: verifier,
                arbitrator: arbitrator,
                amount: 1 ether,
                workerStake: 0,
                submissionDeadline: uint64(block.timestamp + 7 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec-single"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                milestones: ms
            })
        );
        TaskEscrow e = TaskEscrow(addr);

        assertEq(e.milestoneCount(), 1);

        // Use V1 functions
        vm.prank(buyer);
        e.fund{value: 1 ether}();

        vm.prank(worker);
        e.submit(keccak256("submission"), "ipfs://result");

        uint256 workerBefore = worker.balance;
        uint256 treasuryBefore = treasury.balance;

        vm.prank(buyer);
        e.approveByBuyer();

        uint256 msAmount = 1 ether;
        uint256 fee = (msAmount * FEE_BPS) / 10_000;
        assertEq(uint256(e.status()), uint256(TaskEscrow.Status.Settled));
        assertEq(worker.balance, workerBefore + msAmount - fee);
        assertEq(treasury.balance, treasuryBefore + fee);
        assertEq(address(e).balance, 0);
    }

    // ── Fee calculation correctness per-milestone ──

    function testMilestoneFeeCalculation() public {
        TaskEscrow e = _create3MilestoneEscrow();
        _fundEscrow(e);

        uint256 workerBefore = worker.balance;
        uint256 treasuryBefore = treasury.balance;

        _submitMilestone(e, 0);
        _approveMilestone(e, 0);

        uint256 msAmt = 1 ether;
        uint256 msFee = (msAmt * FEE_BPS) / 10_000;
        assertEq(worker.balance, workerBefore + 1 ether - msFee);
        assertEq(treasury.balance, treasuryBefore + msFee);
    }

    // ── Verifier approve and reject on milestones ──

    function testMilestoneApproveByVerifier() public {
        TaskEscrow e = _create3MilestoneEscrow();
        _fundEscrow(e);
        _submitMilestone(e, 0);

        vm.prank(verifier);
        e.approveMilestoneByVerifier(0);

        (,,,,,,,, TaskEscrow.MilestoneStatus msStatus,) = e.milestones(0);
        assertEq(uint256(msStatus), uint256(TaskEscrow.MilestoneStatus.Approved));
    }

    function testMilestoneRejectByVerifier() public {
        TaskEscrow e = _create3MilestoneEscrow();
        _fundEscrow(e);
        _submitMilestone(e, 0);

        vm.prank(verifier);
        e.rejectMilestoneByVerifier(0, "ipfs://reject");

        (,,,,,,,, TaskEscrow.MilestoneStatus msStatus,) = e.milestones(0);
        assertEq(uint256(msStatus), uint256(TaskEscrow.MilestoneStatus.Disputed));
    }

    // ── Silence escalation on milestone ──

    function testMilestoneSilenceEscalation() public {
        TaskEscrow e = _create3MilestoneEscrow();
        _fundEscrow(e);
        _submitMilestone(e, 0);

        vm.warp(block.timestamp + REVIEW + 1);
        vm.prank(worker);
        e.escalateMilestoneSilence(0, "ipfs://silence");

        (,,,,,,,, TaskEscrow.MilestoneStatus msStatus,) = e.milestones(0);
        assertEq(uint256(msStatus), uint256(TaskEscrow.MilestoneStatus.Disputed));
    }

    // ── Creation constraints ──

    function testTooManyMilestonesReverts() public {
        TaskEscrowFactory.CreateMilestoneParams[] memory ms = new TaskEscrowFactory.CreateMilestoneParams[](17);
        for (uint256 i = 0; i < 17; i++) {
            ms[i] = TaskEscrowFactory.CreateMilestoneParams({
                amount: 1 ether, submissionDeadline: uint64(block.timestamp + (i + 1) * 1 days)
            });
        }

        vm.expectRevert(TaskEscrow.TooManyMilestones.selector);
        factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifier: verifier,
                arbitrator: arbitrator,
                amount: 17 ether,
                workerStake: 0,
                submissionDeadline: uint64(block.timestamp + 17 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                milestones: ms
            })
        );
    }

    function testMilestoneAmountMismatchReverts() public {
        TaskEscrowFactory.CreateMilestoneParams[] memory ms = new TaskEscrowFactory.CreateMilestoneParams[](2);
        ms[0] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 1 ether, submissionDeadline: uint64(block.timestamp + 7 days)
        });
        ms[1] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 1 ether, submissionDeadline: uint64(block.timestamp + 14 days)
        });

        vm.expectRevert(TaskEscrow.MilestoneAmountMismatch.selector);
        factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifier: verifier,
                arbitrator: arbitrator,
                amount: 3 ether, // Doesn't match 2 ether total
                workerStake: 0,
                submissionDeadline: uint64(block.timestamp + 14 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                milestones: ms
            })
        );
    }

    function testMilestoneDeadlineOrderReverts() public {
        TaskEscrowFactory.CreateMilestoneParams[] memory ms = new TaskEscrowFactory.CreateMilestoneParams[](2);
        ms[0] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 1 ether, submissionDeadline: uint64(block.timestamp + 14 days)
        });
        ms[1] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 1 ether,
            submissionDeadline: uint64(block.timestamp + 7 days) // Before ms[0]
        });

        vm.expectRevert(TaskEscrow.InvalidMilestoneDeadlineOrder.selector);
        factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifier: verifier,
                arbitrator: arbitrator,
                amount: 2 ether,
                workerStake: 0,
                submissionDeadline: uint64(block.timestamp + 14 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                milestones: ms
            })
        );
    }

    // ── Fuzz: conservation of funds across milestones ──

    function testFuzz_MilestoneConservation(uint16 bps0, uint16 bps1) public {
        vm.assume(bps0 <= 10_000);
        vm.assume(bps1 <= 10_000);

        TaskEscrow e = _create3MilestoneEscrow();
        _fundEscrow(e);

        uint256 buyerBefore = buyer.balance;
        uint256 workerBefore = worker.balance;
        uint256 treasuryBefore = treasury.balance;

        // Dispute and resolve milestone 0
        _submitMilestone(e, 0);
        vm.prank(buyer);
        e.disputeMilestone(0, "ipfs://d0");
        vm.prank(arbitrator);
        e.resolveMilestoneDispute(0, bps0, "ipfs://r0");

        // Dispute and resolve milestone 1
        _submitMilestone(e, 1);
        vm.prank(buyer);
        e.disputeMilestone(1, "ipfs://d1");
        vm.prank(arbitrator);
        e.resolveMilestoneDispute(1, bps1, "ipfs://r1");

        // Abort milestone 2
        vm.prank(buyer);
        e.abortRemainingMilestones();

        uint256 totalOut =
            (worker.balance - workerBefore) + (buyer.balance - buyerBefore) + (treasury.balance - treasuryBefore);
        assertEq(totalOut, TOTAL);
        assertEq(address(e).balance, 0);
    }

    // ── Max 16 milestones works ──

    function testMax16MilestonesWorks() public {
        TaskEscrowFactory.CreateMilestoneParams[] memory ms = new TaskEscrowFactory.CreateMilestoneParams[](16);
        for (uint256 i = 0; i < 16; i++) {
            ms[i] = TaskEscrowFactory.CreateMilestoneParams({
                amount: 0.1 ether, submissionDeadline: uint64(block.timestamp + (i + 1) * 1 days)
            });
        }

        (, address addr) = factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifier: verifier,
                arbitrator: arbitrator,
                amount: 1.6 ether,
                workerStake: 0,
                submissionDeadline: uint64(block.timestamp + 16 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec-16"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                milestones: ms
            })
        );
        TaskEscrow e = TaskEscrow(addr);
        assertEq(e.milestoneCount(), 16);
    }

    // ── Worker stake with all milestones approved ──

    function testMilestoneWorkerStakeAllApproved() public {
        TaskEscrow e = _create3MilestoneEscrowWithStake(STAKE);
        _fundEscrow(e);

        vm.prank(worker);
        e.depositStake{value: STAKE}();

        uint256 workerBefore = worker.balance;

        for (uint8 i = 0; i < 3; i++) {
            _submitMilestone(e, i);
            _approveMilestone(e, i);
        }

        assertEq(uint256(e.status()), uint256(TaskEscrow.Status.Settled));
        // Worker should get full stake back
        uint256 totalFee = (TOTAL * FEE_BPS) / 10_000;
        assertEq(worker.balance, workerBefore + TOTAL - totalFee + STAKE);
        assertEq(address(e).balance, 0);
    }
}
