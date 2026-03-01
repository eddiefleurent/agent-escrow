// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Test} from "forge-std/Test.sol";
import {TaskEscrowFactory} from "../src/TaskEscrowFactory.sol";
import {TaskEscrow} from "../src/TaskEscrow.sol";

/// @notice Tests for tiered service levels (paper §5.3, roadmap item 17).
contract TaskEscrowTieredServiceTest is Test {
    TaskEscrowFactory internal factory;

    address internal owner = makeAddr("owner");
    address internal buyer = makeAddr("buyer");
    address internal worker = makeAddr("worker");
    address internal verifier = makeAddr("verifier");
    address internal arbitrator = makeAddr("arbitrator");
    address internal treasury = makeAddr("treasury");

    uint256 internal constant AMOUNT = 1 ether;
    uint16 internal constant LOW_FEE_BPS = 100; // 1%
    uint16 internal constant HIGH_FEE_BPS = 250; // 2.5%
    uint64 internal constant REVIEW = 86_400;
    uint64 internal constant DISPUTE = 172_800;
    uint64 internal constant ARB_TIMEOUT = 7 days;

    function setUp() public {
        factory = new TaskEscrowFactory(LOW_FEE_BPS, HIGH_FEE_BPS, treasury, owner);
        vm.deal(buyer, 100 ether);
        vm.deal(worker, 10 ether);
    }

    function _createEscrow(uint8 tier) internal returns (TaskEscrow) {
        (, address addr) = factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifierPanel: [verifier, address(0), address(0), address(0), address(0), address(0), address(0)],
                quorumThreshold: 1,
                quorumVerifierCount: 1,
                verifierStakePerVerifier: 0,
                arbitrator: arbitrator,
                amount: AMOUNT,
                workerStake: 0,
                submissionDeadline: uint64(block.timestamp + 7 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec-tiered"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                serviceTier: tier,
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                zkVerifier: address(0),
                circuitId: bytes32(0),
                parentEscrow: address(0),
                milestones: new TaskEscrowFactory.CreateMilestoneParams[](0)
            })
        );
        return TaskEscrow(addr);
    }

    function _fundAndSubmit(TaskEscrow e) internal {
        vm.prank(buyer);
        e.fund{value: AMOUNT}();
        vm.prank(worker);
        e.submit(keccak256("submission"), "ipfs://submission", bytes32(0));
    }

    // ── Factory tier constants ──

    function testFactoryTierConstants() public view {
        assertEq(factory.TIER_LOW_ASSURANCE(), 0);
        assertEq(factory.TIER_HIGH_ASSURANCE(), 1);
    }

    // ── Fee snapshot correctness ──

    function testLowAssuranceFeeSnapshot() public {
        TaskEscrow e = _createEscrow(0);
        assertEq(e.protocolFeeBpsSnapshot(), LOW_FEE_BPS);
    }

    function testHighAssuranceFeeSnapshot() public {
        TaskEscrow e = _createEscrow(1);
        assertEq(e.protocolFeeBpsSnapshot(), HIGH_FEE_BPS);
    }

    function testFeeSnapshotImmutableAfterCreation() public {
        TaskEscrow e = _createEscrow(1);
        assertEq(e.protocolFeeBpsSnapshot(), HIGH_FEE_BPS);

        vm.prank(owner);
        factory.setHighAssuranceFeeBps(500);

        assertEq(e.protocolFeeBpsSnapshot(), HIGH_FEE_BPS, "snapshot should not change after creation");
        assertEq(factory.highAssuranceFeeBps(), 500, "factory fee should have changed");
    }

    // ── Service tier stored correctly ──

    function testLowAssuranceTierStored() public {
        TaskEscrow e = _createEscrow(0);
        assertEq(e.serviceTier(), 0);
    }

    function testHighAssuranceTierStored() public {
        TaskEscrow e = _createEscrow(1);
        assertEq(e.serviceTier(), 1);
    }

    // ── Invalid tier rejection ──

    function testRevertOnInvalidTier() public {
        vm.expectRevert(TaskEscrowFactory.InvalidServiceTier.selector);
        factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifierPanel: [verifier, address(0), address(0), address(0), address(0), address(0), address(0)],
                quorumThreshold: 1,
                quorumVerifierCount: 1,
                verifierStakePerVerifier: 0,
                arbitrator: arbitrator,
                amount: AMOUNT,
                workerStake: 0,
                submissionDeadline: uint64(block.timestamp + 7 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec-bad-tier"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                serviceTier: 2,
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                zkVerifier: address(0),
                circuitId: bytes32(0),
                parentEscrow: address(0),
                milestones: new TaskEscrowFactory.CreateMilestoneParams[](0)
            })
        );
    }

    // ── Low-assurance: buyer approval works (unchanged behavior) ──

    function testLowAssuranceBuyerApprovalWorks() public {
        TaskEscrow e = _createEscrow(0);
        _fundAndSubmit(e);

        vm.prank(buyer);
        e.approveByBuyer();

        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Settled));
    }

    function testLowAssuranceVerifierApprovalAlsoWorks() public {
        TaskEscrow e = _createEscrow(0);
        _fundAndSubmit(e);

        vm.prank(verifier);
        e.castVerifierVote(true, "");

        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Settled));
    }

    // ── High-assurance: buyer approval blocked ──

    function testHighAssuranceBuyerApprovalReverts() public {
        TaskEscrow e = _createEscrow(1);
        _fundAndSubmit(e);

        vm.prank(buyer);
        vm.expectRevert(TaskEscrow.HighAssuranceRequiresVerifier.selector);
        e.approveByBuyer();
    }

    function testHighAssuranceVerifierApprovalWorks() public {
        TaskEscrow e = _createEscrow(1);
        _fundAndSubmit(e);

        vm.prank(verifier);
        e.castVerifierVote(true, "");

        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Settled));
    }

    // ── High-assurance: milestone buyer approval blocked ──

    function testHighAssuranceMilestoneBuyerApprovalReverts() public {
        TaskEscrowFactory.CreateMilestoneParams[] memory ms = new TaskEscrowFactory.CreateMilestoneParams[](2);
        ms[0] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 0.5 ether, submissionDeadline: uint64(block.timestamp + 3 days)
        });
        ms[1] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 0.5 ether, submissionDeadline: uint64(block.timestamp + 7 days)
        });

        (, address addr) = factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifierPanel: [verifier, address(0), address(0), address(0), address(0), address(0), address(0)],
                quorumThreshold: 1,
                quorumVerifierCount: 1,
                verifierStakePerVerifier: 0,
                arbitrator: arbitrator,
                amount: AMOUNT,
                workerStake: 0,
                submissionDeadline: uint64(block.timestamp + 7 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec-ms-high"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                serviceTier: 1,
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                zkVerifier: address(0),
                circuitId: bytes32(0),
                parentEscrow: address(0),
                milestones: ms
            })
        );
        TaskEscrow e = TaskEscrow(addr);

        vm.prank(buyer);
        e.fund{value: AMOUNT}();

        vm.prank(worker);
        e.submitMilestone(0, keccak256("ms0"), "ipfs://ms0", bytes32(0));

        vm.prank(buyer);
        vm.expectRevert(TaskEscrow.HighAssuranceRequiresVerifier.selector);
        e.approveMilestoneByBuyer(0);
    }

    function testHighAssuranceMilestoneVerifierApprovalWorks() public {
        TaskEscrowFactory.CreateMilestoneParams[] memory ms = new TaskEscrowFactory.CreateMilestoneParams[](2);
        ms[0] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 0.5 ether, submissionDeadline: uint64(block.timestamp + 3 days)
        });
        ms[1] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 0.5 ether, submissionDeadline: uint64(block.timestamp + 7 days)
        });

        (, address addr) = factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifierPanel: [verifier, address(0), address(0), address(0), address(0), address(0), address(0)],
                quorumThreshold: 1,
                quorumVerifierCount: 1,
                verifierStakePerVerifier: 0,
                arbitrator: arbitrator,
                amount: AMOUNT,
                workerStake: 0,
                submissionDeadline: uint64(block.timestamp + 7 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec-ms-high-v"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                serviceTier: 1,
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                zkVerifier: address(0),
                circuitId: bytes32(0),
                parentEscrow: address(0),
                milestones: ms
            })
        );
        TaskEscrow e = TaskEscrow(addr);

        vm.prank(buyer);
        e.fund{value: AMOUNT}();

        vm.prank(worker);
        e.submitMilestone(0, keccak256("ms0"), "ipfs://ms0", bytes32(0));

        vm.prank(verifier);
        e.castMilestoneVerifierVote(0, true, "");

        (,,,,,,,,, TaskEscrow.MilestoneStatus msStatus,) = e.milestones(0);
        assertEq(uint8(msStatus), uint8(TaskEscrow.MilestoneStatus.Approved));
    }

    // ── Fee settlement correctness ──

    function testHighAssuranceSettlementUsesHighFee() public {
        TaskEscrow e = _createEscrow(1);
        _fundAndSubmit(e);

        uint256 treasuryBefore = treasury.balance;

        vm.prank(verifier);
        e.castVerifierVote(true, "");

        uint256 expectedFee = AMOUNT * HIGH_FEE_BPS / 10_000;
        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Settled));
        assertEq(treasury.balance - treasuryBefore, expectedFee, "treasury should receive high-assurance fee");
    }

    function testLowAssuranceSettlementUsesLowFee() public {
        TaskEscrow e = _createEscrow(0);
        _fundAndSubmit(e);

        uint256 treasuryBefore = treasury.balance;

        vm.prank(buyer);
        e.approveByBuyer();

        uint256 expectedFee = AMOUNT * LOW_FEE_BPS / 10_000;
        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Settled));
        assertEq(treasury.balance - treasuryBefore, expectedFee, "treasury should receive low-assurance fee");
    }

    // ── EscrowCreated event includes tier ──

    function testEscrowCreatedEventIncludesTier() public {
        vm.expectEmit(true, false, true, false);
        emit TaskEscrowFactory.EscrowCreated(
            0,
            address(0),
            buyer,
            worker,
            1,
            1,
            arbitrator,
            keccak256("spec-event"),
            address(0),
            1,
            address(0),
            bytes32(0)
        );

        _createEscrow(1);
    }

    // ── setHighAssuranceFeeBps admin function ──

    function testSetHighAssuranceFeeBps() public {
        vm.prank(owner);
        factory.setHighAssuranceFeeBps(300);
        assertEq(factory.highAssuranceFeeBps(), 300);
    }

    function testSetHighAssuranceFeeBpsEmitsEvent() public {
        vm.prank(owner);
        vm.expectEmit(false, false, false, true);
        emit TaskEscrowFactory.HighAssuranceFeeUpdated(HIGH_FEE_BPS, 300);
        factory.setHighAssuranceFeeBps(300);
    }

    function testSetHighAssuranceFeeBpsOnlyOwner() public {
        vm.prank(buyer);
        vm.expectRevert(TaskEscrowFactory.Unauthorized.selector);
        factory.setHighAssuranceFeeBps(300);
    }

    function testSetHighAssuranceFeeBpsRejectsInvalid() public {
        vm.prank(owner);
        vm.expectRevert(TaskEscrowFactory.InvalidFeeBps.selector);
        factory.setHighAssuranceFeeBps(10_001);
    }
}
