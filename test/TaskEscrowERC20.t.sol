// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {BaseEscrowTest} from "./BaseEscrowTest.sol";
import {TaskEscrowFactory} from "../src/TaskEscrowFactory.sol";
import {TaskEscrow} from "../src/TaskEscrow.sol";
import {MockERC20, MockERC20NoReturn, MockFeeOnTransferERC20} from "./mocks/MockERC20.sol";

contract TaskEscrowERC20Test is BaseEscrowTest {
    MockERC20 internal usdc;

    uint256 internal constant USDC_AMOUNT = 1000e6; // 1000 USDC (6 decimals)

    function setUp() public override {
        super.setUp();
        usdc = new MockERC20("USD Coin", "USDC", 6);
        usdc.mint(buyer, 10_000e6);
    }

    function _createERC20Escrow() internal returns (TaskEscrow) {
        return _createERC20EscrowWithStake(0);
    }

    function _createERC20EscrowWithStake(uint256 stake) internal returns (TaskEscrow) {
        TaskEscrowFactory.CreateParams memory params = _defaultParams();
        params.amount = USDC_AMOUNT;
        params.workerStake = stake;
        params.token = address(usdc);
        return _create(params);
    }

    // ── ERC20 funding ──

    function testERC20FundTransfersTokens() public {
        TaskEscrow escrow = _createERC20Escrow();
        uint256 buyerBefore = usdc.balanceOf(buyer);

        _fund(escrow);

        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Funded));
        assertEq(usdc.balanceOf(address(escrow)), USDC_AMOUNT);
        assertEq(usdc.balanceOf(buyer), buyerBefore - USDC_AMOUNT);
    }

    function testERC20FundRejectsETH() public {
        TaskEscrow escrow = _createERC20Escrow();
        vm.deal(buyer, 1 ether);

        vm.startPrank(buyer);
        usdc.approve(address(escrow), USDC_AMOUNT);
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

        uint256 fee = (USDC_AMOUNT * FEE_BPS) / 10_000;
        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Settled));
        assertEq(usdc.balanceOf(worker), workerBefore + (USDC_AMOUNT - fee));
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
        escrow.castVerifierVote(true, "");

        uint256 fee = (USDC_AMOUNT * FEE_BPS) / 10_000;
        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Settled));
        assertEq(usdc.balanceOf(worker), workerBefore + (USDC_AMOUNT - fee));
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

        uint256 workerGross = (USDC_AMOUNT * 5000) / 10_000;
        uint256 fee = (workerGross * FEE_BPS) / 10_000;
        uint256 workerNet = workerGross - fee;
        uint256 buyerRefund = USDC_AMOUNT - workerGross;

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
        assertEq(usdc.balanceOf(buyer), buyerBefore + USDC_AMOUNT);
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

        uint256 fee = (USDC_AMOUNT * FEE_BPS) / 10_000;
        assertEq(usdc.balanceOf(worker), workerBefore + USDC_AMOUNT - fee);
        assertEq(usdc.balanceOf(treasury), treasuryBefore + fee);
        assertEq(usdc.balanceOf(address(escrow)), 0);
    }

    // ── ERC20 timeout refund ──

    function testERC20TimeoutRefund() public {
        TaskEscrow escrow = _createERC20Escrow();
        _fund(escrow);

        vm.warp(block.timestamp + 8 days);
        uint256 buyerBefore = usdc.balanceOf(buyer);

        vm.prank(buyer);
        escrow.claimTimeoutRefund();

        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Refunded));
        assertEq(usdc.balanceOf(buyer), buyerBefore + USDC_AMOUNT);
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
        assertEq(usdc.balanceOf(buyer), buyerBefore + USDC_AMOUNT);
        assertEq(usdc.balanceOf(address(escrow)), 0);
    }

    // ── ERC20 verifier reject + silence escalation ──

    function testERC20VerifierRejectPath() public {
        TaskEscrow escrow = _createERC20Escrow();
        _fundAndSubmit(escrow);

        vm.prank(verifier);
        escrow.castVerifierVote(false, "ipfs://reject");

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

        uint256 workerGross = (USDC_AMOUNT * workerAwardBps) / 10_000;
        uint256 fee = (workerGross * FEE_BPS) / 10_000;
        uint256 workerNet = workerGross - fee;
        uint256 buyerRefund = USDC_AMOUNT - workerGross;

        assertEq(usdc.balanceOf(worker), workerBefore + workerNet);
        assertEq(usdc.balanceOf(buyer), buyerBefore + buyerRefund);
        assertEq(usdc.balanceOf(treasury), treasuryBefore + fee);
        assertEq(usdc.balanceOf(address(escrow)), 0);
    }

    // ── Fee-on-transfer token rejection ──

    function testFeeOnTransferTokenReverts() public {
        MockFeeOnTransferERC20 fot = new MockFeeOnTransferERC20("FeeToken", "FOT", 6);
        fot.mint(buyer, 10_000e6);
        TaskEscrowFactory.CreateParams memory params = _defaultParams();
        params.amount = USDC_AMOUNT;
        params.token = address(fot);

        TaskEscrow escrow = _create(params);

        vm.startPrank(buyer);
        fot.approve(address(escrow), USDC_AMOUNT);
        vm.expectRevert(TaskEscrow.InsufficientReceived.selector);
        escrow.fund();
        vm.stopPrank();
    }

    // ── Non-standard ERC20 (no return value, like USDT) ──

    // ── ERC20 Worker Stake Tests ──

    function testERC20WorkerStakeHappyPath() public {
        uint256 stakeAmount = 50e6;
        TaskEscrow escrow = _createERC20EscrowWithStake(stakeAmount);
        usdc.mint(worker, stakeAmount);

        _fund(escrow);

        vm.startPrank(worker);
        usdc.approve(address(escrow), stakeAmount);
        escrow.depositStake();
        vm.stopPrank();

        assertTrue(escrow.workerStaked());

        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result", bytes32(0));

        uint256 workerBefore = usdc.balanceOf(worker);
        uint256 treasuryBefore = usdc.balanceOf(treasury);

        vm.prank(buyer);
        escrow.approveByBuyer();

        uint256 fee = (USDC_AMOUNT * FEE_BPS) / 10_000;
        assertEq(usdc.balanceOf(worker), workerBefore + (USDC_AMOUNT - fee) + stakeAmount);
        assertEq(usdc.balanceOf(treasury), treasuryBefore + fee);
        assertEq(usdc.balanceOf(address(escrow)), 0);
    }

    function testERC20WorkerStakeSubmitWithoutStakeReverts() public {
        uint256 stakeAmount = 50e6;
        TaskEscrow escrow = _createERC20EscrowWithStake(stakeAmount);
        _fund(escrow);

        vm.expectRevert(TaskEscrow.StakeNotDeposited.selector);
        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result", bytes32(0));
    }

    function testERC20WorkerStakeRejectsETH() public {
        uint256 stakeAmount = 50e6;
        TaskEscrow escrow = _createERC20EscrowWithStake(stakeAmount);
        _fund(escrow);
        vm.deal(worker, 1 ether);

        vm.expectRevert(TaskEscrow.ETHNotAccepted.selector);
        vm.prank(worker);
        escrow.depositStake{value: 1 ether}();
    }

    function testERC20WorkerStakeDisputeFullRefund() public {
        uint256 stakeAmount = 50e6;
        TaskEscrow escrow = _createERC20EscrowWithStake(stakeAmount);
        usdc.mint(worker, stakeAmount);

        _fund(escrow);

        vm.startPrank(worker);
        usdc.approve(address(escrow), stakeAmount);
        escrow.depositStake();
        escrow.submit(keccak256("submission"), "ipfs://result", bytes32(0));
        vm.stopPrank();

        vm.prank(buyer);
        escrow.dispute("ipfs://reason");

        uint256 buyerBefore = usdc.balanceOf(buyer);
        uint256 workerBefore = usdc.balanceOf(worker);

        vm.prank(arbitrator);
        escrow.resolveDispute(0, "ipfs://resolution");

        assertEq(usdc.balanceOf(buyer), buyerBefore + USDC_AMOUNT + stakeAmount);
        assertEq(usdc.balanceOf(worker), workerBefore);
        assertEq(usdc.balanceOf(address(escrow)), 0);
    }

    function testERC20WorkerStakeDisputeSplit50_50() public {
        uint256 stakeAmount = 50e6;
        TaskEscrow escrow = _createERC20EscrowWithStake(stakeAmount);
        usdc.mint(worker, stakeAmount);

        _fund(escrow);

        vm.startPrank(worker);
        usdc.approve(address(escrow), stakeAmount);
        escrow.depositStake();
        escrow.submit(keccak256("submission"), "ipfs://result", bytes32(0));
        vm.stopPrank();

        vm.prank(buyer);
        escrow.dispute("ipfs://reason");

        uint256 buyerBefore = usdc.balanceOf(buyer);
        uint256 workerBefore = usdc.balanceOf(worker);
        uint256 treasuryBefore = usdc.balanceOf(treasury);

        vm.prank(arbitrator);
        escrow.resolveDispute(5000, "ipfs://resolution");

        uint256 workerGross = (USDC_AMOUNT * 5000) / 10_000;
        uint256 fee = (workerGross * FEE_BPS) / 10_000;
        uint256 workerNet = workerGross - fee;
        uint256 buyerRefund = USDC_AMOUNT - workerGross;
        uint256 stakeReturn = (stakeAmount * 5000) / 10_000;
        uint256 stakeForfeited = stakeAmount - stakeReturn;

        assertEq(usdc.balanceOf(worker), workerBefore + workerNet + stakeReturn);
        assertEq(usdc.balanceOf(buyer), buyerBefore + buyerRefund + stakeForfeited);
        assertEq(usdc.balanceOf(treasury), treasuryBefore + fee);
        assertEq(usdc.balanceOf(address(escrow)), 0);
    }

    // ── Non-standard ERC20 (no return value, like USDT) ──

    function testNoReturnERC20HappyPath() public {
        MockERC20NoReturn usdt = new MockERC20NoReturn("Tether", "USDT", 6);
        usdt.mint(buyer, 10_000e6);
        TaskEscrowFactory.CreateParams memory params = _defaultParams();
        params.amount = USDC_AMOUNT;
        params.token = address(usdt);

        TaskEscrow escrow = _create(params);

        vm.startPrank(buyer);
        usdt.approve(address(escrow), USDC_AMOUNT);
        escrow.fund();
        vm.stopPrank();

        assertEq(usdt.balanceOf(address(escrow)), USDC_AMOUNT);

        vm.prank(worker);
        escrow.submit(keccak256("submission"), "ipfs://result", bytes32(0));

        uint256 workerBefore = usdt.balanceOf(worker);
        uint256 treasuryBefore = usdt.balanceOf(treasury);

        vm.prank(buyer);
        escrow.approveByBuyer();

        uint256 fee = (USDC_AMOUNT * FEE_BPS) / 10_000;
        assertEq(uint256(escrow.status()), uint256(TaskEscrow.Status.Settled));
        assertEq(usdt.balanceOf(worker), workerBefore + (USDC_AMOUNT - fee));
        assertEq(usdt.balanceOf(treasury), treasuryBefore + fee);
        assertEq(usdt.balanceOf(address(escrow)), 0);
    }
}
