// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Test} from "forge-std/Test.sol";
import {Vm} from "forge-std/Vm.sol";
import {TaskEscrow} from "../../src/TaskEscrow.sol";
import {IERC20} from "../../src/interfaces/IERC20.sol";

/// @notice Ghost-state ledger shared by all invariant handlers.
/// @dev Every handler action is bracketed by `begin(e)` / `end(e)`. Because each action targets exactly one
/// escrow, every balance delta observed between the two calls is attributable to that escrow. The ledger
/// combines three independent sources of truth so that a bug in one is caught by another:
///   1. actual token/ETH balance deltas of every participant,
///   2. settlement events emitted by the escrow (Settled, MilestoneSettled, Refunded, RemainingMilestonesAborted),
///   3. on-chain escrow state (status, stake counters, withdrawable balances).
/// The ledger is not a fuzz target; only the actor handlers are.
contract EscrowLedger is Test {
    bytes32 internal constant SETTLED_SIG = keccak256("Settled(uint256,uint256,uint256,uint256)");
    bytes32 internal constant MS_SETTLED_SIG = keccak256("MilestoneSettled(uint8,uint256,uint256,uint256)");
    bytes32 internal constant REFUNDED_SIG = keccak256("Refunded(uint256,uint256)");
    bytes32 internal constant ABORTED_SIG = keccak256("RemainingMilestonesAborted(uint8,uint256)");
    bytes32 internal constant BACKUP_SIG = keccak256("BackupActivated(address,address,uint64)");

    struct Ghost {
        // Settlement events
        uint256 principalReleased; // principal paid out of escrow (to worker, buyer, or treasury)
        uint256 workerGross; // worker net + fee, i.e. the worker-awarded part of the principal
        uint256 msWorkerGross; // same, restricted to MilestoneSettled events
        uint256 fees; // protocol fees reported in events
        // Observed balance deltas
        uint256 treasuryReceived;
        uint256 workerStakeIn; // worker stake deposits (primary + backup)
        uint256 verifierStakeIn; // verifier stake deposits
        uint256 withdrawableCredited; // total credited to withdrawable[] (verifier stake settlements)
        uint256 withdrawn; // total pulled via withdrawStake
        uint256 backupActivations;
        // Violation flags (checked by invariants, set during `end`)
        bool terminalLeft; // a terminal state transitioned to another state
        bool terminalOutflow; // funds left a terminal escrow other than through withdrawStake
        bool treasuryMismatch; // treasury delta != fee events for a call
        bool strayFlow; // a non-payee (arbitrator, owner, rotated treasury) received or lost funds
    }

    address[] public escrowList;
    mapping(address => Ghost) internal _ghosts;
    mapping(address => uint16) public feeSnapshotAtCreation;
    mapping(address => address) public treasuryAtCreation;
    mapping(bytes32 => uint256) public successes; // per-action success counters (coverage diagnostics)

    // Participants whose balances are tracked.
    address public buyer;
    address public worker;
    address public backup;
    address public arbitrator;
    address public treasury;
    address public owner;
    address public treasury2; // treasury the owner may rotate to; existing escrows must never pay it
    address[] public verifiers;

    // Per-call snapshot
    struct Snap {
        address escrow;
        uint256 escrowBal;
        uint256 treasuryBal;
        uint256 strayBal;
        uint256 sumWithdrawable;
        uint8 quorumStakeCount;
        uint8 status;
    }

    Snap internal _snap;

    constructor(
        address _buyer,
        address _worker,
        address _backup,
        address _arbitrator,
        address _treasury,
        address _owner,
        address _treasury2,
        address[] memory _verifiers
    ) {
        buyer = _buyer;
        worker = _worker;
        backup = _backup;
        arbitrator = _arbitrator;
        treasury = _treasury;
        owner = _owner;
        treasury2 = _treasury2;
        verifiers = _verifiers;
    }

    function register(TaskEscrow e) external {
        escrowList.push(address(e));
        feeSnapshotAtCreation[address(e)] = e.protocolFeeBpsSnapshot();
        treasuryAtCreation[address(e)] = e.treasurySnapshot();
    }

    function escrowCount() external view returns (uint256) {
        return escrowList.length;
    }

    function escrowAt(uint256 seed) external view returns (TaskEscrow) {
        return TaskEscrow(escrowList[seed % escrowList.length]);
    }

    function ghost(address e) external view returns (Ghost memory) {
        return _ghosts[e];
    }

    function verifierCount() external view returns (uint256) {
        return verifiers.length;
    }

    // ── Balance helpers ──

    function balanceOf(address tok, address who) public view returns (uint256) {
        return tok == address(0) ? who.balance : IERC20(tok).balanceOf(who);
    }

    /// @dev withdrawable[] is only ever credited to panel verifiers and the buyer.
    function sumWithdrawable(TaskEscrow e) public view returns (uint256 s) {
        s = e.withdrawable(buyer);
        for (uint256 i; i < verifiers.length; i++) {
            s += e.withdrawable(verifiers[i]);
        }
    }

    function _strayBalance(address tok) internal view returns (uint256) {
        return balanceOf(tok, arbitrator) + balanceOf(tok, owner) + balanceOf(tok, treasury2);
    }

    // ── Bracketing ──

    function begin(TaskEscrow e) external {
        address tok = e.token();
        _snap = Snap({
            escrow: address(e),
            escrowBal: balanceOf(tok, address(e)),
            treasuryBal: balanceOf(tok, treasury),
            strayBal: _strayBalance(tok),
            sumWithdrawable: sumWithdrawable(e),
            quorumStakeCount: e.quorumStakeCount(),
            status: uint8(e.status())
        });
        vm.recordLogs();
    }

    function end(TaskEscrow e, bytes32 action, bool ok) external {
        require(_snap.escrow == address(e), "ledger: unbalanced begin/end");
        Ghost storage g = _ghosts[address(e)];
        address tok = e.token();
        if (ok) successes[action]++;

        // vm.recordLogs also captures logs emitted inside calls that later revert, so drain the buffer
        // unconditionally but only account for events from calls that actually took effect.
        _recordEvents(e, g, ok);

        // Treasury: every unit it receives in this call must be a fee reported by this escrow.
        uint256 tNow = balanceOf(tok, treasury);
        uint256 tDelta = tNow >= _snap.treasuryBal ? tNow - _snap.treasuryBal : 0;
        if (tNow < _snap.treasuryBal) g.treasuryMismatch = true;
        g.treasuryReceived += tDelta;

        if (_strayBalance(tok) != _snap.strayBal) g.strayFlow = true;

        // Stake deposits: quorumStakeCount only grows via depositVerifierStake.
        uint8 qsc = e.quorumStakeCount();
        if (qsc > _snap.quorumStakeCount) {
            g.verifierStakeIn += uint256(qsc - _snap.quorumStakeCount) * e.verifierStakePerVerifier();
        }
        if (action == "depositStake" && ok) g.workerStakeIn += e.workerStake();

        // withdrawable[] only grows via stake settlement and only shrinks via withdrawStake.
        uint256 sw = sumWithdrawable(e);
        if (sw >= _snap.sumWithdrawable) g.withdrawableCredited += sw - _snap.sumWithdrawable;
        else g.withdrawn += _snap.sumWithdrawable - sw;

        // Terminal states are absorbing: status frozen, and only withdrawStake may move funds out.
        uint8 st = uint8(e.status());
        if (_snap.status >= uint8(TaskEscrow.Status.Settled)) {
            if (st != _snap.status) g.terminalLeft = true;
            uint256 bal = balanceOf(tok, address(e));
            uint256 withdrawnNow = _snap.sumWithdrawable > sw ? _snap.sumWithdrawable - sw : 0;
            if (bal + withdrawnNow != _snap.escrowBal) g.terminalOutflow = true;
        }

        _snap.escrow = address(0);
    }

    function _recordEvents(TaskEscrow e, Ghost storage g, bool ok) internal {
        Vm.Log[] memory logs = vm.getRecordedLogs();
        uint256 callFees;
        for (uint256 i; ok && i < logs.length; i++) {
            if (logs[i].emitter != address(e)) continue;
            bytes32 sig = logs[i].topics[0];
            if (sig == SETTLED_SIG) {
                (uint256 wn, uint256 br, uint256 fee,) = abi.decode(logs[i].data, (uint256, uint256, uint256, uint256));
                g.principalReleased += wn + br + fee;
                g.workerGross += wn + fee;
                g.fees += fee;
                callFees += fee;
            } else if (sig == MS_SETTLED_SIG) {
                (uint256 wn, uint256 br, uint256 fee) = abi.decode(logs[i].data, (uint256, uint256, uint256));
                g.principalReleased += wn + br + fee;
                g.workerGross += wn + fee;
                g.msWorkerGross += wn + fee;
                g.fees += fee;
                callFees += fee;
            } else if (sig == REFUNDED_SIG) {
                (uint256 amt,) = abi.decode(logs[i].data, (uint256, uint256));
                g.principalReleased += amt;
            } else if (sig == ABORTED_SIG) {
                (, uint256 refund) = abi.decode(logs[i].data, (uint8, uint256));
                g.principalReleased += refund;
            } else if (sig == BACKUP_SIG) {
                g.backupActivations++;
            }
        }
        // Per-call: treasury delta must equal exactly the fees this call reported.
        address tok = e.token();
        uint256 tNow = balanceOf(tok, treasury);
        if (tNow >= _snap.treasuryBal && tNow - _snap.treasuryBal != callFees) g.treasuryMismatch = true;
    }
}
