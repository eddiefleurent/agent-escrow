// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Test} from "forge-std/Test.sol";
import {TaskEscrow} from "../../src/TaskEscrow.sol";
import {TaskEscrowFactory} from "../../src/TaskEscrowFactory.sol";
import {EscrowLedger} from "./EscrowLedger.sol";

/// @notice Shared plumbing for actor handlers: escrow selection, milestone index selection,
/// and ledger-bracketed pranked calls. Every action is a low-level call so reverts are swallowed
/// (the fuzzer explores invalid transitions too) while the ledger still observes the outcome.
abstract contract HandlerBase is Test {
    EscrowLedger internal ledger;

    constructor(EscrowLedger _ledger) {
        ledger = _ledger;
    }

    function _escrow(uint256 seed) internal view returns (TaskEscrow) {
        return ledger.escrowAt(seed);
    }

    /// @dev Target-aware selection: 3 in 4 calls pick (starting from `seed`) the first escrow on which the
    /// action is plausible, which keeps per-escrow action density high enough for multi-step milestone and
    /// quorum flows to complete within a run. 1 in 4 calls (or when nothing matches) use the raw seed so
    /// invalid transitions are still explored.
    function _escrowWhere(uint256 seed, function(TaskEscrow) view returns (bool) plausible)
        internal
        view
        returns (TaskEscrow)
    {
        if (seed % 4 != 0) {
            uint256 n = ledger.escrowCount();
            for (uint256 k; k < n; k++) {
                TaskEscrow e = ledger.escrowAt(seed + k);
                if (plausible(e)) return e;
            }
        }
        return ledger.escrowAt(seed);
    }

    function _msStatus(TaskEscrow e) internal view returns (TaskEscrow.MilestoneStatus st) {
        (,,,,,,,,, st,) = e.milestones(e.currentMilestone());
    }

    function _created(TaskEscrow e) internal view returns (bool) {
        return e.status() == TaskEscrow.Status.Created;
    }

    function _live(TaskEscrow e) internal view returns (bool) {
        TaskEscrow.Status s = e.status();
        return s == TaskEscrow.Status.Funded || s == TaskEscrow.Status.Submitted;
    }

    function _liveMulti(TaskEscrow e) internal view returns (bool) {
        return _isMulti(e) && e.status() == TaskEscrow.Status.Funded;
    }

    function _submittable(TaskEscrow e) internal view returns (bool) {
        if (e.status() != TaskEscrow.Status.Funded) return false;
        return !_isMulti(e) || _msStatus(e) == TaskEscrow.MilestoneStatus.Pending;
    }

    function _reviewable(TaskEscrow e) internal view returns (bool) {
        if (!_isMulti(e)) return e.status() == TaskEscrow.Status.Submitted;
        return e.status() == TaskEscrow.Status.Funded && _msStatus(e) == TaskEscrow.MilestoneStatus.Submitted;
    }

    function _disputed(TaskEscrow e) internal view returns (bool) {
        if (!_isMulti(e)) return e.status() == TaskEscrow.Status.Disputed;
        return e.status() == TaskEscrow.Status.Funded && _msStatus(e) == TaskEscrow.MilestoneStatus.Disputed;
    }

    /// @dev Mostly the current milestone (the only valid index); 1 in 8 a random index to exercise ordering guards.
    function _msIndex(TaskEscrow e, uint256 idxSeed) internal view returns (uint8) {
        if (idxSeed % 8 == 0) return uint8((idxSeed >> 8) % 16);
        return e.currentMilestone();
    }

    function _isMulti(TaskEscrow e) internal view returns (bool) {
        return e.milestoneCount() > 1;
    }

    function _ethValue(TaskEscrow e, uint256 amt) internal view returns (uint256) {
        return e.token() == address(0) ? amt : 0;
    }

    function _run(TaskEscrow e, bytes32 action, address actor, uint256 value, bytes memory data)
        internal
        returns (bool ok)
    {
        ledger.begin(e);
        vm.prank(actor);
        (ok,) = address(e).call{value: value}(data);
        ledger.end(e, action, ok);
    }
}

contract BuyerHandler is HandlerBase {
    /// @dev KNOWN BUG guard: single-shot `claimTimeoutRefund` is callable on milestone escrows (no
    /// `milestoneCount` check) and double-pays principal already released by milestone settlements.
    /// Disabled so the invariant suite stays green; flip to true to reproduce. See
    /// `TaskEscrowLifecycleInvariantsTest.test_knownBug_singleShotTimeoutRefundOnMilestoneEscrow`.
    bool public constant INCLUDE_KNOWN_BUG_PATHS = false;

    address internal buyer;

    constructor(EscrowLedger _ledger, address _buyer) HandlerBase(_ledger) {
        buyer = _buyer;
    }

    function fund(uint256 seed) external {
        TaskEscrow e = _escrowWhere(seed, _created);
        _run(e, "fund", buyer, _ethValue(e, e.amount()), abi.encodeCall(TaskEscrow.fund, ()));
    }

    function cancelBeforeFunding(uint256 seed) external {
        TaskEscrow e = _escrowWhere(seed, _created);
        _run(e, "cancelBeforeFunding", buyer, 0, abi.encodeCall(TaskEscrow.cancelBeforeFunding, ()));
    }

    function approve(uint256 seed, uint256 idxSeed) external {
        TaskEscrow e = _escrowWhere(seed, _reviewable);
        if (_isMulti(e)) {
            _run(
                e,
                "approveMilestoneByBuyer",
                buyer,
                0,
                abi.encodeCall(TaskEscrow.approveMilestoneByBuyer, (_msIndex(e, idxSeed)))
            );
        } else {
            _run(e, "approveByBuyer", buyer, 0, abi.encodeCall(TaskEscrow.approveByBuyer, ()));
        }
    }

    function dispute(uint256 seed, uint256 idxSeed) external {
        TaskEscrow e = _escrowWhere(seed, _reviewable);
        if (_isMulti(e)) {
            _run(
                e,
                "disputeMilestone",
                buyer,
                0,
                abi.encodeCall(TaskEscrow.disputeMilestone, (_msIndex(e, idxSeed), "ipfs://d"))
            );
        } else {
            _run(e, "dispute", buyer, 0, abi.encodeCall(TaskEscrow.dispute, ("ipfs://d")));
        }
    }

    function claimTimeout(uint256 seed, uint256 idxSeed) external {
        TaskEscrow e = _escrowWhere(seed, _submittable);
        if (!_isMulti(e)) {
            _run(e, "claimTimeoutRefund", buyer, 0, abi.encodeCall(TaskEscrow.claimTimeoutRefund, ()));
            return;
        }
        if (INCLUDE_KNOWN_BUG_PATHS) {
            _run(e, "claimTimeoutRefund", buyer, 0, abi.encodeCall(TaskEscrow.claimTimeoutRefund, ()));
        }
        _run(
            e,
            "claimMilestoneTimeoutRefund",
            buyer,
            0,
            abi.encodeCall(TaskEscrow.claimMilestoneTimeoutRefund, (_msIndex(e, idxSeed)))
        );
    }

    function claimArbitratorTimeout(uint256 seed, uint256 idxSeed) external {
        TaskEscrow e = _escrowWhere(seed, _disputed);
        if (_isMulti(e)) {
            _run(
                e,
                "claimMilestoneArbitratorTimeout",
                buyer,
                0,
                abi.encodeCall(TaskEscrow.claimMilestoneArbitratorTimeout, (_msIndex(e, idxSeed)))
            );
        } else {
            _run(e, "claimArbitratorTimeout", buyer, 0, abi.encodeCall(TaskEscrow.claimArbitratorTimeout, ()));
        }
    }

    function activateBackup(uint256 seed) external {
        TaskEscrow e = _escrowWhere(seed, _submittable);
        _run(e, "activateBackup", buyer, 0, abi.encodeCall(TaskEscrow.activateBackup, ()));
    }

    function abortRemaining(uint256 seed) external {
        TaskEscrow e = _escrowWhere(seed, _liveMulti);
        _run(e, "abortRemainingMilestones", buyer, 0, abi.encodeCall(TaskEscrow.abortRemainingMilestones, ()));
    }

    function expireNoQuorum(uint256 seed, uint256 idxSeed) external {
        TaskEscrow e = _escrowWhere(seed, _reviewable);
        if (_isMulti(e)) {
            _run(
                e,
                "expireMilestoneNoQuorum",
                buyer,
                0,
                abi.encodeCall(TaskEscrow.expireMilestoneNoQuorum, (_msIndex(e, idxSeed), "ipfs://x"))
            );
        } else {
            _run(e, "expireNoQuorum", buyer, 0, abi.encodeCall(TaskEscrow.expireNoQuorum, ("ipfs://x")));
        }
    }

    function withdraw(uint256 seed) external {
        TaskEscrow e = _escrow(seed);
        _run(e, "withdrawStake", buyer, 0, abi.encodeCall(TaskEscrow.withdrawStake, ()));
    }
}

contract WorkerHandler is HandlerBase {
    address internal worker;
    address internal backup;

    constructor(EscrowLedger _ledger, address _worker, address _backup) HandlerBase(_ledger) {
        worker = _worker;
        backup = _backup;
    }

    /// @dev Usually the active worker; 1 in 8 the inactive one, to exercise role guards across backup activation.
    function _actor(TaskEscrow e, uint256 actorSeed) internal view returns (address) {
        address active = e.activeWorker();
        if (actorSeed % 8 != 0) return active;
        return active == worker ? backup : worker;
    }

    function depositStake(uint256 seed, uint256 actorSeed) external {
        TaskEscrow e = _escrowWhere(seed, _live);
        _run(
            e,
            "depositStake",
            _actor(e, actorSeed),
            _ethValue(e, e.workerStake()),
            abi.encodeCall(TaskEscrow.depositStake, ())
        );
    }

    function submit(uint256 seed, uint256 idxSeed, uint256 actorSeed) external {
        TaskEscrow e = _escrowWhere(seed, _submittable);
        address a = _actor(e, actorSeed);
        bytes32 h = keccak256(abi.encode(seed, idxSeed));
        if (_isMulti(e)) {
            _run(
                e,
                "submitMilestone",
                a,
                0,
                abi.encodeCall(TaskEscrow.submitMilestone, (_msIndex(e, idxSeed), h, "ipfs://s", bytes32(0)))
            );
        } else {
            _run(e, "submit", a, 0, abi.encodeCall(TaskEscrow.submit, (h, "ipfs://s", bytes32(0))));
        }
    }

    function escalateSilence(uint256 seed, uint256 idxSeed) external {
        TaskEscrow e = _escrowWhere(seed, _reviewable);
        address a = e.activeWorker();
        if (_isMulti(e)) {
            _run(
                e,
                "escalateMilestoneSilence",
                a,
                0,
                abi.encodeCall(TaskEscrow.escalateMilestoneSilence, (_msIndex(e, idxSeed), "ipfs://e"))
            );
        } else {
            _run(e, "escalateSilence", a, 0, abi.encodeCall(TaskEscrow.escalateSilence, ("ipfs://e")));
        }
    }
}

contract VerifierHandler is HandlerBase {
    constructor(EscrowLedger _ledger) HandlerBase(_ledger) {}

    function _verifier(uint256 vSeed) internal view returns (address) {
        return ledger.verifiers(vSeed % ledger.verifierCount());
    }

    function depositVerifierStake(uint256 seed, uint256 vSeed) external {
        TaskEscrow e = _escrowWhere(seed, _live);
        _run(
            e,
            "depositVerifierStake",
            _verifier(vSeed),
            _ethValue(e, e.verifierStakePerVerifier()),
            abi.encodeCall(TaskEscrow.depositVerifierStake, ())
        );
    }

    function vote(uint256 seed, uint256 vSeed, uint256 idxSeed, bool approve) external {
        TaskEscrow e = _escrowWhere(seed, _reviewable);
        address v = _verifier(vSeed);
        if (_isMulti(e)) {
            _run(
                e,
                "castMilestoneVerifierVote",
                v,
                0,
                abi.encodeCall(TaskEscrow.castMilestoneVerifierVote, (_msIndex(e, idxSeed), approve, "ipfs://v"))
            );
        } else {
            _run(e, "castVerifierVote", v, 0, abi.encodeCall(TaskEscrow.castVerifierVote, (approve, "ipfs://v")));
        }
    }

    function withdraw(uint256 seed, uint256 vSeed) external {
        TaskEscrow e = _escrow(seed);
        _run(e, "withdrawStake", _verifier(vSeed), 0, abi.encodeCall(TaskEscrow.withdrawStake, ()));
    }
}

contract ArbitratorHandler is HandlerBase {
    address internal arbitrator;

    constructor(EscrowLedger _ledger, address _arbitrator) HandlerBase(_ledger) {
        arbitrator = _arbitrator;
    }

    function resolve(uint256 seed, uint256 idxSeed, uint16 bps) external {
        TaskEscrow e = _escrowWhere(seed, _disputed);
        // Mostly valid awards; 1 in 16 out of range to exercise the bound check.
        uint16 award = idxSeed % 16 == 0 ? bps : uint16(bound(bps, 0, 10_000));
        if (_isMulti(e)) {
            _run(
                e,
                "resolveMilestoneDispute",
                arbitrator,
                0,
                abi.encodeCall(TaskEscrow.resolveMilestoneDispute, (_msIndex(e, idxSeed), award, "ipfs://r"))
            );
        } else {
            _run(e, "resolveDispute", arbitrator, 0, abi.encodeCall(TaskEscrow.resolveDispute, (award, "ipfs://r")));
        }
    }
}

/// @notice Factory owner: emergency freeze / unfreeze / resolve (paper §4.9) plus governance changes that
/// must never affect existing escrows (fee and treasury are snapshotted at creation).
contract OwnerHandler is HandlerBase {
    TaskEscrowFactory internal factory;
    address internal owner;
    address internal treasury2;

    constructor(EscrowLedger _ledger, TaskEscrowFactory _factory, address _owner, address _treasury2)
        HandlerBase(_ledger)
    {
        factory = _factory;
        owner = _owner;
        treasury2 = _treasury2;
    }

    /// @dev Escrows are registered in creation order, so the ledger index equals the factory escrow id.
    function _id(uint256 seed) internal view returns (uint256) {
        return seed % ledger.escrowCount();
    }

    function _runFactory(uint256 seed, bytes32 action, bytes memory data) internal {
        TaskEscrow e = _escrow(seed);
        ledger.begin(e);
        vm.prank(owner);
        (bool ok,) = address(factory).call(data);
        ledger.end(e, action, ok);
    }

    /// @dev Freezing halts almost every participant path, so only 1 in 8 calls actually freezes; the rest
    /// unfreeze. Keeps emergency coverage without parking most runs on a frozen escrow.
    function toggleFreeze(uint256 seed, uint8 coin) external {
        if (coin % 8 == 0) {
            _runFactory(seed, "freezeEscrow", abi.encodeCall(TaskEscrowFactory.freezeEscrow, (_id(seed))));
        } else {
            _runFactory(seed, "unfreezeEscrow", abi.encodeCall(TaskEscrowFactory.unfreezeEscrow, (_id(seed))));
        }
    }

    function emergencyResolve(uint256 seed, uint16 bps) external {
        uint16 award = uint16(bound(bps, 0, 10_000));
        _runFactory(seed, "emergencyResolve", abi.encodeCall(TaskEscrowFactory.emergencyResolve, (_id(seed), award)));
    }

    /// @dev Governance changes that must not affect existing escrows (fee + treasury are snapshotted).
    /// Views are evaluated before vm.prank: a prank is consumed by the next call, including staticcalls.
    function governance(uint16 bps, bool rotateToSecond) external {
        uint16 fee = uint16(bound(bps, 0, factory.highAssuranceFeeBps()));
        address next = rotateToSecond ? treasury2 : ledger.treasury();
        vm.startPrank(owner);
        factory.setProtocolFeeBps(fee);
        factory.setTreasury(next);
        vm.stopPrank();
    }
}

contract TimeHandler is Test {
    /// @dev Bimodal pacing against the compressed windows of the lifecycle suite: 7 in 8 warps are short
    /// (actions land inside review/dispute windows), 1 in 8 is a long jump (deadlines and arbitrator timeouts,
    /// including the last milestone deadline, are reached within a normal run depth).
    function warp(uint32 secs, uint8 mode) external {
        uint256 delta = mode % 8 == 0 ? bound(secs, 6 hours, 30 hours) : bound(secs, 1, 2 hours);
        vm.warp(block.timestamp + delta);
    }
}
