// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Test} from "forge-std/Test.sol";
import {TaskEscrowFactory} from "../src/TaskEscrowFactory.sol";
import {TaskEscrow} from "../src/TaskEscrow.sol";

/// @notice Tests for market-stability controls (roadmap item 20).
contract TaskEscrowMarketStabilityTest is Test {
    TaskEscrowFactory internal factory;

    address internal owner = makeAddr("owner");
    address internal buyer = makeAddr("buyer");
    address internal worker = makeAddr("worker");
    address internal workerAlt = makeAddr("worker-alt");
    address internal verifier = makeAddr("verifier");
    address internal arbitrator = makeAddr("arbitrator");
    address internal treasury = makeAddr("treasury");

    uint256 internal constant AMOUNT = 1 ether;
    uint16 internal constant LOW_FEE_BPS = 100;
    uint16 internal constant HIGH_FEE_BPS = 250;
    uint64 internal constant REVIEW = 1 days;
    uint64 internal constant DISPUTE = 2 days;
    uint64 internal constant ARB_TIMEOUT = 7 days;

    function setUp() public {
        factory = new TaskEscrowFactory(LOW_FEE_BPS, HIGH_FEE_BPS, treasury, owner);
    }

    function _createEscrow(address buyerAddr, address workerAddr, address parentEscrow) internal returns (TaskEscrow) {
        (, address addr) = factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyerAddr,
                worker: workerAddr,
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
                taskSpecHash: keccak256("market-stability"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                serviceTier: 0,
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                zkVerifier: address(0),
                circuitId: bytes32(0),
                parentEscrow: parentEscrow,
                milestones: new TaskEscrowFactory.CreateMilestoneParams[](0)
            })
        );
        return TaskEscrow(addr);
    }

    function testParentEscrowMustBeRegistered() public {
        vm.expectRevert(TaskEscrowFactory.NotRegisteredEscrow.selector);
        _createEscrow(buyer, worker, makeAddr("not-registered-parent"));
    }

    function testRedelegationSurchargeProgressionAndCap() public {
        vm.prank(owner);
        factory.setRedelegationSurchargePolicy(25, 60, 1 hours);

        TaskEscrow parent = _createEscrow(buyer, worker, address(0));
        assertEq(parent.protocolFeeBpsSnapshot(), LOW_FEE_BPS);

        TaskEscrow child1 = _createEscrow(worker, workerAlt, address(parent));
        TaskEscrow child2 = _createEscrow(worker, workerAlt, address(parent));
        TaskEscrow child3 = _createEscrow(worker, workerAlt, address(parent));
        TaskEscrow child4 = _createEscrow(worker, workerAlt, address(parent));

        // First child linked to parent starts a fresh streak (no surcharge yet).
        assertEq(child1.protocolFeeBpsSnapshot(), LOW_FEE_BPS);
        // Subsequent frequent re-delegations accrue surcharge up to the cap.
        assertEq(child2.protocolFeeBpsSnapshot(), LOW_FEE_BPS + 25);
        assertEq(child3.protocolFeeBpsSnapshot(), LOW_FEE_BPS + 50);
        assertEq(child4.protocolFeeBpsSnapshot(), LOW_FEE_BPS + 60);

        (uint64 lastRedelegationAt, uint16 streak) = factory.redelegationFeeState(address(parent));
        assertEq(lastRedelegationAt, uint64(block.timestamp));
        assertEq(streak, 3);
    }

    function testRedelegationSurchargeResetsOutsideWindow() public {
        vm.prank(owner);
        factory.setRedelegationSurchargePolicy(30, 90, 1 hours);

        TaskEscrow parent = _createEscrow(buyer, worker, address(0));
        TaskEscrow child1 = _createEscrow(worker, workerAlt, address(parent));
        TaskEscrow child2 = _createEscrow(worker, workerAlt, address(parent));
        assertEq(child1.protocolFeeBpsSnapshot(), LOW_FEE_BPS);
        assertEq(child2.protocolFeeBpsSnapshot(), LOW_FEE_BPS + 30);

        vm.warp(block.timestamp + 2 hours);
        TaskEscrow child3 = _createEscrow(worker, workerAlt, address(parent));
        assertEq(child3.protocolFeeBpsSnapshot(), LOW_FEE_BPS);
    }

    function testSetRedelegationSurchargePolicyOnlyOwner() public {
        vm.expectRevert(TaskEscrowFactory.Unauthorized.selector);
        factory.setRedelegationSurchargePolicy(25, 100, 3600);
    }

    function testSetRedelegationSurchargePolicyRejectsFeeOverflow() public {
        vm.startPrank(owner);
        vm.expectRevert(TaskEscrowFactory.InvalidFeeBps.selector);
        factory.setRedelegationSurchargePolicy(25, 9_751, 3600);
        vm.stopPrank();
    }

    function testSetRedelegationSurchargePolicyRejectsZeroWindowWithNonZeroSurcharge() public {
        vm.startPrank(owner);
        vm.expectRevert(TaskEscrowFactory.InvalidConfig.selector);
        factory.setRedelegationSurchargePolicy(25, 60, 0);

        vm.expectRevert(TaskEscrowFactory.InvalidConfig.selector);
        factory.setRedelegationSurchargePolicy(0, 60, 0);

        // Zero surcharges with zero window is allowed (effectively disables surcharge)
        factory.setRedelegationSurchargePolicy(0, 0, 0);
        vm.stopPrank();
    }
}
