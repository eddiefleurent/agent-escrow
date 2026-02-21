// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Test} from "forge-std/Test.sol";
import {StdInvariant} from "forge-std/StdInvariant.sol";
import {TaskEscrowFactory} from "../src/TaskEscrowFactory.sol";
import {TaskEscrow} from "../src/TaskEscrow.sol";

contract TaskEscrowHandler is Test {
    TaskEscrow public escrow;

    address public buyer;
    address public worker;
    address public verifier;
    address public arbitrator;

    constructor(TaskEscrow _escrow, address _buyer, address _worker, address _verifier, address _arbitrator) {
        escrow = _escrow;
        buyer = _buyer;
        worker = _worker;
        verifier = _verifier;
        arbitrator = _arbitrator;
    }

    function fund() external {
        vm.prank(buyer);
        try escrow.fund{value: 1 ether}() {} catch {}
    }

    function cancelBeforeFunding() external {
        vm.prank(buyer);
        try escrow.cancelBeforeFunding() {} catch {}
    }

    function submit(bytes32 hash) external {
        vm.prank(worker);
        try escrow.submit(hash, "ipfs://submission") {} catch {}
    }

    function approveByBuyer() external {
        vm.prank(buyer);
        try escrow.approveByBuyer() {} catch {}
    }

    function approveByVerifier() external {
        vm.prank(verifier);
        try escrow.approveByVerifier() {} catch {}
    }

    function rejectByVerifier() external {
        vm.prank(verifier);
        try escrow.rejectByVerifier("ipfs://reject") {} catch {}
    }

    function dispute() external {
        vm.prank(buyer);
        try escrow.dispute("ipfs://buyer-dispute") {} catch {}
    }

    function escalateSilence() external {
        vm.prank(worker);
        try escrow.escalateSilence("ipfs://silence") {} catch {}
    }

    function resolveDispute(uint16 workerAwardBps) external {
        if (workerAwardBps > 10_000) {
            workerAwardBps = uint16(workerAwardBps % 10_001);
        }
        vm.prank(arbitrator);
        try escrow.resolveDispute(workerAwardBps, "ipfs://resolution") {} catch {}
    }

    function claimTimeoutRefund() external {
        vm.prank(buyer);
        try escrow.claimTimeoutRefund() {} catch {}
    }

    function claimArbitratorTimeout() external {
        vm.prank(buyer);
        try escrow.claimArbitratorTimeout() {} catch {}
    }

    function warpTime(uint32 jump) external {
        uint256 delta = uint256(jump % 10 days);
        vm.warp(block.timestamp + delta);
    }
}

contract TaskEscrowInvariantsTest is StdInvariant, Test {
    TaskEscrowFactory internal factory;
    TaskEscrow internal escrow;
    TaskEscrowHandler internal handler;

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

    uint256 internal baselineSystemSum;

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
                submissionDeadline: uint64(block.timestamp + 30 days),
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

        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();
        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result");

        baselineSystemSum = buyer.balance + worker.balance + treasury.balance + address(escrow).balance;

        handler = new TaskEscrowHandler(escrow, buyer, worker, verifier, arbitrator);
        targetContract(address(handler));
    }

    function invariant_terminalStateIsSticky() public {
        TaskEscrow.Status st = escrow.status();
        bool terminal =
            (st == TaskEscrow.Status.Settled || st == TaskEscrow.Status.Refunded || st == TaskEscrow.Status.Cancelled);
        if (!terminal) return;

        uint256 statusBefore = uint256(st);

        vm.expectRevert(TaskEscrow.InvalidState.selector);
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.expectRevert(TaskEscrow.InvalidState.selector);
        vm.prank(buyer);
        escrow.cancelBeforeFunding();

        vm.expectRevert(TaskEscrow.InvalidState.selector);
        vm.prank(worker);
        escrow.submit(keccak256("late-submission"), "ipfs://late");

        vm.expectRevert(TaskEscrow.InvalidState.selector);
        vm.prank(buyer);
        escrow.approveByBuyer();

        vm.expectRevert(TaskEscrow.InvalidState.selector);
        vm.prank(verifier);
        escrow.approveByVerifier();

        vm.expectRevert(TaskEscrow.InvalidState.selector);
        vm.prank(verifier);
        escrow.rejectByVerifier("ipfs://reject");

        vm.expectRevert(TaskEscrow.InvalidState.selector);
        vm.prank(buyer);
        escrow.dispute("ipfs://reason");

        vm.expectRevert(TaskEscrow.InvalidState.selector);
        vm.prank(worker);
        escrow.escalateSilence("ipfs://silence");

        vm.expectRevert(TaskEscrow.InvalidState.selector);
        vm.prank(arbitrator);
        escrow.resolveDispute(5000, "ipfs://resolution");

        vm.expectRevert(TaskEscrow.InvalidState.selector);
        vm.prank(buyer);
        escrow.claimTimeoutRefund();

        vm.expectRevert(TaskEscrow.InvalidState.selector);
        vm.prank(buyer);
        escrow.claimArbitratorTimeout();

        assertEq(uint256(escrow.status()), statusBefore);
    }

    function invariant_balanceConservation() public {
        uint256 currentSystemSum = buyer.balance + worker.balance + treasury.balance + address(escrow).balance;
        assertEq(currentSystemSum, baselineSystemSum);
    }
}

