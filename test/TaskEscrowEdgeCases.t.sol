// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Test} from "forge-std/Test.sol";
import {TaskEscrowFactory} from "../src/TaskEscrowFactory.sol";
import {TaskEscrow} from "../src/TaskEscrow.sol";

contract TaskEscrowEdgeCasesTest is Test {
    TaskEscrowFactory internal factory;
    TaskEscrow internal escrow;

    address internal owner = makeAddr("owner");
    address internal buyer = makeAddr("buyer");
    address internal worker = makeAddr("worker");
    address internal verifier = makeAddr("verifier");
    address internal arbitrator = makeAddr("arbitrator");
    address internal treasury = makeAddr("treasury");
    address internal randomUser = makeAddr("randomUser");

    uint256 internal constant AMOUNT = 1 ether;
    uint16 internal constant FEE_BPS = 100; // 1%
    uint64 internal constant REVIEW = 86_400;
    uint64 internal constant DISPUTE = 172_800;
    uint64 internal constant ARB_TIMEOUT = 7 days;

    function setUp() public {
        factory = new TaskEscrowFactory(FEE_BPS, FEE_BPS, treasury, owner);
        escrow = _createEscrow(uint64(block.timestamp + 7 days));

        vm.deal(buyer, 10 ether);
        vm.deal(worker, 1 ether);
    }

    function testCannotSubmitAfterDeadline() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.warp(block.timestamp + 8 days);
        vm.expectRevert(TaskEscrow.WindowExpired.selector);
        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://late", bytes32(0));
    }

    function testCannotApproveAfterReviewWindow() public {
        _fundAndSubmit();
        vm.warp(block.timestamp + REVIEW + 1);

        vm.expectRevert(TaskEscrow.WindowExpired.selector);
        vm.prank(buyer);
        escrow.approveByBuyer();
    }

    function testCannotEscalateBeforeReviewWindowEnds() public {
        _fundAndSubmit();

        vm.expectRevert(TaskEscrow.WindowNotOpen.selector);
        vm.prank(worker);
        escrow.escalateSilence("ipfs://early");
    }

    function testCannotEscalateAfterDisputeWindowEnds() public {
        _fundAndSubmit();
        vm.warp(block.timestamp + REVIEW + DISPUTE + 1);

        vm.expectRevert(TaskEscrow.WindowExpired.selector);
        vm.prank(worker);
        escrow.escalateSilence("ipfs://too-late");
    }

    function testInvalidWorkerAwardBpsReverts() public {
        _fundAndSubmit();
        vm.prank(buyer);
        escrow.dispute("ipfs://reason");

        vm.expectRevert(TaskEscrow.InvalidAwardBps.selector);
        vm.prank(arbitrator);
        escrow.resolveDispute(10_001, "ipfs://resolution");
    }

    function testOnlyArbitratorCanResolveDispute() public {
        _fundAndSubmit();
        vm.prank(buyer);
        escrow.dispute("ipfs://reason");

        vm.expectRevert(TaskEscrow.Unauthorized.selector);
        vm.prank(randomUser);
        escrow.resolveDispute(5_000, "ipfs://resolution");
    }

    function testOnlyOwnerCanChangeFactoryConfig() public {
        vm.expectRevert(TaskEscrowFactory.Unauthorized.selector);
        vm.prank(randomUser);
        factory.setProtocolFeeBps(150);

        vm.expectRevert(TaskEscrowFactory.Unauthorized.selector);
        vm.prank(randomUser);
        factory.setTreasury(makeAddr("newTreasury"));

        vm.expectRevert(TaskEscrowFactory.Unauthorized.selector);
        vm.prank(randomUser);
        factory.setPaused(true);
    }

    function testFactoryPauseBlocksCreateEscrow() public {
        vm.prank(owner);
        factory.setPaused(true);

        vm.expectRevert(TaskEscrowFactory.Paused.selector);
        vm.prank(randomUser);
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
                taskSpecHash: keccak256("spec-2"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                serviceTier: 0,
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                zkVerifier: address(0),
                circuitId: bytes32(0),
                parentEscrow: address(0),
                milestones: new TaskEscrowFactory.CreateMilestoneParams[](0)
            })
        );
    }

    function testFuzz_DisputeSettlementConservesFunds(uint16 workerAwardBps) public {
        vm.assume(workerAwardBps <= 10_000);
        _fundAndSubmit();
        vm.prank(buyer);
        escrow.dispute("ipfs://reason");

        uint256 buyerBefore = buyer.balance;
        uint256 workerBefore = worker.balance;
        uint256 treasuryBefore = treasury.balance;

        vm.prank(arbitrator);
        escrow.resolveDispute(workerAwardBps, "ipfs://resolution");

        uint256 workerGross = (AMOUNT * workerAwardBps) / 10_000;
        uint256 fee = (workerGross * FEE_BPS) / 10_000;
        uint256 workerNet = workerGross - fee;
        uint256 buyerRefund = AMOUNT - workerGross;

        assertEq(worker.balance, workerBefore + workerNet);
        assertEq(buyer.balance, buyerBefore + buyerRefund);
        assertEq(treasury.balance, treasuryBefore + fee);
        assertEq(address(escrow).balance, 0);
    }

    function testFuzz_ArbitratorTimeoutBoundary(uint256 warpDelta) public {
        vm.assume(warpDelta <= 30 days);
        _fundAndSubmit();
        vm.prank(buyer);
        escrow.dispute("ipfs://reason");

        uint256 disputeTime = block.timestamp;
        vm.warp(disputeTime + warpDelta);

        if (warpDelta <= ARB_TIMEOUT) {
            vm.expectRevert(TaskEscrow.ArbitratorTimeoutNotReached.selector);
            vm.prank(buyer);
            escrow.claimArbitratorTimeout();
        } else {
            vm.prank(buyer);
            escrow.claimArbitratorTimeout();
            assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Refunded));
        }
    }

    // ── Role address distinctness checks ──

    function testRolesNotDistinct_BuyerEqualsWorker() public {
        vm.expectRevert(TaskEscrow.RolesNotDistinct.selector);
        vm.prank(randomUser);
        factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: buyer,
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
                taskSpecHash: keccak256("spec"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                serviceTier: 0,
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                zkVerifier: address(0),
                circuitId: bytes32(0),
                parentEscrow: address(0),
                milestones: new TaskEscrowFactory.CreateMilestoneParams[](0)
            })
        );
    }

    function testRolesNotDistinct_BuyerEqualsVerifier() public {
        vm.expectRevert(TaskEscrow.RolesNotDistinct.selector);
        vm.prank(randomUser);
        factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifierPanel: [buyer, address(0), address(0), address(0), address(0), address(0), address(0)],
                quorumThreshold: 1,
                quorumVerifierCount: 1,
                verifierStakePerVerifier: 0,
                arbitrator: arbitrator,
                amount: AMOUNT,
                workerStake: 0,
                submissionDeadline: uint64(block.timestamp + 7 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                serviceTier: 0,
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                zkVerifier: address(0),
                circuitId: bytes32(0),
                parentEscrow: address(0),
                milestones: new TaskEscrowFactory.CreateMilestoneParams[](0)
            })
        );
    }

    function testRolesNotDistinct_BuyerEqualsArbitrator() public {
        vm.expectRevert(TaskEscrow.RolesNotDistinct.selector);
        vm.prank(randomUser);
        factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifierPanel: [verifier, address(0), address(0), address(0), address(0), address(0), address(0)],
                quorumThreshold: 1,
                quorumVerifierCount: 1,
                verifierStakePerVerifier: 0,
                arbitrator: buyer,
                amount: AMOUNT,
                workerStake: 0,
                submissionDeadline: uint64(block.timestamp + 7 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                serviceTier: 0,
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                zkVerifier: address(0),
                circuitId: bytes32(0),
                parentEscrow: address(0),
                milestones: new TaskEscrowFactory.CreateMilestoneParams[](0)
            })
        );
    }

    function testRolesNotDistinct_WorkerEqualsVerifier() public {
        vm.expectRevert(TaskEscrow.RolesNotDistinct.selector);
        vm.prank(randomUser);
        factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifierPanel: [worker, address(0), address(0), address(0), address(0), address(0), address(0)],
                quorumThreshold: 1,
                quorumVerifierCount: 1,
                verifierStakePerVerifier: 0,
                arbitrator: arbitrator,
                amount: AMOUNT,
                workerStake: 0,
                submissionDeadline: uint64(block.timestamp + 7 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                serviceTier: 0,
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                zkVerifier: address(0),
                circuitId: bytes32(0),
                parentEscrow: address(0),
                milestones: new TaskEscrowFactory.CreateMilestoneParams[](0)
            })
        );
    }

    function testRolesNotDistinct_WorkerEqualsArbitrator() public {
        vm.expectRevert(TaskEscrow.RolesNotDistinct.selector);
        vm.prank(randomUser);
        factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifierPanel: [verifier, address(0), address(0), address(0), address(0), address(0), address(0)],
                quorumThreshold: 1,
                quorumVerifierCount: 1,
                verifierStakePerVerifier: 0,
                arbitrator: worker,
                amount: AMOUNT,
                workerStake: 0,
                submissionDeadline: uint64(block.timestamp + 7 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                serviceTier: 0,
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                zkVerifier: address(0),
                circuitId: bytes32(0),
                parentEscrow: address(0),
                milestones: new TaskEscrowFactory.CreateMilestoneParams[](0)
            })
        );
    }

    function testRolesNotDistinct_VerifierEqualsArbitrator() public {
        vm.expectRevert(TaskEscrow.RolesNotDistinct.selector);
        vm.prank(randomUser);
        factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifierPanel: [verifier, address(0), address(0), address(0), address(0), address(0), address(0)],
                quorumThreshold: 1,
                quorumVerifierCount: 1,
                verifierStakePerVerifier: 0,
                arbitrator: verifier,
                amount: AMOUNT,
                workerStake: 0,
                submissionDeadline: uint64(block.timestamp + 7 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                serviceTier: 0,
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                zkVerifier: address(0),
                circuitId: bytes32(0),
                parentEscrow: address(0),
                milestones: new TaskEscrowFactory.CreateMilestoneParams[](0)
            })
        );
    }

    // ── Two-step ownership transfer ──

    function testTwoStepOwnershipTransfer() public {
        address newOwner = makeAddr("newOwner");

        vm.prank(owner);
        factory.transferOwnership(newOwner);

        assertEq(factory.pendingOwner(), newOwner);
        assertEq(factory.owner(), owner);

        vm.prank(newOwner);
        factory.acceptOwnership();

        assertEq(factory.owner(), newOwner);
        assertEq(factory.pendingOwner(), address(0));
    }

    function testTransferOwnershipToZeroReverts() public {
        vm.expectRevert(TaskEscrowFactory.InvalidAddress.selector);
        vm.prank(owner);
        factory.transferOwnership(address(0));
    }

    function testTransferOwnershipOnlyOwner() public {
        vm.expectRevert(TaskEscrowFactory.Unauthorized.selector);
        vm.prank(randomUser);
        factory.transferOwnership(makeAddr("newOwner"));
    }

    function testAcceptOwnershipOnlyPendingOwner() public {
        address newOwner = makeAddr("newOwner");
        vm.prank(owner);
        factory.transferOwnership(newOwner);

        vm.expectRevert(TaskEscrowFactory.Unauthorized.selector);
        vm.prank(randomUser);
        factory.acceptOwnership();
    }

    function testAcceptOwnershipWithoutPendingReverts() public {
        vm.expectRevert(TaskEscrowFactory.Unauthorized.selector);
        vm.prank(randomUser);
        factory.acceptOwnership();
    }

    function testNewOwnerCanAdminister() public {
        address newOwner = makeAddr("newOwner");

        vm.prank(owner);
        factory.transferOwnership(newOwner);
        vm.prank(newOwner);
        factory.acceptOwnership();

        vm.prank(newOwner);
        factory.setProtocolFeeBps(50);
        assertEq(factory.protocolFeeBps(), 50);

        vm.expectRevert(TaskEscrowFactory.Unauthorized.selector);
        vm.prank(owner);
        factory.setProtocolFeeBps(50);
    }

    function testTransferOwnershipOverwritesPending() public {
        address newOwner1 = makeAddr("newOwner1");
        address newOwner2 = makeAddr("newOwner2");

        vm.prank(owner);
        factory.transferOwnership(newOwner1);
        assertEq(factory.pendingOwner(), newOwner1);

        vm.prank(owner);
        factory.transferOwnership(newOwner2);
        assertEq(factory.pendingOwner(), newOwner2);

        vm.expectRevert(TaskEscrowFactory.Unauthorized.selector);
        vm.prank(newOwner1);
        factory.acceptOwnership();

        vm.prank(newOwner2);
        factory.acceptOwnership();
        assertEq(factory.owner(), newOwner2);
    }

    function _fundAndSubmit() internal {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result", bytes32(0));
    }

    function _createEscrow(uint64 submissionDeadline) internal returns (TaskEscrow created) {
        return _createEscrowWithToken(submissionDeadline, address(0));
    }

    function _createEscrowWithToken(uint64 submissionDeadline, address tokenAddr)
        internal
        returns (TaskEscrow created)
    {
        vm.prank(randomUser);
        (, address escrowAddress) = factory.createEscrow(
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
                submissionDeadline: submissionDeadline,
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: tokenAddr,
                serviceTier: 0,
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                zkVerifier: address(0),
                circuitId: bytes32(0),
                parentEscrow: address(0),
                milestones: new TaskEscrowFactory.CreateMilestoneParams[](0)
            })
        );
        return TaskEscrow(escrowAddress);
    }
}

