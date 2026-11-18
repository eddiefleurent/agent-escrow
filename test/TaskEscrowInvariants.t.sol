// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Test} from "forge-std/Test.sol";
import {StdInvariant} from "forge-std/StdInvariant.sol";
import {TaskEscrowFactory} from "../src/TaskEscrowFactory.sol";
import {TaskEscrow} from "../src/TaskEscrow.sol";
import {BaseEscrowTest} from "./BaseEscrowTest.sol";

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
        try escrow.submit(hash, "ipfs://submission", bytes32(0)) {} catch {}
    }

    function approveByBuyer() external {
        vm.prank(buyer);
        try escrow.approveByBuyer() {} catch {}
    }

    function approveByVerifier() external {
        vm.prank(verifier);
        try escrow.castVerifierVote(true, "") {} catch {}
    }

    function rejectByVerifier() external {
        vm.prank(verifier);
        try escrow.castVerifierVote(false, "ipfs://reject") {} catch {}
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

/// forge-config: default.invariant.runs = 256
/// forge-config: default.invariant.depth = 500
contract TaskEscrowInvariantsTest is StdInvariant, BaseEscrowTest {
    TaskEscrow internal escrow;
    TaskEscrowHandler internal handler;

    uint256 internal baselineSystemSum;

    function setUp() public override {
        super.setUp();

        TaskEscrowFactory.CreateParams memory p = _defaultParams();
        p.submissionDeadline = uint64(block.timestamp + 30 days);
        vm.prank(address(0xBEEF));
        escrow = _create(p);

        vm.deal(buyer, 10 ether);
        vm.deal(worker, 1 ether);

        _fundAndSubmit(escrow);

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
        escrow.submit(keccak256("late-submission"), "ipfs://late", bytes32(0));

        vm.expectRevert(TaskEscrow.InvalidState.selector);
        vm.prank(buyer);
        escrow.approveByBuyer();

        vm.expectRevert(TaskEscrow.QuorumFinalized.selector);
        vm.prank(verifier);
        escrow.castVerifierVote(true, "");

        vm.expectRevert(TaskEscrow.QuorumFinalized.selector);
        vm.prank(verifier);
        escrow.castVerifierVote(false, "ipfs://reject");

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

