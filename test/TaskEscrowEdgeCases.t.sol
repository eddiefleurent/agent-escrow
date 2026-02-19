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
        factory = new TaskEscrowFactory(FEE_BPS, treasury, owner);
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
        escrow.submit(keccak256("submission"), "ipfs://late");
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
            buyer,
            worker,
            verifier,
            arbitrator,
            AMOUNT,
            uint64(block.timestamp + 7 days),
            REVIEW,
            DISPUTE,
            keccak256("spec-2"),
            ARB_TIMEOUT
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

    function _fundAndSubmit() internal {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result");
    }

    function _createEscrow(uint64 submissionDeadline) internal returns (TaskEscrow created) {
        vm.prank(randomUser);
        (, address escrowAddress) = factory.createEscrow(
            buyer, worker, verifier, arbitrator, AMOUNT, submissionDeadline, REVIEW, DISPUTE, keccak256("spec"), ARB_TIMEOUT
        );
        return TaskEscrow(escrowAddress);
    }
}

