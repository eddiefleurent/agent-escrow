// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Test} from "forge-std/Test.sol";
import {TaskEscrowFactory} from "../src/TaskEscrowFactory.sol";
import {TaskEscrow} from "../src/TaskEscrow.sol";
import {IERC20} from "../src/interfaces/IERC20.sol";

/// @dev Minimal ERC20 for testing. Standard-compliant (returns bool).
contract MockERC20 is IERC20 {
    string public name;
    string public symbol;
    uint8 public decimals;
    uint256 public totalSupply;
    mapping(address => uint256) public balanceOf;
    mapping(address => mapping(address => uint256)) public allowance;

    constructor(string memory _name, string memory _symbol, uint8 _decimals) {
        name = _name;
        symbol = _symbol;
        decimals = _decimals;
    }

    function mint(address to, uint256 amount) external {
        balanceOf[to] += amount;
        totalSupply += amount;
    }

    function transfer(address to, uint256 amount) external returns (bool) {
        require(balanceOf[msg.sender] >= amount, "insufficient balance");
        balanceOf[msg.sender] -= amount;
        balanceOf[to] += amount;
        return true;
    }

    function approve(address spender, uint256 amount) external returns (bool) {
        allowance[msg.sender][spender] = amount;
        return true;
    }

    function transferFrom(address from, address to, uint256 amount) external returns (bool) {
        require(balanceOf[from] >= amount, "insufficient balance");
        require(allowance[from][msg.sender] >= amount, "insufficient allowance");
        balanceOf[from] -= amount;
        allowance[from][msg.sender] -= amount;
        balanceOf[to] += amount;
        return true;
    }
}

/// @dev ERC20 that does not return a bool on transfer/transferFrom (like USDT).
contract MockERC20NoReturn {
    string public name;
    string public symbol;
    uint8 public decimals;
    uint256 public totalSupply;
    mapping(address => uint256) public balanceOf;
    mapping(address => mapping(address => uint256)) public allowance;

    constructor(string memory _name, string memory _symbol, uint8 _decimals) {
        name = _name;
        symbol = _symbol;
        decimals = _decimals;
    }

    function mint(address to, uint256 amount) external {
        balanceOf[to] += amount;
        totalSupply += amount;
    }

    function transfer(address to, uint256 amount) external {
        require(balanceOf[msg.sender] >= amount, "insufficient balance");
        balanceOf[msg.sender] -= amount;
        balanceOf[to] += amount;
    }

    function approve(address spender, uint256 amount) external {
        allowance[msg.sender][spender] = amount;
    }

    function transferFrom(address from, address to, uint256 amount) external {
        require(balanceOf[from] >= amount, "insufficient balance");
        require(allowance[from][msg.sender] >= amount, "insufficient allowance");
        balanceOf[from] -= amount;
        allowance[from][msg.sender] -= amount;
        balanceOf[to] += amount;
    }
}

contract TaskEscrowERC20Test is Test {
    TaskEscrowFactory internal factory;
    MockERC20 internal usdc;

    address internal owner = makeAddr("owner");
    address internal buyer = makeAddr("buyer");
    address internal worker = makeAddr("worker");
    address internal verifier = makeAddr("verifier");
    address internal arbitrator = makeAddr("arbitrator");
    address internal treasury = makeAddr("treasury");

    uint256 internal constant AMOUNT = 1000e6; // 1000 USDC (6 decimals)
    uint16 internal constant FEE_BPS = 100; // 1%
    uint64 internal constant REVIEW = 86_400;
    uint64 internal constant DISPUTE = 172_800;
    uint64 internal constant ARB_TIMEOUT = 7 days;

    function setUp() public {
        factory = new TaskEscrowFactory(FEE_BPS, treasury, owner);
        usdc = new MockERC20("USD Coin", "USDC", 6);
        usdc.mint(buyer, 10_000e6);
    }

    function _createERC20Escrow() internal returns (TaskEscrow) {
        (, address addr) = factory.createEscrow(
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
                token: address(usdc)
            })
        );
        return TaskEscrow(addr);
    }

    function _fundERC20Escrow(TaskEscrow escrow) internal {
        vm.startPrank(buyer);
        usdc.approve(address(escrow), AMOUNT);
        escrow.fund();
        vm.stopPrank();
    }

    function _fundAndSubmit(TaskEscrow escrow) internal {
        _fundERC20Escrow(escrow);
        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result");
    }

    // ── Token field ──

    function testTokenFieldSetCorrectly() public {
        TaskEscrow escrow = _createERC20Escrow();
        assertEq(escrow.token(), address(usdc));
    }

    function testETHEscrowTokenIsZero() public {
        (, address addr) = factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifier: verifier,
                arbitrator: arbitrator,
                amount: 1 ether,
                submissionDeadline: uint64(block.timestamp + 7 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0)
            })
        );
        assertEq(TaskEscrow(addr).token(), address(0));
    }

    // ── ERC20 funding ──

    function testERC20FundTransfersTokens() public {
        TaskEscrow escrow = _createERC20Escrow();
        uint256 buyerBefore = usdc.balanceOf(buyer);

        _fundERC20Escrow(escrow);

        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Funded));
        assertEq(usdc.balanceOf(address(escrow)), AMOUNT);
        assertEq(usdc.balanceOf(buyer), buyerBefore - AMOUNT);
    }

    function testERC20FundRejectsETH() public {
        TaskEscrow escrow = _createERC20Escrow();
        vm.deal(buyer, 1 ether);

        vm.startPrank(buyer);
        usdc.approve(address(escrow), AMOUNT);
        vm.expectRevert(TaskEscrow.ETHNotAccepted.selector);
        escrow.fund{value: 1 ether}();
        vm.stopPrank();
    }

    function testERC20FundWithoutApprovalReverts() public {
        TaskEscrow escrow = _createERC20Escrow();

        vm.prank(buyer);
        vm.expectRevert(TaskEscrow.TransferFailed.selector);
        escrow.fund();
    }

    // ── ERC20 happy path (approve by buyer) ──

    function testERC20HappyPathApproveByBuyer() public {
        TaskEscrow escrow = _createERC20Escrow();
        _fundAndSubmit(escrow);

        uint256 treasuryBefore = usdc.balanceOf(treasury);
        uint256 workerBefore = usdc.balanceOf(worker);

        vm.prank(buyer);
        escrow.approveByBuyer();

        uint256 fee = (AMOUNT * FEE_BPS) / 10_000;
        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Settled));
        assertEq(usdc.balanceOf(worker), workerBefore + (AMOUNT - fee));
        assertEq(usdc.balanceOf(treasury), treasuryBefore + fee);
        assertEq(usdc.balanceOf(address(escrow)), 0);
    }

    // ── ERC20 happy path (approve by verifier) ──

    function testERC20HappyPathApproveByVerifier() public {
        TaskEscrow escrow = _createERC20Escrow();
        _fundAndSubmit(escrow);

        uint256 treasuryBefore = usdc.balanceOf(treasury);
        uint256 workerBefore = usdc.balanceOf(worker);

        vm.prank(verifier);
        escrow.approveByVerifier();

        uint256 fee = (AMOUNT * FEE_BPS) / 10_000;
        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Settled));
        assertEq(usdc.balanceOf(worker), workerBefore + (AMOUNT - fee));
        assertEq(usdc.balanceOf(treasury), treasuryBefore + fee);
        assertEq(usdc.balanceOf(address(escrow)), 0);
    }

    // ── ERC20 dispute + resolution ──

    function testERC20DisputeResolvedSplit() public {
        TaskEscrow escrow = _createERC20Escrow();
        _fundAndSubmit(escrow);

        vm.prank(buyer);
        escrow.dispute("ipfs://reason");

        uint256 treasuryBefore = usdc.balanceOf(treasury);
        uint256 buyerBefore = usdc.balanceOf(buyer);
        uint256 workerBefore = usdc.balanceOf(worker);

        vm.prank(arbitrator);
        escrow.resolveDispute(5000, "ipfs://resolution");

        uint256 workerGross = (AMOUNT * 5000) / 10_000;
        uint256 fee = (workerGross * FEE_BPS) / 10_000;
        uint256 workerNet = workerGross - fee;
        uint256 buyerRefund = AMOUNT - workerGross;

        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Settled));
        assertEq(usdc.balanceOf(worker), workerBefore + workerNet);
        assertEq(usdc.balanceOf(buyer), buyerBefore + buyerRefund);
        assertEq(usdc.balanceOf(treasury), treasuryBefore + fee);
        assertEq(usdc.balanceOf(address(escrow)), 0);
    }

    function testERC20DisputeFullRefund() public {
        TaskEscrow escrow = _createERC20Escrow();
        _fundAndSubmit(escrow);

        vm.prank(buyer);
        escrow.dispute("ipfs://reason");

        uint256 buyerBefore = usdc.balanceOf(buyer);

        vm.prank(arbitrator);
        escrow.resolveDispute(0, "ipfs://resolution");

        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Settled));
        assertEq(usdc.balanceOf(buyer), buyerBefore + AMOUNT);
        assertEq(usdc.balanceOf(address(escrow)), 0);
    }

    function testERC20DisputeFullWorkerPayout() public {
        TaskEscrow escrow = _createERC20Escrow();
        _fundAndSubmit(escrow);

        vm.prank(buyer);
        escrow.dispute("ipfs://reason");

        uint256 workerBefore = usdc.balanceOf(worker);
        uint256 treasuryBefore = usdc.balanceOf(treasury);

        vm.prank(arbitrator);
        escrow.resolveDispute(10_000, "ipfs://resolution");

        uint256 fee = (AMOUNT * FEE_BPS) / 10_000;
        assertEq(usdc.balanceOf(worker), workerBefore + AMOUNT - fee);
        assertEq(usdc.balanceOf(treasury), treasuryBefore + fee);
        assertEq(usdc.balanceOf(address(escrow)), 0);
    }

    // ── ERC20 timeout refund ──

    function testERC20TimeoutRefund() public {
        TaskEscrow escrow = _createERC20Escrow();
        _fundERC20Escrow(escrow);

        vm.warp(block.timestamp + 8 days);
        uint256 buyerBefore = usdc.balanceOf(buyer);

        vm.prank(buyer);
        escrow.claimTimeoutRefund();

        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Refunded));
        assertEq(usdc.balanceOf(buyer), buyerBefore + AMOUNT);
        assertEq(usdc.balanceOf(address(escrow)), 0);
    }

    // ── ERC20 arbitrator timeout ──

    function testERC20ArbitratorTimeout() public {
        TaskEscrow escrow = _createERC20Escrow();
        _fundAndSubmit(escrow);

        vm.prank(buyer);
        escrow.dispute("ipfs://reason");

        vm.warp(block.timestamp + ARB_TIMEOUT + 1);
        uint256 buyerBefore = usdc.balanceOf(buyer);

        vm.prank(buyer);
        escrow.claimArbitratorTimeout();

        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Refunded));
        assertEq(usdc.balanceOf(buyer), buyerBefore + AMOUNT);
        assertEq(usdc.balanceOf(address(escrow)), 0);
    }

    // ── ERC20 verifier reject + silence escalation ──

    function testERC20VerifierRejectPath() public {
        TaskEscrow escrow = _createERC20Escrow();
        _fundAndSubmit(escrow);

        vm.prank(verifier);
        escrow.rejectByVerifier("ipfs://reject");

        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Disputed));
    }

    function testERC20SilenceEscalation() public {
        TaskEscrow escrow = _createERC20Escrow();
        _fundAndSubmit(escrow);

        vm.warp(block.timestamp + REVIEW + 1);
        vm.prank(worker);
        escrow.escalateSilence("ipfs://silence");

        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Disputed));
    }

    // ── ERC20 cancel before funding ──

    function testERC20CancelBeforeFunding() public {
        TaskEscrow escrow = _createERC20Escrow();
        vm.prank(buyer);
        escrow.cancelBeforeFunding();
        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Cancelled));
    }

    // ── Fuzz: ERC20 dispute settlement conserves funds ──

    function testFuzz_ERC20DisputeSettlementConservesFunds(uint16 workerAwardBps) public {
        vm.assume(workerAwardBps <= 10_000);
        TaskEscrow escrow = _createERC20Escrow();
        _fundAndSubmit(escrow);

        vm.prank(buyer);
        escrow.dispute("ipfs://reason");

        uint256 buyerBefore = usdc.balanceOf(buyer);
        uint256 workerBefore = usdc.balanceOf(worker);
        uint256 treasuryBefore = usdc.balanceOf(treasury);

        vm.prank(arbitrator);
        escrow.resolveDispute(workerAwardBps, "ipfs://resolution");

        uint256 workerGross = (AMOUNT * workerAwardBps) / 10_000;
        uint256 fee = (workerGross * FEE_BPS) / 10_000;
        uint256 workerNet = workerGross - fee;
        uint256 buyerRefund = AMOUNT - workerGross;

        assertEq(usdc.balanceOf(worker), workerBefore + workerNet);
        assertEq(usdc.balanceOf(buyer), buyerBefore + buyerRefund);
        assertEq(usdc.balanceOf(treasury), treasuryBefore + fee);
        assertEq(usdc.balanceOf(address(escrow)), 0);
    }

    // ── Non-standard ERC20 (no return value, like USDT) ──

    function testNoReturnERC20HappyPath() public {
        MockERC20NoReturn usdt = new MockERC20NoReturn("Tether", "USDT", 6);
        usdt.mint(buyer, 10_000e6);

        (, address addr) = factory.createEscrow(
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
                token: address(usdt)
            })
        );
        TaskEscrow escrow = TaskEscrow(addr);

        vm.startPrank(buyer);
        usdt.approve(address(escrow), AMOUNT);
        escrow.fund();
        vm.stopPrank();

        assertEq(usdt.balanceOf(address(escrow)), AMOUNT);

        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result");

        uint256 workerBefore = usdt.balanceOf(worker);
        uint256 treasuryBefore = usdt.balanceOf(treasury);

        vm.prank(buyer);
        escrow.approveByBuyer();

        uint256 fee = (AMOUNT * FEE_BPS) / 10_000;
        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Settled));
        assertEq(usdt.balanceOf(worker), workerBefore + (AMOUNT - fee));
        assertEq(usdt.balanceOf(treasury), treasuryBefore + fee);
        assertEq(usdt.balanceOf(address(escrow)), 0);
    }
}
