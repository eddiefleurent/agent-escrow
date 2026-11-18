// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {StdInvariant} from "forge-std/StdInvariant.sol";
import {console} from "forge-std/console.sol";
import {BaseEscrowTest} from "./BaseEscrowTest.sol";
import {TaskEscrowFactory} from "../src/TaskEscrowFactory.sol";
import {TaskEscrow} from "../src/TaskEscrow.sol";
import {MockERC20} from "./mocks/MockERC20.sol";
import {EscrowLedger} from "./invariants/EscrowLedger.sol";
import {
    BuyerHandler,
    WorkerHandler,
    VerifierHandler,
    ArbitratorHandler,
    OwnerHandler,
    TimeHandler
} from "./invariants/EscrowHandlers.sol";

/// @notice Multi-escrow, multi-actor invariant suite covering milestone escrows, quorum with verifier stakes,
/// worker stakes, backup activation, ERC20 and ETH denominations, high-assurance tier, and emergency
/// freeze/resolve. Invariants are derived from docs/SPEC.md §6 and the emergency invariant in §7.
contract TaskEscrowLifecycleInvariantsTest is StdInvariant, BaseEscrowTest {
    uint16 internal constant HIGH_FEE_BPS = 250;
    // Compressed windows so the fuzzer crosses every deadline within a normal run depth.
    uint64 internal constant INV_REVIEW = 12 hours;
    uint64 internal constant INV_DISPUTE = 12 hours;
    uint64 internal constant INV_ARB_TIMEOUT = 18 hours;
    uint64 internal constant INV_DEADLINE = 36 hours;
    uint64 internal constant INV_MS_SPACING = 24 hours;

    address internal verifier2 = makeAddr("verifier2");
    address internal verifier3 = makeAddr("verifier3");
    address internal treasury2 = makeAddr("treasury2");

    MockERC20 internal usdc;
    EscrowLedger internal ledger;
    TaskEscrow[] internal escrows;
    address[] internal participants;
    uint256 internal initialEthTotal;
    uint256 internal initialTokenTotal;

    function setUp() public override {
        factory = new TaskEscrowFactory(FEE_BPS, HIGH_FEE_BPS, treasury, owner);
        usdc = new MockERC20("USD Coin", "USDC", 6);

        address[] memory panel = new address[](3);
        panel[0] = verifier;
        panel[1] = verifier2;
        panel[2] = verifier3;

        // 0: ETH single-shot, 2-of-3 quorum, verifier + worker stakes, backup
        TaskEscrowFactory.CreateParams memory p = _withBackupInv(_withQuorum(_invParams(), panel, 2, 0.05 ether));
        p.workerStake = STAKE;
        escrows.push(_create(p));

        // 1: ERC20 single-shot, same shape
        p = _withBackupInv(_withQuorum(_invParams(), panel, 2, 50e6));
        p.token = address(usdc);
        p.amount = 1000e6;
        p.workerStake = 100e6;
        escrows.push(_create(p));

        // 2: ETH 3-milestone, 2-of-3 quorum, verifier + worker stakes, backup
        p = _withBackupInv(_withQuorum(_withMilestones(_invParams(), 3, 1 ether, INV_MS_SPACING), panel, 2, 0.05 ether));
        p.workerStake = 0.3 ether;
        escrows.push(_create(p));

        // 3: ERC20 3-milestone, 1-of-3 quorum, verifier + worker stakes, backup
        p = _withBackupInv(_withQuorum(_withMilestones(_invParams(), 3, 1000e6, INV_MS_SPACING), panel, 1, 50e6));
        p.token = address(usdc);
        p.workerStake = 300e6;
        escrows.push(_create(p));

        // 4: ETH 2-milestone, high-assurance tier (verifier-only approval), 3-of-3 quorum, no stakes,
        //    uneven amounts to exercise fee/award rounding
        p = _withQuorum(_invParams(), panel, 3, 0);
        p.serviceTier = 1;
        p.milestones = new TaskEscrowFactory.CreateMilestoneParams[](2);
        p.milestones[0] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 1 ether + 3, submissionDeadline: uint64(block.timestamp + 18 hours)
        });
        p.milestones[1] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 9_999, submissionDeadline: uint64(block.timestamp + 36 hours)
        });
        p.amount = 1 ether + 3 + 9_999;
        p.submissionDeadline = uint64(block.timestamp + 36 hours);
        escrows.push(_create(p));

        address[10] memory all =
            [buyer, worker, backup, verifier, verifier2, verifier3, arbitrator, treasury, owner, treasury2];
        for (uint256 i; i < all.length; i++) {
            participants.push(all[i]);
        }
        ledger = new EscrowLedger(buyer, worker, backup, arbitrator, treasury, owner, treasury2, panel);
        for (uint256 i; i < escrows.length; i++) {
            ledger.register(escrows[i]);
        }

        // Fund actors. ERC20 escrows are pre-approved so handlers only need to prank the call itself.
        address[6] memory payers = [buyer, worker, backup, verifier, verifier2, verifier3];
        for (uint256 i; i < payers.length; i++) {
            vm.deal(payers[i], 1000 ether);
            usdc.mint(payers[i], 1_000_000e6);
            for (uint256 j; j < escrows.length; j++) {
                if (escrows[j].token() == address(usdc)) {
                    vm.prank(payers[i]);
                    usdc.approve(address(escrows[j]), type(uint256).max);
                }
            }
        }

        // Start most escrows mid-lifecycle so the fuzzer spends its depth on review/dispute/settlement paths.
        // Escrow 4 stays in Created to keep fund / cancelBeforeFunding / emergency-from-Created reachable.
        for (uint256 i; i < 4; i++) {
            _fund(escrows[i]);
        }
        _depositStake(escrows[0]);
        _depositStake(escrows[2]);

        initialEthTotal = _systemTotal(address(0));
        initialTokenTotal = _systemTotal(address(usdc));

        BuyerHandler buyerH = new BuyerHandler(ledger, buyer);
        WorkerHandler workerH = new WorkerHandler(ledger, worker, backup);
        VerifierHandler verifierH = new VerifierHandler(ledger);
        ArbitratorHandler arbitratorH = new ArbitratorHandler(ledger, arbitrator);
        OwnerHandler ownerH = new OwnerHandler(ledger, factory, owner, treasury2);
        TimeHandler timeH = new TimeHandler();

        targetContract(address(buyerH));
        targetContract(address(workerH));
        targetContract(address(verifierH));
        targetContract(address(arbitratorH));
        targetContract(address(ownerH));
        targetContract(address(timeH));
    }

    // ── Helpers ──

    function _invParams() internal view returns (TaskEscrowFactory.CreateParams memory p) {
        p = _defaultParams();
        p.reviewPeriodSeconds = INV_REVIEW;
        p.disputePeriodSeconds = INV_DISPUTE;
        p.arbitratorTimeoutSeconds = INV_ARB_TIMEOUT;
        p.submissionDeadline = uint64(block.timestamp + INV_DEADLINE);
    }

    function _withBackupInv(TaskEscrowFactory.CreateParams memory p)
        internal
        view
        returns (TaskEscrowFactory.CreateParams memory)
    {
        p = _withBackup(p);
        p.backupDeadlineExtension = 18 hours;
        return p;
    }

    function _bal(address tok, address who) internal view returns (uint256) {
        return tok == address(0) ? who.balance : usdc.balanceOf(who);
    }

    function _systemTotal(address tok) internal view returns (uint256 total) {
        for (uint256 i; i < participants.length; i++) {
            total += _bal(tok, participants[i]);
        }
        for (uint256 i; i < escrows.length; i++) {
            total += _bal(tok, address(escrows[i]));
        }
    }

    function _isTerminal(TaskEscrow e) internal view returns (bool) {
        return e.status() >= TaskEscrow.Status.Settled;
    }

    function _msStatus(TaskEscrow e, uint256 i) internal view returns (TaskEscrow.MilestoneStatus st) {
        (,,,,,,,,, st,) = e.milestones(i);
    }

    function _msAmount(TaskEscrow e, uint256 i) internal view returns (uint256 amt) {
        (amt,,,,,,,,,,) = e.milestones(i);
    }

    function _msTerminal(TaskEscrow.MilestoneStatus st) internal pure returns (bool) {
        return st == TaskEscrow.MilestoneStatus.Approved || st == TaskEscrow.MilestoneStatus.Resolved
            || st == TaskEscrow.MilestoneStatus.Cancelled;
    }

    /// @dev Escrow principal not yet distributed, derived purely from on-chain state.
    function _owedPrincipal(TaskEscrow e) internal view returns (uint256 owed) {
        TaskEscrow.Status s = e.status();
        if (s == TaskEscrow.Status.Created || s >= TaskEscrow.Status.Settled) return 0;
        if (e.milestoneCount() <= 1) return e.amount();
        for (uint256 i; i < e.milestoneCount(); i++) {
            if (!_msTerminal(_msStatus(e, i))) owed += _msAmount(e, i);
        }
    }

    function _heldWorkerStake(TaskEscrow e) internal view returns (uint256) {
        TaskEscrow.Status s = e.status();
        bool live = s == TaskEscrow.Status.Funded || s == TaskEscrow.Status.Submitted || s == TaskEscrow.Status.Disputed;
        return live && e.workerStaked() ? e.workerStake() : 0;
    }

    function _heldVerifierStake(TaskEscrow e) internal view returns (uint256) {
        return uint256(e.quorumStakeCount()) * e.verifierStakePerVerifier();
    }

    // ── Invariants ──

    /// SPEC §6.1 balance conservation: escrow balance == undistributed principal + held worker stake
    /// + held verifier stakes + withdrawable balances. Exact equality, ETH and ERC20.
    function invariant_valueConservation() public view {
        for (uint256 i; i < escrows.length; i++) {
            TaskEscrow e = escrows[i];
            uint256 expected =
                _owedPrincipal(e) + _heldWorkerStake(e) + _heldVerifierStake(e) + ledger.sumWithdrawable(e);
            assertEq(_bal(e.token(), address(e)), expected, "escrow balance != owed + held + withdrawable");
        }
    }

    /// Closed system: no value is created or leaks to an address outside the participant set.
    function invariant_closedSystemConservation() public view {
        assertEq(_systemTotal(address(0)), initialEthTotal, "ETH created or leaked");
        assertEq(_systemTotal(address(usdc)), initialTokenTotal, "ERC20 created or leaked");
    }

    /// After a terminal state nothing is stranded: only explicitly withdrawable balances remain and
    /// no verifier stake is still marked as deposited.
    function invariant_terminalNothingStranded() public view {
        for (uint256 i; i < escrows.length; i++) {
            TaskEscrow e = escrows[i];
            if (!_isTerminal(e)) continue;
            assertEq(e.quorumStakeCount(), 0, "terminal escrow still holds verifier stakes");
            assertEq(_bal(e.token(), address(e)), ledger.sumWithdrawable(e), "terminal escrow holds stranded funds");
        }
    }

    /// SPEC §6.2 terminal exclusivity: Settled/Refunded/Cancelled are absorbing; funds only leave via withdrawStake.
    function invariant_terminalStatesAbsorbing() public view {
        for (uint256 i; i < escrows.length; i++) {
            EscrowLedger.Ghost memory g = ledger.ghost(address(escrows[i]));
            assertFalse(g.terminalLeft, "terminal state was left");
            assertFalse(g.terminalOutflow, "funds left terminal escrow outside withdrawStake");
        }
    }

    /// SPEC §6.3 / §6.8 fund conservation: principal released (per settlement events) never exceeds `amount`
    /// and always equals `amount - owed` once funded.
    function invariant_principalAccounting() public view {
        for (uint256 i; i < escrows.length; i++) {
            TaskEscrow e = escrows[i];
            EscrowLedger.Ghost memory g = ledger.ghost(address(e));
            assertLe(g.principalReleased, e.amount(), "released more principal than funded");
            TaskEscrow.Status s = e.status();
            if (s == TaskEscrow.Status.Created || s == TaskEscrow.Status.Cancelled) {
                assertEq(g.principalReleased, 0, "principal released from an unfunded escrow");
            } else {
                assertEq(g.principalReleased, e.amount() - _owedPrincipal(e), "principal events != state");
            }
        }
    }

    /// Milestone payouts (worker net + fee) sum to at most the total funded, and the worker-awarded part never
    /// exceeds what was released.
    function invariant_milestonePayoutsBounded() public view {
        for (uint256 i; i < escrows.length; i++) {
            TaskEscrow e = escrows[i];
            EscrowLedger.Ghost memory g = ledger.ghost(address(e));
            assertLe(g.msWorkerGross, e.amount(), "milestone payouts exceed total funded");
            assertLe(g.workerGross, g.principalReleased, "worker awarded more than released");
        }
    }

    /// SPEC §6.9: each verifier stake settles exactly once per review cycle. Deposits == credited + still held,
    /// the stake counter matches per-verifier flags, and nobody withdraws more than was credited.
    function invariant_verifierStakeSettlesOnce() public view {
        for (uint256 i; i < escrows.length; i++) {
            TaskEscrow e = escrows[i];
            EscrowLedger.Ghost memory g = ledger.ghost(address(e));
            assertEq(
                g.verifierStakeIn, g.withdrawableCredited + _heldVerifierStake(e), "verifier stake not settled once"
            );
            assertLe(g.withdrawn, g.withdrawableCredited, "withdrew more than credited");
            uint8 staked;
            for (uint256 v; v < ledger.verifierCount(); v++) {
                if (e.quorumStaked(ledger.verifiers(v))) staked++;
            }
            assertEq(staked, e.quorumStakeCount(), "quorumStakeCount out of sync with quorumStaked");
        }
    }

    /// SPEC §6.4 / §9: fees go only to the snapshotted treasury, equal reported fees exactly, never exceed the
    /// snapshotted bps of the worker gross award, and the snapshot never changes after creation.
    function invariant_feesBoundedAndOnlyToTreasury() public view {
        for (uint256 i; i < escrows.length; i++) {
            TaskEscrow e = escrows[i];
            EscrowLedger.Ghost memory g = ledger.ghost(address(e));
            uint16 bps = e.protocolFeeBpsSnapshot();
            assertEq(bps, ledger.feeSnapshotAtCreation(address(e)), "fee snapshot changed");
            assertEq(e.treasurySnapshot(), ledger.treasuryAtCreation(address(e)), "treasury snapshot changed");
            assertLe(bps, 10_000, "fee bps above 100%");
            assertLe(g.fees * 10_000, g.workerGross * bps, "fee exceeds configured bps");
            assertLe(g.fees, g.workerGross, "fee exceeds worker gross award");
            assertEq(g.treasuryReceived, g.fees, "treasury received != reported fees");
            assertFalse(g.treasuryMismatch, "treasury delta mismatched fees within a call");
            assertFalse(g.strayFlow, "funds moved to/from a non-payee");
        }
    }

    /// SPEC §6.7 milestone ordering: everything before `currentMilestone` is terminal; while the escrow is live,
    /// the current milestone is not terminal and later ones are untouched (Pending) or aborted (Cancelled).
    function invariant_milestoneOrdering() public view {
        for (uint256 i; i < escrows.length; i++) {
            TaskEscrow e = escrows[i];
            uint8 mc = e.milestoneCount();
            if (mc <= 1) continue;
            uint8 cur = e.currentMilestone();
            assertLt(cur, mc, "currentMilestone out of range");
            for (uint8 m; m < cur; m++) {
                assertTrue(_msTerminal(_msStatus(e, m)), "earlier milestone not terminal");
            }
            if (e.status() != TaskEscrow.Status.Funded) continue;
            assertFalse(_msTerminal(_msStatus(e, cur)), "live escrow parked on a terminal milestone");
            for (uint8 m = cur + 1; m < mc; m++) {
                TaskEscrow.MilestoneStatus st = _msStatus(e, m);
                assertTrue(
                    st == TaskEscrow.MilestoneStatus.Pending || st == TaskEscrow.MilestoneStatus.Cancelled,
                    "future milestone already progressed"
                );
            }
        }
    }

    /// A completed milestone escrow (Settled/Refunded) has no milestone left in flight: every milestone is
    /// Approved, Resolved, or Cancelled. (Cancelled escrows never funded, so their milestones stay Pending.)
    function invariant_completedMilestoneEscrowHasNoLiveMilestone() public view {
        for (uint256 i; i < escrows.length; i++) {
            TaskEscrow e = escrows[i];
            TaskEscrow.Status s = e.status();
            if (e.milestoneCount() <= 1 || (s != TaskEscrow.Status.Settled && s != TaskEscrow.Status.Refunded)) {
                continue;
            }
            for (uint256 m; m < e.milestoneCount(); m++) {
                assertTrue(_msTerminal(_msStatus(e, m)), "completed escrow left a milestone in flight");
            }
        }
    }

    /// SPEC §6.6 backup exclusivity: activation happens at most once and `activeWorker` reflects it.
    function invariant_backupExclusivity() public view {
        for (uint256 i; i < escrows.length; i++) {
            TaskEscrow e = escrows[i];
            EscrowLedger.Ghost memory g = ledger.ghost(address(e));
            assertLe(g.backupActivations, 1, "backup activated more than once");
            assertEq(e.backupActivated(), g.backupActivations == 1, "backupActivated flag out of sync");
            assertEq(e.activeWorker(), e.backupActivated() ? e.backupWorker() : e.worker(), "wrong activeWorker");
        }
    }

    /// Coverage diagnostics (visible with -vv): how often each transition actually succeeded.
    function afterInvariant() external view {
        bytes32[22] memory actions = [
            bytes32("fund"),
            "depositStake",
            "depositVerifierStake",
            "submit",
            "submitMilestone",
            "approveByBuyer",
            "approveMilestoneByBuyer",
            "castVerifierVote",
            "castMilestoneVerifierVote",
            "dispute",
            "disputeMilestone",
            "escalateSilence",
            "escalateMilestoneSilence",
            "resolveDispute",
            "resolveMilestoneDispute",
            "claimTimeoutRefund",
            "claimMilestoneTimeoutRefund",
            "claimArbitratorTimeout",
            "activateBackup",
            "abortRemainingMilestones",
            "emergencyResolve",
            "withdrawStake"
        ];
        for (uint256 i; i < actions.length; i++) {
            console.log(string(abi.encodePacked(actions[i])), ledger.successes(actions[i]));
        }
    }
}

/// @notice Deterministic reproductions of contract bugs surfaced while building the invariant suite.
/// Each is skipped (src/ is out of scope for this PR) and asserts the *correct* behaviour, so un-skipping
/// it after a fix turns it into a regression test.
contract TaskEscrowKnownBugsTest is BaseEscrowTest {
    address internal verifier2 = makeAddr("verifier2");
    address internal verifier3 = makeAddr("verifier3");

    // KNOWN BUG: single-shot `claimTimeoutRefund()` is callable on a multi-milestone escrow. It only checks
    // `status == Funded` (which holds for the whole milestone cycle) and the escrow-level `submissionDeadline`,
    // never `milestoneCount`. It then pays the buyer the full `amount` (+ worker stake) even though milestones
    // were already settled, so the shortfall is taken from verifier stakes sitting in `withdrawable[]`: those
    // verifiers can no longer withdraw. If `submissionDeadline` is earlier than the last milestone deadline,
    // it also lets the buyer bypass in-flight milestone reviews. Breaks invariant_valueConservation and
    // invariant_principalAccounting (reproduce by setting BuyerHandler.INCLUDE_KNOWN_BUG_PATHS = true).
    function test_knownBug_singleShotTimeoutRefundOnMilestoneEscrow() public {
        vm.skip(true);

        address[] memory panel = new address[](3);
        panel[0] = verifier;
        panel[1] = verifier2;
        panel[2] = verifier3;
        TaskEscrowFactory.CreateParams memory p =
            _withQuorum(_withMilestones(_defaultParams(), 3, 1 ether, 3 days), panel, 2, 1 ether);
        TaskEscrow e = _create(p);
        vm.deal(buyer, 10 ether);
        vm.deal(verifier, 1 ether);
        vm.deal(verifier2, 1 ether);

        _fund(e);
        vm.prank(verifier);
        e.depositVerifierStake{value: 1 ether}();
        vm.prank(verifier2);
        e.depositVerifierStake{value: 1 ether}();
        _submitMs(e, 0);
        _approveMs(e, 0); // pays milestone 0 to worker; verifier stakes move to withdrawable[]

        vm.warp(p.submissionDeadline + 1);

        // Expected: the single-shot path is rejected for milestone escrows.
        vm.prank(buyer);
        vm.expectRevert(TaskEscrow.InvalidState.selector);
        e.claimTimeoutRefund();

        // And every verifier can still recover its stake.
        vm.prank(verifier);
        e.withdrawStake();
        vm.prank(verifier2);
        e.withdrawStake();
    }
}
