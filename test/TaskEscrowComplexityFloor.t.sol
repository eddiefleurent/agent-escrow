// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {BaseEscrowTest} from "./BaseEscrowTest.sol";
import {TaskEscrowFactory} from "../src/TaskEscrowFactory.sol";
import {MockERC20} from "./mocks/MockERC20.sol";

contract TaskEscrowComplexityFloorTest is BaseEscrowTest {
    MockERC20 internal token;

    address internal nonOwner = makeAddr("nonOwner");

    uint256 internal constant FLOOR = 0.01 ether;

    function setUp() public override {
        super.setUp();
        token = new MockERC20("TestToken", "TT", 18);
    }

    function _defaultParams(uint256 amount) internal view returns (TaskEscrowFactory.CreateParams memory) {
        TaskEscrowFactory.CreateParams memory params = _defaultParams();
        params.amount = amount;
        return params;
    }

    // ── Default state ──

    function testComplexityFloorDefaultZero() public view {
        assertEq(factory.complexityFloor(), 0);
    }

    // ── Setter ──

    function testSetComplexityFloor() public {
        vm.prank(owner);
        vm.expectEmit(false, false, false, true);
        emit TaskEscrowFactory.ComplexityFloorUpdated(0, FLOOR);
        factory.setComplexityFloor(FLOOR);

        assertEq(factory.complexityFloor(), FLOOR);
    }

    function testSetComplexityFloorNonOwnerReverts() public {
        vm.prank(nonOwner);
        vm.expectRevert(TaskEscrowFactory.Unauthorized.selector);
        factory.setComplexityFloor(FLOOR);
    }

    // ── Enforcement in createEscrow ──

    function testCreateEscrowBelowFloorReverts() public {
        vm.prank(owner);
        factory.setComplexityFloor(FLOOR);

        vm.expectRevert(TaskEscrowFactory.BelowComplexityFloor.selector);
        factory.createEscrow(_defaultParams(FLOOR - 1));
    }

    function testCreateEscrowAtFloorSucceeds() public {
        vm.prank(owner);
        factory.setComplexityFloor(FLOOR);

        (uint256 id, address addr) = factory.createEscrow(_defaultParams(FLOOR));
        assertTrue(addr != address(0));
        assertEq(id, 0);
    }

    function testCreateEscrowAboveFloorSucceeds() public {
        vm.prank(owner);
        factory.setComplexityFloor(FLOOR);

        (uint256 id, address addr) = factory.createEscrow(_defaultParams(FLOOR + 1 ether));
        assertTrue(addr != address(0));
        assertEq(id, 0);
    }

    function testCreateEscrowFloorZeroAllowsAnyAmount() public {
        // Floor defaults to 0 -- any positive amount should work
        (uint256 id, address addr) = factory.createEscrow(_defaultParams(1));
        assertTrue(addr != address(0));
        assertEq(id, 0);
    }

    // ── Milestone escrows ──

    function testCreateMilestoneEscrowBelowFloorReverts() public {
        vm.prank(owner);
        factory.setComplexityFloor(FLOOR);

        // Total amount below floor, split across 2 milestones
        uint256 totalAmount = FLOOR - 1;
        TaskEscrowFactory.CreateMilestoneParams[] memory ms = new TaskEscrowFactory.CreateMilestoneParams[](2);
        ms[0] = TaskEscrowFactory.CreateMilestoneParams({
            amount: totalAmount / 2, submissionDeadline: uint64(block.timestamp + 3 days)
        });
        ms[1] = TaskEscrowFactory.CreateMilestoneParams({
            amount: totalAmount - totalAmount / 2, submissionDeadline: uint64(block.timestamp + 6 days)
        });

        TaskEscrowFactory.CreateParams memory p = _defaultParams(totalAmount);
        p.milestones = ms;

        vm.expectRevert(TaskEscrowFactory.BelowComplexityFloor.selector);
        factory.createEscrow(p);
    }

    function testCreateMilestoneEscrowAtFloorSucceeds() public {
        vm.prank(owner);
        factory.setComplexityFloor(FLOOR);

        TaskEscrowFactory.CreateMilestoneParams[] memory ms = new TaskEscrowFactory.CreateMilestoneParams[](2);
        ms[0] = TaskEscrowFactory.CreateMilestoneParams({
            amount: FLOOR / 2, submissionDeadline: uint64(block.timestamp + 3 days)
        });
        ms[1] = TaskEscrowFactory.CreateMilestoneParams({
            amount: FLOOR - FLOOR / 2, submissionDeadline: uint64(block.timestamp + 6 days)
        });

        TaskEscrowFactory.CreateParams memory p = _defaultParams(FLOOR);
        p.milestones = ms;

        (, address addr) = factory.createEscrow(p);
        assertTrue(addr != address(0));
    }

    // ── Re-disabling ──

    function testSetComplexityFloorToZeroDisables() public {
        vm.startPrank(owner);
        factory.setComplexityFloor(FLOOR);
        assertEq(factory.complexityFloor(), FLOOR);

        factory.setComplexityFloor(0);
        assertEq(factory.complexityFloor(), 0);
        vm.stopPrank();

        // Now any amount should work again
        (, address addr) = factory.createEscrow(_defaultParams(1));
        assertTrue(addr != address(0));
    }

    // ── ERC20 ──

    function testComplexityFloorWithERC20() public {
        vm.prank(owner);
        factory.setComplexityFloor(FLOOR);

        TaskEscrowFactory.CreateParams memory p = _defaultParams(FLOOR - 1);
        p.token = address(token);

        vm.expectRevert(TaskEscrowFactory.BelowComplexityFloor.selector);
        factory.createEscrow(p);

        // At floor should succeed
        TaskEscrowFactory.CreateParams memory p2 = _defaultParams(FLOOR);
        p2.token = address(token);

        (, address addr) = factory.createEscrow(p2);
        assertTrue(addr != address(0));
    }
}
