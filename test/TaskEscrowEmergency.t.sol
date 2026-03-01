// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Test} from "forge-std/Test.sol";
import {TaskEscrowFactory} from "../src/TaskEscrowFactory.sol";
import {TaskEscrow} from "../src/TaskEscrow.sol";

contract TaskEscrowEmergencyTest is Test {
    TaskEscrowFactory internal factory;
    TaskEscrow internal escrow;

    address internal owner = makeAddr("owner");
    address internal buyer = makeAddr("buyer");
    address internal worker = makeAddr("worker");
    address internal verifier = makeAddr("verifier");
    address internal arbitrator = makeAddr("arbitrator");
    address internal treasury = makeAddr("treasury");
    address internal nonOwner = makeAddr("nonOwner");

    uint256 internal constant AMOUNT = 1 ether;
    uint256 internal constant STAKE = 0.1 ether;
    uint16 internal constant FEE_BPS = 100; // 1%
    uint64 internal constant REVIEW = 86_400;
    uint64 internal constant DISPUTE = 172_800;
    uint64 internal constant ARB_TIMEOUT = 7 days;

    function setUp() public {
        factory = new TaskEscrowFactory(FEE_BPS, FEE_BPS, treasury, owner);

        vm.prank(nonOwner);
        (, address escrowAddr) = factory.createEscrow(
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
        escrow = TaskEscrow(escrowAddr);

        vm.deal(buyer, 10 ether);
        vm.deal(worker, 1 ether);
    }

    function _defaultParams() internal view returns (TaskEscrowFactory.CreateParams memory) {
        return TaskEscrowFactory.CreateParams({
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
        });
    }

    function _milestoneParams3() internal view returns (TaskEscrowFactory.CreateMilestoneParams[] memory ms) {
        ms = new TaskEscrowFactory.CreateMilestoneParams[](3);
        ms[0] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 0.3 ether, submissionDeadline: uint64(block.timestamp + 7 days)
        });
        ms[1] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 0.3 ether, submissionDeadline: uint64(block.timestamp + 14 days)
        });
        ms[2] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 0.4 ether, submissionDeadline: uint64(block.timestamp + 21 days)
        });
    }

    function _create3MilestoneEscrow() internal returns (TaskEscrow) {
        TaskEscrowFactory.CreateParams memory p = _defaultParams();
        p.amount = 1 ether;
        p.submissionDeadline = uint64(block.timestamp + 21 days);
        p.taskSpecHash = keccak256("spec-ms");
        p.milestones = _milestoneParams3();

        vm.prank(nonOwner);
        (, address addr) = factory.createEscrow(p);
        return TaskEscrow(addr);
    }

    // ── Factory: Address Freezing ──

    function testFreezeAddress() public {
        address target = makeAddr("target");
        assertFalse(factory.frozenAddresses(target));

        vm.prank(owner);
        factory.freezeAddress(target);

        assertTrue(factory.frozenAddresses(target));
    }

    function testUnfreezeAddress() public {
        address target = makeAddr("target");
        vm.prank(owner);
        factory.freezeAddress(target);
        assertTrue(factory.frozenAddresses(target));

        vm.prank(owner);
        factory.unfreezeAddress(target);

        assertFalse(factory.frozenAddresses(target));
    }

    function testFreezeAddressOnlyOwner() public {
        address target = makeAddr("target");

        vm.expectRevert(TaskEscrowFactory.Unauthorized.selector);
        vm.prank(nonOwner);
        factory.freezeAddress(target);
    }

    function testFrozenAddressCannotParticipateInNewEscrow() public {
        vm.prank(owner);
        factory.freezeAddress(buyer);

        vm.expectRevert(TaskEscrowFactory.FrozenAddress.selector);
        vm.prank(nonOwner);
        factory.createEscrow(_defaultParams());
    }

    function testFreezeAddressEmitsEvent() public {
        address target = makeAddr("target");

        vm.prank(owner);
        vm.expectEmit(true, true, false, true, address(factory));
        emit TaskEscrowFactory.AddressFrozen(target);
        factory.freezeAddress(target);

        vm.prank(owner);
        vm.expectEmit(true, true, false, true, address(factory));
        emit TaskEscrowFactory.AddressUnfrozen(target);
        factory.unfreezeAddress(target);
    }

    // ── Factory: Escrow Freezing ──

    function testFreezeEscrow() public {
        assertFalse(escrow.frozen());

        vm.prank(owner);
        factory.freezeEscrow(0);

        assertTrue(escrow.frozen());
    }

    function testUnfreezeEscrow() public {
        vm.prank(owner);
        factory.freezeEscrow(0);
        assertTrue(escrow.frozen());

        vm.prank(owner);
        factory.unfreezeEscrow(0);

        assertFalse(escrow.frozen());
    }

    function testFreezeEscrowOnlyOwner() public {
        vm.expectRevert(TaskEscrowFactory.Unauthorized.selector);
        vm.prank(nonOwner);
        factory.freezeEscrow(0);
    }

    function testFreezeEscrowEmitsEvents() public {
        vm.prank(owner);
        vm.expectEmit(true, false, false, true, address(escrow));
        emit TaskEscrow.EmergencyFrozen();
        vm.expectEmit(true, false, false, true, address(factory));
        emit TaskEscrowFactory.EscrowFrozen(0);
        factory.freezeEscrow(0);

        vm.prank(owner);
        vm.expectEmit(true, false, false, true, address(escrow));
        emit TaskEscrow.EmergencyUnfrozen();
        vm.expectEmit(true, false, false, true, address(factory));
        emit TaskEscrowFactory.EscrowUnfrozen(0);
        factory.unfreezeEscrow(0);
    }

    // ── Frozen Escrow Behavior ──

    function testFrozenEscrowBlocksFund() public {
        vm.prank(owner);
        factory.freezeEscrow(0);

        vm.expectRevert(TaskEscrow.Frozen.selector);
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();
    }

    function testFrozenEscrowBlocksSubmit() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(owner);
        factory.freezeEscrow(0);

        vm.expectRevert(TaskEscrow.Frozen.selector);
        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result", bytes32(0));
    }

    function testFrozenEscrowBlocksApprove() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result", bytes32(0));

        vm.prank(owner);
        factory.freezeEscrow(0);

        vm.expectRevert(TaskEscrow.Frozen.selector);
        vm.prank(buyer);
        escrow.approveByBuyer();
    }

    function testFrozenEscrowBlocksDispute() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result", bytes32(0));

        vm.prank(owner);
        factory.freezeEscrow(0);

        vm.expectRevert(TaskEscrow.Frozen.selector);
        vm.prank(buyer);
        escrow.dispute("ipfs://reason");
    }

    function testFrozenEscrowBlocksResolveDispute() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result", bytes32(0));

        vm.prank(buyer);
        escrow.dispute("ipfs://reason");

        vm.prank(owner);
        factory.freezeEscrow(0);

        vm.expectRevert(TaskEscrow.Frozen.selector);
        vm.prank(arbitrator);
        escrow.resolveDispute(5000, "ipfs://resolution");
    }

    function testFrozenEscrowAllowsTimeoutRefund() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(owner);
        factory.freezeEscrow(0);

        vm.warp(block.timestamp + 8 days);
        uint256 buyerBefore = buyer.balance;

        vm.prank(buyer);
        escrow.claimTimeoutRefund();

        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Refunded));
        assertEq(buyer.balance, buyerBefore + AMOUNT);
        assertEq(address(escrow).balance, 0);
    }

    function testFrozenEscrowAllowsArbitratorTimeout() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result", bytes32(0));

        vm.prank(buyer);
        escrow.dispute("ipfs://reason");

        vm.prank(owner);
        factory.freezeEscrow(0);

        vm.warp(block.timestamp + ARB_TIMEOUT + 1);
        uint256 buyerBefore = buyer.balance;

        vm.prank(buyer);
        escrow.claimArbitratorTimeout();

        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Refunded));
        assertEq(buyer.balance, buyerBefore + AMOUNT);
        assertEq(address(escrow).balance, 0);
    }

    // ── Emergency Resolve ──

    function testEmergencyResolveFullRefund() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(owner);
        factory.freezeEscrow(0);

        uint256 buyerBefore = buyer.balance;
        uint256 workerBefore = worker.balance;

        vm.prank(owner);
        factory.emergencyResolve(0, 0);

        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Settled));
        assertEq(buyer.balance, buyerBefore + AMOUNT);
        assertEq(worker.balance, workerBefore);
        assertEq(address(escrow).balance, 0);
    }

    function testEmergencyResolveFullPayout() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(owner);
        factory.freezeEscrow(0);

        uint256 workerBefore = worker.balance;
        uint256 treasuryBefore = treasury.balance;
        uint256 fee = (AMOUNT * FEE_BPS) / 10_000;

        vm.prank(owner);
        factory.emergencyResolve(0, 10_000);

        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Settled));
        assertEq(worker.balance, workerBefore + AMOUNT - fee);
        assertEq(treasury.balance, treasuryBefore + fee);
        assertEq(address(escrow).balance, 0);
    }

    function testEmergencyResolveSplit() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(owner);
        factory.freezeEscrow(0);

        uint256 buyerBefore = buyer.balance;
        uint256 workerBefore = worker.balance;
        uint256 treasuryBefore = treasury.balance;

        vm.prank(owner);
        factory.emergencyResolve(0, 5000);

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

    function testEmergencyResolveRequiresFrozen() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.expectRevert(TaskEscrowFactory.EscrowNotFrozen.selector);
        vm.prank(owner);
        factory.emergencyResolve(0, 5000);
    }

    function testEmergencyResolveOnlyOwner() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(owner);
        factory.freezeEscrow(0);

        vm.expectRevert(TaskEscrowFactory.Unauthorized.selector);
        vm.prank(nonOwner);
        factory.emergencyResolve(0, 5000);
    }

    function testEmergencyResolveOnTerminalStateReverts() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result", bytes32(0));

        vm.prank(buyer);
        escrow.approveByBuyer();

        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Settled));

        vm.prank(owner);
        factory.freezeEscrow(0);

        vm.expectRevert(TaskEscrow.InvalidState.selector);
        vm.prank(owner);
        factory.emergencyResolve(0, 5000);
    }

    function testEmergencyResolveOnCreatedStateCancels() public {
        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Created));

        vm.prank(owner);
        factory.freezeEscrow(0);

        vm.prank(owner);
        factory.emergencyResolve(0, 5000);

        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Cancelled));
        assertEq(address(escrow).balance, 0);
    }

    function testEmergencyResolveEmitsEvent() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(owner);
        factory.freezeEscrow(0);

        vm.prank(owner);
        vm.expectEmit(true, false, false, true, address(escrow));
        emit TaskEscrow.EmergencyResolved(5000);
        vm.expectEmit(true, false, false, true, address(factory));
        emit TaskEscrowFactory.EmergencyResolved(0, 5000);
        factory.emergencyResolve(0, 5000);
    }

    // ── Emergency Resolve for Milestones ──

    function testEmergencyResolveMilestoneEscrow() public {
        TaskEscrow e = _create3MilestoneEscrow();
        vm.prank(buyer);
        e.fund{value: 1 ether}();

        vm.prank(owner);
        factory.freezeEscrow(1);

        uint256 workerBefore = worker.balance;
        uint256 buyerBefore = buyer.balance;
        uint256 treasuryBefore = treasury.balance;

        vm.prank(owner);
        factory.emergencyResolve(1, 5000);

        uint256 total = 1 ether;
        uint256 workerGross = (total * 5000) / 10_000;
        uint256 fee = (workerGross * FEE_BPS) / 10_000;
        uint256 workerNet = workerGross - fee;
        uint256 buyerRefund = total - workerGross;

        assertEq(uint256(e.status()), uint256(TaskEscrow.Status.Settled));
        assertEq(worker.balance, workerBefore + workerNet);
        assertEq(buyer.balance, buyerBefore + buyerRefund);
        assertEq(treasury.balance, treasuryBefore + fee);
        assertEq(address(e).balance, 0);
    }

    function testEmergencyResolveMilestonePartiallyCompleted() public {
        TaskEscrow e = _create3MilestoneEscrow();
        vm.prank(buyer);
        e.fund{value: 1 ether}();

        vm.prank(worker);
        e.submitMilestone(0, keccak256("ms0"), "ipfs://ms0", bytes32(0));
        vm.prank(buyer);
        e.approveMilestoneByBuyer(0);

        vm.prank(worker);
        e.submitMilestone(1, keccak256("ms1"), "ipfs://ms1", bytes32(0));
        vm.prank(buyer);
        e.approveMilestoneByBuyer(1);

        uint256 workerBefore = worker.balance;
        uint256 buyerBefore = buyer.balance;
        uint256 treasuryBefore = treasury.balance;

        vm.prank(owner);
        factory.freezeEscrow(1);

        vm.prank(owner);
        factory.emergencyResolve(1, 5000);

        uint256 ms2Amount = 0.4 ether;
        uint256 workerGross = (ms2Amount * 5000) / 10_000;
        uint256 fee = (workerGross * FEE_BPS) / 10_000;
        uint256 workerNet = workerGross - fee;
        uint256 buyerRefund = ms2Amount - workerGross;

        assertEq(uint256(e.status()), uint256(TaskEscrow.Status.Settled));
        assertEq(worker.balance, workerBefore + workerNet);
        assertEq(buyer.balance, buyerBefore + buyerRefund);
        assertEq(treasury.balance, treasuryBefore + fee);
        assertEq(address(e).balance, 0);
    }

    // ── Edge Cases ──

    function testFreezeAlreadyFrozenAddress() public {
        address target = makeAddr("target");
        vm.prank(owner);
        factory.freezeAddress(target);

        vm.prank(owner);
        factory.freezeAddress(target);

        assertTrue(factory.frozenAddresses(target));
    }

    function testUnfreezeNonFrozenAddress() public {
        address target = makeAddr("target");

        vm.prank(owner);
        factory.unfreezeAddress(target);

        assertFalse(factory.frozenAddresses(target));
    }

    function testSetFrozenOnlyFactory() public {
        vm.expectRevert(TaskEscrow.Unauthorized.selector);
        vm.prank(nonOwner);
        escrow.setFrozen(true);
    }

    function testEmergencyResolveOnlyFactory() public {
        vm.prank(buyer);
        escrow.fund{value: AMOUNT}();

        vm.prank(owner);
        factory.freezeEscrow(0);

        vm.expectRevert(TaskEscrow.Unauthorized.selector);
        vm.prank(buyer);
        escrow.emergencyResolve(0);
    }
}
