// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {BaseEscrowTest} from "./BaseEscrowTest.sol";
import {TaskEscrowFactory} from "../src/TaskEscrowFactory.sol";
import {TaskEscrow} from "../src/TaskEscrow.sol";

contract TaskEscrowBackupTest is BaseEscrowTest {
    function setUp() public override {
        super.setUp();
        vm.deal(buyer, 100 ether);
        vm.deal(worker, 10 ether);
        vm.deal(backup, 10 ether);
    }

    function _createBackupEscrow() internal returns (TaskEscrow) {
        return _createBackupEscrowWithStake(0);
    }

    function _createBackupEscrowWithStake(uint256 stake) internal returns (TaskEscrow) {
        TaskEscrowFactory.CreateParams memory params = _defaultParams();
        params.workerStake = stake;
        params = _withBackup(params);
        return _create(params);
    }

    function _createBackupMilestoneEscrow() internal returns (TaskEscrow) {
        TaskEscrowFactory.CreateMilestoneParams[] memory ms = new TaskEscrowFactory.CreateMilestoneParams[](3);
        ms[0] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 1 ether, submissionDeadline: uint64(block.timestamp + 7 days)
        });
        ms[1] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 1 ether, submissionDeadline: uint64(block.timestamp + 14 days)
        });
        ms[2] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 1 ether, submissionDeadline: uint64(block.timestamp + 21 days)
        });
        TaskEscrowFactory.CreateParams memory params = _defaultParams();
        params.amount = 3 ether;
        params.submissionDeadline = uint64(block.timestamp + 21 days);
        params = _withBackup(params);
        params.milestones = ms;

        return _create(params);
    }

    // ── Happy path: primary times out, backup submits and gets approved ──

    function testBackupActivationHappyPath() public {
        TaskEscrow e = _createBackupEscrow();

        vm.prank(buyer);
        e.fund{value: AMOUNT}();

        assertEq(e.activeWorker(), worker);

        // Primary misses deadline
        vm.warp(block.timestamp + 8 days);

        vm.prank(buyer);
        e.activateBackup();

        assertEq(e.activeWorker(), backup);
        assertTrue(e.backupActivated());

        // Backup submits within extended deadline
        vm.prank(backup);
        e.submit(keccak256("backup-submission"), "ipfs://backup-result", bytes32(0));

        uint256 backupBefore = backup.balance;
        uint256 treasuryBefore = treasury.balance;

        vm.prank(buyer);
        e.approveByBuyer();

        uint256 fee = (AMOUNT * FEE_BPS) / 10_000;
        assertEq(uint256(e.status()), uint256(TaskEscrow.Status.Settled));
        assertEq(backup.balance, backupBefore + (AMOUNT - fee));
        assertEq(treasury.balance, treasuryBefore + fee);
        assertEq(address(e).balance, 0);
    }

    // ── Backup activation with stake: primary's stake forfeited, backup deposits own ──

    function testBackupActivationWithStake() public {
        TaskEscrow e = _createBackupEscrowWithStake(STAKE);

        vm.prank(buyer);
        e.fund{value: AMOUNT}();

        // Primary deposits stake
        vm.prank(worker);
        e.depositStake{value: STAKE}();
        assertTrue(e.workerStaked());

        uint256 buyerBefore = buyer.balance;

        // Primary misses deadline
        vm.warp(block.timestamp + 8 days);

        vm.prank(buyer);
        e.activateBackup();

        // Primary's stake forfeited to buyer
        assertEq(buyer.balance, buyerBefore + STAKE);
        assertFalse(e.workerStaked());

        // Backup deposits their own stake
        vm.prank(backup);
        e.depositStake{value: STAKE}();
        assertTrue(e.workerStaked());

        // Backup submits
        vm.prank(backup);
        e.submit(keccak256("backup-submission"), "ipfs://backup-result", bytes32(0));

        uint256 backupBefore = backup.balance;

        vm.prank(buyer);
        e.approveByBuyer();

        uint256 fee = (AMOUNT * FEE_BPS) / 10_000;
        assertEq(backup.balance, backupBefore + (AMOUNT - fee) + STAKE);
        assertEq(address(e).balance, 0);
    }

    // ── Milestone mode: backup activated on current milestone timeout ──

    function testBackupActivationMilestone() public {
        TaskEscrow e = _createBackupMilestoneEscrow();

        vm.prank(buyer);
        e.fund{value: 3 ether}();

        // Worker submits and gets approved for milestone 0
        vm.prank(worker);
        e.submitMilestone(0, keccak256("ms0"), "ipfs://ms0", bytes32(0));
        vm.prank(buyer);
        e.approveMilestoneByBuyer(0);

        // Primary misses milestone 1 deadline
        vm.warp(block.timestamp + 15 days);

        vm.prank(buyer);
        e.activateBackup();

        assertEq(e.activeWorker(), backup);

        // Backup submits milestone 1 within extended deadline
        vm.prank(backup);
        e.submitMilestone(1, keccak256("ms1-backup"), "ipfs://ms1-backup", bytes32(0));
        vm.prank(buyer);
        e.approveMilestoneByBuyer(1);

        // Backup submits milestone 2
        vm.prank(backup);
        e.submitMilestone(2, keccak256("ms2-backup"), "ipfs://ms2-backup", bytes32(0));
        vm.prank(buyer);
        e.approveMilestoneByBuyer(2);

        assertEq(uint256(e.status()), uint256(TaskEscrow.Status.Settled));
        assertEq(address(e).balance, 0);
    }

    // ── Cannot activate twice ──

    function testBackupCannotActivateTwice() public {
        TaskEscrow e = _createBackupEscrow();

        vm.prank(buyer);
        e.fund{value: AMOUNT}();

        vm.warp(block.timestamp + 8 days);

        vm.prank(buyer);
        e.activateBackup();

        vm.expectRevert(TaskEscrow.BackupAlreadyActivated.selector);
        vm.prank(buyer);
        e.activateBackup();
    }

    // ── Cannot activate without designation ──

    function testBackupCannotActivateWithoutDesignation() public {
        // Create escrow without backup
        TaskEscrow e = _create(_defaultParams());

        vm.prank(buyer);
        e.fund{value: AMOUNT}();

        vm.warp(block.timestamp + 8 days);

        vm.expectRevert(TaskEscrow.NoBackupDesignated.selector);
        vm.prank(buyer);
        e.activateBackup();
    }

    // ── Cannot activate before deadline ──

    function testBackupCannotActivateBeforeDeadline() public {
        TaskEscrow e = _createBackupEscrow();

        vm.prank(buyer);
        e.fund{value: AMOUNT}();

        vm.expectRevert(TaskEscrow.WindowNotOpen.selector);
        vm.prank(buyer);
        e.activateBackup();
    }

    // ── Cannot activate after submission (wrong state) ──

    function testBackupCannotActivateAfterSubmission() public {
        TaskEscrow e = _createBackupEscrow();

        vm.prank(buyer);
        e.fund{value: AMOUNT}();

        vm.prank(worker);
        e.submit(keccak256("submission"), "ipfs://result", bytes32(0));

        // Escrow is now in Submitted state, not Funded
        vm.expectRevert(TaskEscrow.InvalidState.selector);
        vm.prank(buyer);
        e.activateBackup();
    }

    // ── No backup (address(0)) behaves identically to V1 ──

    function testNoBackupWorkerV1Compatible() public {
        TaskEscrow e = _create(_defaultParams());

        assertEq(e.activeWorker(), worker);
        assertEq(e.backupWorker(), address(0));
        assertFalse(e.backupActivated());

        vm.prank(buyer);
        e.fund{value: AMOUNT}();

        vm.prank(worker);
        e.submit(keccak256("submission"), "ipfs://result", bytes32(0));

        uint256 workerBefore = worker.balance;
        uint256 treasuryBefore = treasury.balance;

        vm.prank(buyer);
        e.approveByBuyer();

        uint256 fee = (AMOUNT * FEE_BPS) / 10_000;
        assertEq(uint256(e.status()), uint256(TaskEscrow.Status.Settled));
        assertEq(worker.balance, workerBefore + (AMOUNT - fee));
        assertEq(treasury.balance, treasuryBefore + fee);
    }

    // ── Backup worker must be distinct from all roles ──

    function testBackupWorkerMustBeDistinct() public {
        // backupWorker == buyer
        TaskEscrowFactory.CreateParams memory params = _defaultParams();
        params.backupWorker = buyer;
        params.backupDeadlineExtension = BACKUP_EXTENSION;
        vm.expectRevert(TaskEscrow.RolesNotDistinct.selector);
        factory.createEscrow(params);

        // backupWorker == worker
        TaskEscrowFactory.CreateParams memory params2 = _defaultParams();
        params2.backupWorker = worker;
        params2.backupDeadlineExtension = BACKUP_EXTENSION;
        vm.expectRevert(TaskEscrow.RolesNotDistinct.selector);
        factory.createEscrow(params2);

        // backupWorker == verifier
        TaskEscrowFactory.CreateParams memory params3 = _defaultParams();
        params3.backupWorker = verifier;
        params3.backupDeadlineExtension = BACKUP_EXTENSION;
        vm.expectRevert(TaskEscrow.RolesNotDistinct.selector);
        factory.createEscrow(params3);

        // backupWorker == arbitrator
        TaskEscrowFactory.CreateParams memory params4 = _defaultParams();
        params4.backupWorker = arbitrator;
        params4.backupDeadlineExtension = BACKUP_EXTENSION;
        vm.expectRevert(TaskEscrow.RolesNotDistinct.selector);
        factory.createEscrow(params4);
    }

    // ── Backup also defaults: buyer claims timeout refund ──

    function testBackupTimeoutRefundIfBackupAlsoDefaults() public {
        TaskEscrow e = _createBackupEscrow();

        vm.prank(buyer);
        e.fund{value: AMOUNT}();

        // Primary misses deadline
        vm.warp(block.timestamp + 8 days);

        vm.prank(buyer);
        e.activateBackup();

        // Backup also misses the extended deadline
        vm.warp(block.timestamp + BACKUP_EXTENSION + 1);

        uint256 buyerBefore = buyer.balance;

        vm.prank(buyer);
        e.claimTimeoutRefund();

        assertEq(uint256(e.status()), uint256(TaskEscrow.Status.Refunded));
        assertEq(buyer.balance, buyerBefore + AMOUNT);
        assertEq(address(e).balance, 0);
    }

    // ── After activation, payout goes to backup worker ──

    function testBackupWorkerReceivesPayout() public {
        TaskEscrow e = _createBackupEscrow();

        vm.prank(buyer);
        e.fund{value: AMOUNT}();

        vm.warp(block.timestamp + 8 days);

        vm.prank(buyer);
        e.activateBackup();

        vm.prank(backup);
        e.submit(keccak256("backup-work"), "ipfs://backup-work", bytes32(0));

        vm.prank(buyer);
        e.dispute("ipfs://reason");

        uint256 backupBefore = backup.balance;
        uint256 buyerBefore = buyer.balance;
        uint256 treasuryBefore = treasury.balance;

        vm.prank(arbitrator);
        e.resolveDispute(5000, "ipfs://resolution");

        uint256 workerGross = (AMOUNT * 5000) / 10_000;
        uint256 fee = (workerGross * FEE_BPS) / 10_000;
        uint256 workerNet = workerGross - fee;
        uint256 buyerRefund = AMOUNT - workerGross;

        assertEq(backup.balance, backupBefore + workerNet);
        assertEq(buyer.balance, buyerBefore + buyerRefund);
        assertEq(treasury.balance, treasuryBefore + fee);
        assertEq(address(e).balance, 0);
    }

    // ── ERC20 backup activation ──
    // (Covered implicitly by the ETH tests since the backup logic is token-agnostic.
    //  The stake forfeiture and payout routing use _send() which handles both.)

    // ── Fuzz: conservation of funds across backup activation + settlement ──

    function testFuzz_BackupConservation(uint16 workerAwardBps) public {
        vm.assume(workerAwardBps <= 10_000);

        TaskEscrow e = _createBackupEscrowWithStake(STAKE);

        vm.prank(buyer);
        e.fund{value: AMOUNT}();

        vm.prank(worker);
        e.depositStake{value: STAKE}();

        // Snapshot all balances after funding + primary stake deposit
        uint256 buyerBefore = buyer.balance;
        uint256 workerBefore = worker.balance;
        uint256 backupBefore = backup.balance;
        uint256 treasuryBefore = treasury.balance;
        uint256 escrowBefore = address(e).balance;

        // Primary misses deadline, backup activated (primary stake -> buyer)
        vm.warp(block.timestamp + 8 days);
        vm.prank(buyer);
        e.activateBackup();

        // Backup deposits stake and submits
        vm.prank(backup);
        e.depositStake{value: STAKE}();

        vm.prank(backup);
        e.submit(keccak256("backup-work"), "ipfs://backup-work", bytes32(0));

        vm.prank(buyer);
        e.dispute("ipfs://reason");

        vm.prank(arbitrator);
        e.resolveDispute(workerAwardBps, "ipfs://resolution");

        // Conservation: sum of all balance changes must equal zero
        int256 buyerDelta = int256(buyer.balance) - int256(buyerBefore);
        int256 workerDelta = int256(worker.balance) - int256(workerBefore);
        int256 backupDelta = int256(backup.balance) - int256(backupBefore);
        int256 treasuryDelta = int256(treasury.balance) - int256(treasuryBefore);
        int256 escrowDelta = int256(address(e).balance) - int256(escrowBefore);

        assertEq(buyerDelta + workerDelta + backupDelta + treasuryDelta + escrowDelta, 0);
        assertEq(address(e).balance, 0);
    }

    // ── Only buyer can activate backup ──

    function testOnlyBuyerCanActivateBackup() public {
        TaskEscrow e = _createBackupEscrow();

        vm.prank(buyer);
        e.fund{value: AMOUNT}();

        vm.warp(block.timestamp + 8 days);

        vm.expectRevert(TaskEscrow.Unauthorized.selector);
        vm.prank(worker);
        e.activateBackup();

        vm.expectRevert(TaskEscrow.Unauthorized.selector);
        vm.prank(backup);
        e.activateBackup();
    }
}
