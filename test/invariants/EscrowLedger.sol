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
///   1. actual token/ETH balance deltas of every participant, each checked against the amount the
///      settlement events (plus stake that left the escrow) assign to that participant,
///   2. settlement events emitted by the escrow (Settled, MilestoneSettled, Refunded, RemainingMilestonesAborted),
///   3. on-chain escrow state (status, stake counters, withdrawable balances).
/// The ledger is not a fuzz target; only the actor handlers are.
contract EscrowLedger is Test {
    bytes32 internal constant SETTLED_SIG = keccak256("Settled(uint256,uint256,uint256,uint256)");
    bytes32 internal constant MS_SETTLED_SIG = keccak256("MilestoneSettled(uint8,uint256,uint256,uint256)");
    bytes32 internal constant REFUNDED_SIG = keccak256("Refunded(uint256,uint256)");
    bytes32 internal constant ABORTED_SIG = keccak256("RemainingMilestonesAborted(uint8,uint256)");
    bytes32 internal constant BACKUP_SIG = keccak256("BackupActivated(address,address,uint64)");
    bytes32 internal constant FUNDED_SIG = keccak256("EscrowFunded(address,uint256)");
    bytes32 internal constant STAKE_DEP_SIG = keccak256("WorkerStakeDeposited(address,uint256)");
    bytes32 internal constant VSTAKE_DEP_SIG = keccak256("QuorumVerifierStakeDeposited(address,uint256)");
    bytes32 internal constant VSTAKE_WD_SIG = keccak256("QuorumVerifierStakeWithdrawn(address,uint256)");

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
        bool recipientMismatch; // a party's ETH/token delta != the amount events assign that party
    }

    struct DualSnap {
        uint256 eth;
        uint256 tok;
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
        bool workerStaked;
        uint256 workerStakeAmt;
    }

    Snap internal _snap;
    DualSnap internal _buyerSnap;
    DualSnap internal _workerSnap;
    DualSnap internal _backupSnap;
    DualSnap[] internal _verifierSnaps;

    // Expected balance deltas for the call in progress, in the escrow's denomination.
    // Positive means the party should have received funds; negative means they should have paid in.
    int256 internal _expBuyer;
    int256 internal _expWorker;
    int256 internal _expBackup;
    int256[] internal _expVerifiers;
    uint256 internal _stakeReturned; // Settled.workerStakeReturned summed over this call
    uint256 internal _refundedStake; // Refunded.workerStakeForfeited summed over this call

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

    function _dual(address tok, address who) internal view returns (DualSnap memory) {
        return DualSnap({eth: who.balance, tok: tok == address(0) ? 0 : IERC20(tok).balanceOf(who)});
    }

    /// @dev Worker stake still sitting in the escrow. Terminal (and pre-fund) states hold none,
    /// even if `workerStaked` was left set by a settlement path that does not clear the flag.
    function _stakeHeld(uint8 status, bool staked, uint256 stake) internal pure returns (uint256) {
        if (!staked) return 0;
        if (
            status == uint8(TaskEscrow.Status.Funded) || status == uint8(TaskEscrow.Status.Submitted)
                || status == uint8(TaskEscrow.Status.Disputed)
        ) return stake;
        return 0;
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
            status: uint8(e.status()),
            workerStaked: e.workerStaked(),
            workerStakeAmt: e.workerStake()
        });
        _buyerSnap = _dual(tok, buyer);
        _workerSnap = _dual(tok, worker);
        _backupSnap = _dual(tok, backup);
        _verifierSnaps = new DualSnap[](verifiers.length);
        for (uint256 i; i < verifiers.length; i++) {
            _verifierSnaps[i] = _dual(tok, verifiers[i]);
        }
        vm.recordLogs();
    }

    function end(TaskEscrow e, bytes32 action, bool ok) external {
        require(_snap.escrow == address(e), "ledger: unbalanced begin/end");
        Ghost storage g = _ghosts[address(e)];
        address tok = e.token();
        if (ok) successes[action]++;

        _resetExpectations();
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

        // Stake that left the escrow but is not named by Settled/Refunded is the forfeited
        // portion (backup activation, dispute split, milestone stake settlement) and is paid to the buyer.
        if (ok) _creditUnreportedStake(e, g);
        _checkParties(e, g);

        _snap.escrow = address(0);
    }

    function _resetExpectations() internal {
        _expBuyer = 0;
        _expWorker = 0;
        _expBackup = 0;
        _stakeReturned = 0;
        _refundedStake = 0;
        _expVerifiers = new int256[](verifiers.length);
    }

    /// @dev Worker-awarded principal and returned stake are paid to `activeWorker`, which is the
    /// backup after activation. Attributing them to `worker()` would let a payout to the original
    /// worker pass unnoticed.
    function _workerPayee(TaskEscrow e) internal view returns (address) {
        return e.activeWorker();
    }

    function _credit(address who, int256 amt) internal {
        if (amt == 0 || who == address(0)) return;
        if (who == buyer) {
            _expBuyer += amt;
        } else if (who == worker) {
            _expWorker += amt;
        } else if (who == backup) {
            _expBackup += amt;
        } else {
            for (uint256 i; i < verifiers.length; i++) {
                if (verifiers[i] == who) {
                    _expVerifiers[i] += amt;
                    return;
                }
            }
        }
    }

    function _recordEvents(TaskEscrow e, Ghost storage g, bool ok) internal {
        Vm.Log[] memory logs = vm.getRecordedLogs();
        uint256 callFees;
        address payee = _workerPayee(e);
        for (uint256 i; ok && i < logs.length; i++) {
            if (logs[i].emitter != address(e)) continue;
            bytes32 sig = logs[i].topics[0];
            if (sig == SETTLED_SIG) {
                (uint256 wn, uint256 br, uint256 fee, uint256 sr) =
                    abi.decode(logs[i].data, (uint256, uint256, uint256, uint256));
                g.principalReleased += wn + br + fee;
                g.workerGross += wn + fee;
                g.fees += fee;
                callFees += fee;
                _credit(payee, _signed(wn + sr));
                _credit(buyer, _signed(br));
                _stakeReturned += sr;
            } else if (sig == MS_SETTLED_SIG) {
                (uint256 wn, uint256 br, uint256 fee) = abi.decode(logs[i].data, (uint256, uint256, uint256));
                g.principalReleased += wn + br + fee;
                g.workerGross += wn + fee;
                g.msWorkerGross += wn + fee;
                g.fees += fee;
                callFees += fee;
                _credit(payee, _signed(wn));
                _credit(buyer, _signed(br));
            } else if (sig == REFUNDED_SIG) {
                (uint256 amt, uint256 sf) = abi.decode(logs[i].data, (uint256, uint256));
                g.principalReleased += amt;
                _credit(buyer, _signed(amt + sf));
                _refundedStake += sf;
            } else if (sig == ABORTED_SIG) {
                (, uint256 refund) = abi.decode(logs[i].data, (uint8, uint256));
                g.principalReleased += refund;
                _credit(buyer, _signed(refund));
            } else if (sig == BACKUP_SIG) {
                g.backupActivations++;
            } else if (sig == FUNDED_SIG) {
                _credit(_topicAddr(logs[i]), -_signed(abi.decode(logs[i].data, (uint256))));
            } else if (sig == STAKE_DEP_SIG || sig == VSTAKE_DEP_SIG) {
                _credit(_topicAddr(logs[i]), -_signed(abi.decode(logs[i].data, (uint256))));
            } else if (sig == VSTAKE_WD_SIG) {
                _credit(_topicAddr(logs[i]), _signed(abi.decode(logs[i].data, (uint256))));
            }
        }
        // Per-call: treasury delta must equal exactly the fees this call reported.
        address tok = e.token();
        uint256 tNow = balanceOf(tok, treasury);
        if (tNow >= _snap.treasuryBal && tNow - _snap.treasuryBal != callFees) g.treasuryMismatch = true;
    }

    /// @dev Stake that left the escrow beyond what Settled/Refunded already assigned is forfeited to the buyer
    /// (backup activation sends the whole stake; dispute and milestone settlement send the non-returned part).
    function _creditUnreportedStake(TaskEscrow e, Ghost storage g) internal {
        uint256 heldBefore = _stakeHeld(_snap.status, _snap.workerStaked, _snap.workerStakeAmt);
        uint256 heldAfter = _stakeHeld(uint8(e.status()), e.workerStaked(), e.workerStake());
        uint256 reported = _stakeReturned + _refundedStake;
        if (heldBefore > heldAfter) {
            uint256 stakeOut = heldBefore - heldAfter;
            if (reported > stakeOut) g.recipientMismatch = true;
            else _credit(buyer, _signed(stakeOut - reported));
        } else if (reported != 0) {
            g.recipientMismatch = true;
        }
    }

    function _checkParties(TaskEscrow e, Ghost storage g) internal {
        address tok = e.token();
        _checkParty(g, tok, buyer, _buyerSnap, _expBuyer);
        _checkParty(g, tok, worker, _workerSnap, _expWorker);
        _checkParty(g, tok, backup, _backupSnap, _expBackup);
        for (uint256 i; i < verifiers.length; i++) {
            _checkParty(g, tok, verifiers[i], _verifierSnaps[i], _expVerifiers[i]);
        }
    }

    /// @dev The escrow's own denomination must move by exactly `expected`. The other currency must not move:
    /// an ETH payout on an ERC20 escrow (or the reverse) is a misdirected transfer.
    function _checkParty(Ghost storage g, address tok, address who, DualSnap memory before, int256 expected) internal {
        int256 ethDelta = _signedDelta(before.eth, who.balance);
        if (tok == address(0)) {
            if (ethDelta != expected) g.recipientMismatch = true;
            return;
        }
        int256 tokDelta = _signedDelta(before.tok, IERC20(tok).balanceOf(who));
        if (tokDelta != expected || ethDelta != 0) g.recipientMismatch = true;
    }

    function _topicAddr(Vm.Log memory log) internal pure returns (address) {
        return address(uint160(uint256(log.topics[1])));
    }

    function _signed(uint256 v) internal pure returns (int256) {
        return int256(v);
    }

    function _signedDelta(uint256 beforeBal, uint256 afterBal) internal pure returns (int256) {
        if (afterBal >= beforeBal) return int256(afterBal - beforeBal);
        return -int256(beforeBal - afterBal);
    }
}
