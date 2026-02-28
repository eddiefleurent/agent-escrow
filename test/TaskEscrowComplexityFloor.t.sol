// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Test} from "forge-std/Test.sol";
import {TaskEscrowFactory} from "../src/TaskEscrowFactory.sol";
import {IERC20} from "../src/interfaces/IERC20.sol";

contract MockERC20Floor is IERC20 {
    string public name = "TestToken";
    string public symbol = "TT";
    uint8 public decimals = 18;
    uint256 public totalSupply;
    mapping(address => uint256) public balanceOf;
    mapping(address => mapping(address => uint256)) public allowance;

    function mint(address to, uint256 amount) external {
        balanceOf[to] += amount;
        totalSupply += amount;
    }

    function transfer(address to, uint256 amount) external returns (bool) {
        balanceOf[msg.sender] -= amount;
        balanceOf[to] += amount;
        return true;
    }

    function approve(address spender, uint256 amount) external returns (bool) {
        allowance[msg.sender][spender] = amount;
        return true;
    }

    function transferFrom(address from, address to, uint256 amount) external returns (bool) {
        balanceOf[from] -= amount;
        allowance[from][msg.sender] -= amount;
        balanceOf[to] += amount;
        return true;
    }
}

contract TaskEscrowComplexityFloorTest is Test {
    TaskEscrowFactory internal factory;
    MockERC20Floor internal token;

    address internal factoryOwner = makeAddr("owner");
    address internal buyer = makeAddr("buyer");
    address internal worker = makeAddr("worker");
    address internal verifier = makeAddr("verifier");
    address internal arbitrator = makeAddr("arbitrator");
    address internal treasury = makeAddr("treasury");
    address internal nonOwner = makeAddr("nonOwner");

    uint16 internal constant FEE_BPS = 100;
    uint64 internal constant REVIEW = 86_400;
    uint64 internal constant DISPUTE = 172_800;
    uint64 internal constant ARB_TIMEOUT = 7 days;
    uint256 internal constant FLOOR = 0.01 ether;

    function setUp() public {
        factory = new TaskEscrowFactory(FEE_BPS, FEE_BPS, treasury, factoryOwner);
        token = new MockERC20Floor();
    }

    function _defaultParams(uint256 amount) internal view returns (TaskEscrowFactory.CreateParams memory) {
        return TaskEscrowFactory.CreateParams({
            buyer: buyer,
            worker: worker,
            verifierPanel: [verifier, address(0), address(0), address(0), address(0), address(0), address(0)],
            quorumThreshold: 1,
            quorumVerifierCount: 1,
            verifierStakePerVerifier: 0,
            arbitrator: arbitrator,
            amount: amount,
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
            milestones: new TaskEscrowFactory.CreateMilestoneParams[](0)
        });
    }

    // ── Default state ──

    function testComplexityFloorDefaultZero() public view {
        assertEq(factory.complexityFloor(), 0);
    }

    // ── Setter ──

    function testSetComplexityFloor() public {
        vm.prank(factoryOwner);
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
        vm.prank(factoryOwner);
        factory.setComplexityFloor(FLOOR);

        vm.expectRevert(TaskEscrowFactory.BelowComplexityFloor.selector);
        factory.createEscrow(_defaultParams(FLOOR - 1));
    }

    function testCreateEscrowAtFloorSucceeds() public {
        vm.prank(factoryOwner);
        factory.setComplexityFloor(FLOOR);

        (uint256 id, address addr) = factory.createEscrow(_defaultParams(FLOOR));
        assertTrue(addr != address(0));
        assertEq(id, 0);
    }

    function testCreateEscrowAboveFloorSucceeds() public {
        vm.prank(factoryOwner);
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
        vm.prank(factoryOwner);
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
        vm.prank(factoryOwner);
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
        vm.startPrank(factoryOwner);
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
        vm.prank(factoryOwner);
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
