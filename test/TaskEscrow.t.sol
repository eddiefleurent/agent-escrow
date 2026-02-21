// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Test} from "forge-std/Test.sol";
import {TaskEscrowFactory} from "../src/TaskEscrowFactory.sol";
import {TaskEscrow} from "../src/TaskEscrow.sol";

contract TaskEscrowTest is Test {
    TaskEscrowFactory internal factory;
    TaskEscrow internal escrow;

    address internal owner = makeAddr("owner");
    address internal buyer = makeAddr("buyer");
    address internal worker = makeAddr("worker");
    address internal verifier = makeAddr("verifier");
    address internal arbitrator = makeAddr("arbitrator");
    address internal treasury = makeAddr("treasury");

    uint256 internal constant AMOUNT = 1 ether;
    uint16 internal constant FEE_BPS = 100; // 1%
    uint64 internal constant REVIEW = 86_400;
    uint64 internal constant DISPUTE = 172_800;
    uint64 internal constant ARB_TIMEOUT = 7 days;

    function setUp() public {
        factory = new TaskEscrowFactory(FEE_BPS, treasury, owner);

        vm.prank(address(0xBEEF));
        (, address escrowAddr) = factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifier: verifier,
                arbitrator: arbitrator,
                amount: AMOUNT,
                submissionDeadline: uint64(block.timestamp + 7 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0)
            })
        );
        escrow = TaskEscrow(escrowAddr);

        vm.deal(buyer, 10 ether);
        vm.deal(worker, 1 ether);
    }

    function testHappyPathApproveByBuyer() public {
        uint256 treasuryBefore = treasury.balance;
        uint256 workerBefore = worker.balance;

        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result");

        vm.prank(buyer);
        escrow.approveByBuyer();

        uint256 fee = (AMOUNT * FEE_BPS) / 10_000;
        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Settled));
        assertEq(worker.balance, workerBefore + (AMOUNT - fee));
        assertEq(treasury.balance, treasuryBefore + fee);
        assertEq(address(escrow).balance, 0);
    }

    function testDisputeResolvedSplit50_50() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result");

        vm.prank(buyer);
        escrow.dispute("ipfs://reason");

        uint256 treasuryBefore = treasury.balance;
        uint256 buyerBefore = buyer.balance;
        uint256 workerBefore = worker.balance;

        vm.prank(arbitrator);
        escrow.resolveDispute(5000, "ipfs://resolution");

        uint256 workerGross = (AMOUNT * 5000) / 10_000;
        uint256 fee = (workerGross * FEE_BPS) / 10_000;
        uint256 workerNet = workerGross - fee;
        uint256 buyerRefund = AMOUNT - workerGross;

        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Settled));
        assertEq(worker.balance, workerBefore + workerNet);
        assertEq(buyer.balance, buyerBefore + buyerRefund);
        assertEq(treasury.balance, treasuryBefore + fee);
        assertEq(address(escrow).balance, 0);
    }

    function testTimeoutRefund() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.warp(block.timestamp + 8 days);
        uint256 buyerBefore = buyer.balance;

        vm.prank(buyer);
        escrow.claimTimeoutRefund();

        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Refunded));
        assertEq(buyer.balance, buyerBefore + AMOUNT);
        assertEq(address(escrow).balance, 0);
    }

    function testVerifierRejectPath() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result");

        vm.prank(verifier);
        escrow.rejectByVerifier("ipfs://reject");

        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Disputed));
    }

    function testWorkerSilenceEscalationPath() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result");

        vm.warp(block.timestamp + REVIEW + 1);
        vm.prank(worker);
        escrow.escalateSilence("ipfs://silence");

        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Disputed));
    }

    function testArbitratorTimeoutClaim() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result");

        vm.prank(buyer);
        escrow.dispute("ipfs://reason");

        vm.warp(block.timestamp + ARB_TIMEOUT + 1);
        uint256 buyerBefore = buyer.balance;

        vm.prank(buyer);
        escrow.claimArbitratorTimeout();

        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Refunded));
        assertEq(buyer.balance, buyerBefore + AMOUNT);
        assertEq(address(escrow).balance, 0);
    }

    function testArbitratorTimeoutTooEarlyReverts() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result");

        vm.prank(buyer);
        escrow.dispute("ipfs://reason");

        vm.warp(block.timestamp + ARB_TIMEOUT);

        vm.expectRevert(TaskEscrow.ArbitratorTimeoutNotReached.selector);
        vm.prank(buyer);
        escrow.claimArbitratorTimeout();
    }

    function testArbitratorTimeoutWrongRoleReverts() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result");

        vm.prank(buyer);
        escrow.dispute("ipfs://reason");

        vm.warp(block.timestamp + ARB_TIMEOUT + 1);

        vm.expectRevert(TaskEscrow.Unauthorized.selector);
        vm.prank(worker);
        escrow.claimArbitratorTimeout();
    }

    function testOnlyBuyerCanFund() public {
        vm.expectRevert(TaskEscrow.Unauthorized.selector);
        vm.prank(worker);
        escrow.fund{value: AMOUNT}();
    }
}
