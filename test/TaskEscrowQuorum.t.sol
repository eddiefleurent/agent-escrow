// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Test} from "forge-std/Test.sol";
import {TaskEscrowFactory} from "../src/TaskEscrowFactory.sol";
import {TaskEscrow} from "../src/TaskEscrow.sol";
import {MockZKVerifier} from "./mocks/MockZKVerifier.sol";

contract TaskEscrowQuorumTest is Test {
    TaskEscrowFactory internal factory;
    MockZKVerifier internal zk;

    address internal owner = makeAddr("owner");
    address internal treasury = makeAddr("treasury");
    address internal buyer = makeAddr("buyer");
    address internal worker = makeAddr("worker");
    address internal verifierA = makeAddr("verifierA");
    address internal verifierB = makeAddr("verifierB");
    address internal verifierC = makeAddr("verifierC");
    address internal arbitrator = makeAddr("arbitrator");
    address internal outsider = makeAddr("outsider");

    uint16 internal constant FEE_BPS = 100;
    uint64 internal constant REVIEW = 1 days;
    uint64 internal constant DISPUTE = 2 days;
    uint64 internal constant ARB_TIMEOUT = 7 days;
    bytes32 internal constant CIRCUIT_ID = keccak256("quorum-circuit");

    function setUp() public {
        factory = new TaskEscrowFactory(FEE_BPS, FEE_BPS, treasury, owner);
        zk = new MockZKVerifier();
        vm.txGasPrice(0);

        vm.deal(buyer, 100 ether);
        vm.deal(worker, 100 ether);
        vm.deal(verifierA, 100 ether);
        vm.deal(verifierB, 100 ether);
        vm.deal(verifierC, 100 ether);
    }

    function testDegeneratePanelApprove() public {
        (TaskEscrow e,) = _createSingle(1, 1, 0, 0, address(0), bytes32(0));
        _fundAndSubmit(e, bytes32(0));

        vm.prank(verifierA);
        e.castVerifierVote(true, "");

        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Settled));
    }

    function testDegeneratePanelReject() public {
        (TaskEscrow e,) = _createSingle(1, 1, 0, 0, address(0), bytes32(0));
        _fundAndSubmit(e, bytes32(0));

        vm.prank(verifierA);
        e.castVerifierVote(false, "ipfs://reject");

        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Disputed));
    }

    function testTwoOfThreeApproveAutoFinalizes() public {
        (TaskEscrow e,) = _createSingle(2, 3, 0, 0, address(0), bytes32(0));
        _fundAndSubmit(e, bytes32(0));

        vm.prank(verifierA);
        e.castVerifierVote(true, "");
        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Submitted));

        vm.prank(verifierB);
        e.castVerifierVote(true, "");
        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Settled));
    }

    function testTwoOfThreeMajorityRejects() public {
        (TaskEscrow e,) = _createSingle(2, 3, 0, 0, address(0), bytes32(0));
        _fundAndSubmit(e, bytes32(0));

        vm.prank(verifierA);
        e.castVerifierVote(false, "ipfs://reject-a");

        vm.prank(verifierB);
        e.castVerifierVote(false, "ipfs://reject-b");

        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Disputed));
        assertEq(e.disputeReasonURI(), "ipfs://reject-b");
    }

    function testSchellingStakeMajorityRefundMinoritySlash() public {
        uint256 stake = 0.2 ether;
        (TaskEscrow e,) = _createSingle(2, 3, stake, 0, address(0), bytes32(0));
        _fundAndSubmit(e, bytes32(0));

        vm.prank(verifierA);
        e.depositVerifierStake{value: stake}();
        vm.prank(verifierB);
        e.depositVerifierStake{value: stake}();
        vm.prank(verifierC);
        e.depositVerifierStake{value: stake}();

        uint256 buyerBefore = buyer.balance;
        uint256 aAfterDeposit = verifierA.balance;
        uint256 bAfterDeposit = verifierB.balance;
        uint256 cAfterDeposit = verifierC.balance;

        vm.prank(verifierA);
        e.castVerifierVote(false, "ipfs://reject");
        vm.prank(verifierB);
        e.castVerifierVote(true, "");
        vm.prank(verifierC);
        e.castVerifierVote(true, "");

        // Pull-based settlement: claim owed stakes before checking balances.
        vm.prank(verifierB);
        e.withdrawStake();
        vm.prank(verifierC);
        e.withdrawStake();
        vm.prank(buyer);
        e.withdrawStake();

        assertEq(verifierA.balance, aAfterDeposit);
        assertEq(verifierB.balance, bAfterDeposit + stake);
        assertEq(verifierC.balance, cAfterDeposit + stake);
        assertEq(buyer.balance, buyerBefore + stake);
    }

    function testBuyerApprovalRefundsDepositedVerifierStake() public {
        uint256 stake = 0.2 ether;
        (TaskEscrow e,) = _createSingle(2, 3, stake, 0, address(0), bytes32(0));
        _fundAndSubmit(e, bytes32(0));

        vm.prank(verifierA);
        e.depositVerifierStake{value: stake}();
        uint256 verifierAfterDeposit = verifierA.balance;

        vm.prank(buyer);
        e.approveByBuyer();

        vm.prank(verifierA);
        e.withdrawStake();

        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Settled));
        assertEq(verifierA.balance, verifierAfterDeposit + stake);
        assertFalse(e.quorumStaked(verifierA));
        assertEq(uint256(e.quorumStakeCount()), 0);
    }

    function testDisputeResolutionRefundsUnsettledVerifierStakes() public {
        uint256 stake = 0.2 ether;
        (TaskEscrow e,) = _createSingle(2, 3, stake, 0, address(0), bytes32(0));
        _fundAndSubmit(e, bytes32(0));

        vm.prank(verifierA);
        e.depositVerifierStake{value: stake}();
        uint256 verifierAfterDeposit = verifierA.balance;

        vm.prank(verifierA);
        e.castVerifierVote(true, "");
        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Submitted));

        vm.prank(buyer);
        e.dispute("ipfs://manual-dispute");
        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Disputed));

        vm.prank(arbitrator);
        e.resolveDispute(5000, "ipfs://resolution");

        vm.prank(verifierA);
        e.withdrawStake();

        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Settled));
        assertEq(verifierA.balance, verifierAfterDeposit + stake);
        assertFalse(e.quorumStaked(verifierA));
        assertEq(uint256(e.quorumStakeCount()), 0);
    }

    function testClaimTimeoutRefundReturnsVerifierStake() public {
        uint256 stake = 0.2 ether;
        (TaskEscrow e,) = _createSingle(2, 3, stake, 0, address(0), bytes32(0));

        uint256 amount = e.amount();
        vm.prank(buyer);
        e.fund{value: amount}();

        vm.prank(verifierA);
        e.depositVerifierStake{value: stake}();
        uint256 verifierAfterDeposit = verifierA.balance;

        vm.warp(uint256(e.submissionDeadline()) + 1);
        vm.prank(buyer);
        e.claimTimeoutRefund();

        vm.prank(verifierA);
        e.withdrawStake();

        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Refunded));
        assertEq(verifierA.balance, verifierAfterDeposit + stake);
        assertFalse(e.quorumStaked(verifierA));
        assertEq(uint256(e.quorumStakeCount()), 0);
    }

    function testVoteAfterQuorumReachedReverts() public {
        (TaskEscrow e,) = _createSingle(2, 3, 0, 0, address(0), bytes32(0));
        _fundAndSubmit(e, bytes32(0));

        vm.prank(verifierA);
        e.castVerifierVote(true, "");
        vm.prank(verifierB);
        e.castVerifierVote(true, "");

        vm.expectRevert(TaskEscrow.QuorumFinalized.selector);
        vm.prank(verifierC);
        e.castVerifierVote(true, "");
    }

    function testNonPanelVerifierReverts() public {
        (TaskEscrow e,) = _createSingle(2, 3, 0, 0, address(0), bytes32(0));
        _fundAndSubmit(e, bytes32(0));

        vm.expectRevert(TaskEscrow.NotQuorumVerifier.selector);
        vm.prank(outsider);
        e.castVerifierVote(true, "");
    }

    function testCountOneOnlyFirstVerifierCanVoteSingleShot() public {
        (TaskEscrow e,) = _createSingle(1, 1, 0, 0, address(0), bytes32(0));
        _fundAndSubmit(e, bytes32(0));

        vm.expectRevert(TaskEscrow.NotQuorumVerifier.selector);
        vm.prank(verifierB);
        e.castVerifierVote(true, "");
    }

    function testCountOneOnlyFirstVerifierCanVoteMilestone() public {
        (TaskEscrow e,) = _createTwoMilestoneEscrow(1, 1, 0);
        vm.prank(buyer);
        e.fund{value: 2 ether}();
        vm.prank(worker);
        e.submitMilestone(0, keccak256("ms0"), "ipfs://ms0", bytes32(0));

        vm.expectRevert(TaskEscrow.NotQuorumVerifier.selector);
        vm.prank(verifierB);
        e.castMilestoneVerifierVote(0, true, "");
    }

    function testDoubleVoteReverts() public {
        (TaskEscrow e,) = _createSingle(2, 3, 0, 0, address(0), bytes32(0));
        _fundAndSubmit(e, bytes32(0));

        vm.prank(verifierA);
        e.castVerifierVote(true, "");

        vm.expectRevert(TaskEscrow.AlreadyVoted.selector);
        vm.prank(verifierA);
        e.castVerifierVote(true, "");
    }

    function testMissingStakeReverts() public {
        (TaskEscrow e,) = _createSingle(2, 3, 0.1 ether, 0, address(0), bytes32(0));
        _fundAndSubmit(e, bytes32(0));

        vm.expectRevert(TaskEscrow.QuorumStakeRequired.selector);
        vm.prank(verifierA);
        e.castVerifierVote(true, "");
    }

    function testZKProofCountsAsOneQuorumVote() public {
        (TaskEscrow e,) = _createSingle(2, 3, 0, 0, address(zk), CIRCUIT_ID);

        bytes memory proof = abi.encodePacked("proof-ok");
        bytes32 proofHash = keccak256(proof);
        _fundAndSubmit(e, proofHash);

        vm.prank(verifierA);
        e.verifyAndApprove(proof);
        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Submitted));

        vm.prank(verifierB);
        e.castVerifierVote(true, "");
        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Settled));
    }

    function testZKVerifyRequiresStakeWhenVerifierStakeConfigured() public {
        uint256 stake = 0.2 ether;
        (TaskEscrow e,) = _createSingle(2, 3, stake, 0, address(zk), CIRCUIT_ID);

        bytes memory proof = abi.encodePacked("proof-with-stake");
        bytes32 proofHash = keccak256(proof);
        _fundAndSubmit(e, proofHash);

        vm.expectRevert(TaskEscrow.QuorumStakeRequired.selector);
        vm.prank(verifierA);
        e.verifyAndApprove(proof);

        vm.prank(verifierA);
        e.depositVerifierStake{value: stake}();

        vm.prank(verifierA);
        e.verifyAndApprove(proof);
        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Submitted));
    }

    function testHighAssuranceStillRejectsBuyerApproval() public {
        (TaskEscrow e,) = _createSingle(1, 1, 0, 1, address(0), bytes32(0));
        _fundAndSubmit(e, bytes32(0));

        vm.expectRevert(TaskEscrow.HighAssuranceRequiresVerifier.selector);
        vm.prank(buyer);
        e.approveByBuyer();
    }

    function testEmergencyResolveWhileQuorumInProgress() public {
        uint256 stake = 0.2 ether;
        (TaskEscrow e, uint256 escrowId) = _createSingle(2, 3, stake, 0, address(0), bytes32(0));
        _fundAndSubmit(e, bytes32(0));

        vm.prank(verifierA);
        e.depositVerifierStake{value: stake}();
        uint256 verifierAfterDeposit = verifierA.balance;
        uint256 escrowBeforeResolve = address(e).balance;

        vm.prank(verifierA);
        e.castVerifierVote(true, "");
        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Submitted));

        vm.prank(owner);
        factory.freezeEscrow(escrowId);

        vm.prank(owner);
        factory.emergencyResolve(escrowId, 5_000);

        vm.prank(verifierA);
        e.withdrawStake();

        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Settled));
        assertEq(verifierA.balance, verifierAfterDeposit + stake);
        assertEq(address(e).balance, 0);
        assertEq(escrowBeforeResolve, e.amount() + stake);
        assertFalse(e.quorumStaked(verifierA));
        assertEq(uint256(e.quorumStakeCount()), 0);
    }

    function testMilestoneQuorumApproveAndAdvance() public {
        (TaskEscrow e,) = _createTwoMilestoneEscrow(2, 3, 0);
        vm.prank(buyer);
        e.fund{value: 2 ether}();

        vm.prank(worker);
        e.submitMilestone(0, keccak256("ms0"), "ipfs://ms0", bytes32(0));

        uint256 workerBefore = worker.balance;
        vm.prank(verifierA);
        e.castMilestoneVerifierVote(0, true, "");
        vm.prank(verifierB);
        e.castMilestoneVerifierVote(0, true, "");

        assertEq(e.currentMilestone(), 1);
        (,,,,,,,,, TaskEscrow.MilestoneStatus ms0Status,) = e.milestones(0);
        assertEq(uint8(ms0Status), uint8(TaskEscrow.MilestoneStatus.Approved));

        uint256 fee = (1 ether * uint256(FEE_BPS)) / 10_000;
        assertEq(worker.balance, workerBefore + 1 ether - fee);
    }

    function testMilestoneDisputeResolutionRefundsVerifierStakeForNextCycle() public {
        uint256 stake = 0.2 ether;
        (TaskEscrow e,) = _createTwoMilestoneEscrow(2, 3, stake);

        vm.prank(buyer);
        e.fund{value: 2 ether}();

        vm.prank(verifierA);
        e.depositVerifierStake{value: stake}();
        uint256 verifierAfterDeposit = verifierA.balance;

        vm.prank(worker);
        e.submitMilestone(0, keccak256("ms0"), "ipfs://ms0", bytes32(0));
        vm.prank(buyer);
        e.disputeMilestone(0, "ipfs://ms0-dispute");
        vm.prank(arbitrator);
        e.resolveMilestoneDispute(0, 5000, "ipfs://ms0-resolution");

        vm.prank(verifierA);
        e.withdrawStake();

        assertEq(e.currentMilestone(), 1);
        assertEq(verifierA.balance, verifierAfterDeposit + stake);
        assertFalse(e.quorumStaked(verifierA));
        assertEq(uint256(e.quorumStakeCount()), 0);

        vm.prank(worker);
        e.submitMilestone(1, keccak256("ms1"), "ipfs://ms1", bytes32(0));

        vm.expectRevert(TaskEscrow.QuorumStakeRequired.selector);
        vm.prank(verifierA);
        e.castMilestoneVerifierVote(1, true, "");
    }

    function testExpireNoQuorumRefundsVerifierStakeAndMovesToDisputed() public {
        uint256 stake = 0.2 ether;
        (TaskEscrow e,) = _createSingle(2, 3, stake, 0, address(0), bytes32(0));
        _fundAndSubmit(e, bytes32(0));

        vm.prank(verifierA);
        e.depositVerifierStake{value: stake}();
        uint256 verifierAfterDeposit = verifierA.balance;

        vm.prank(verifierA);
        e.castVerifierVote(true, "");
        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Submitted));

        uint256 disputeEnd = uint256(e.submittedAt()) + uint256(REVIEW) + uint256(DISPUTE);
        vm.warp(disputeEnd + 1);

        vm.prank(buyer);
        e.expireNoQuorum("ipfs://expired-no-quorum");

        vm.prank(verifierA);
        e.withdrawStake();

        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Disputed));
        assertEq(uint256(e.disputedAt()), disputeEnd + 1);
        assertEq(verifierA.balance, verifierAfterDeposit + stake);
        assertFalse(e.quorumStaked(verifierA));
        assertEq(uint256(e.quorumStakeCount()), 0);
    }

    function testExpireMilestoneNoQuorumRefundsVerifierStakeAndMovesToDisputed() public {
        uint256 stake = 0.2 ether;
        (TaskEscrow e,) = _createTwoMilestoneEscrow(2, 3, stake);
        vm.prank(buyer);
        e.fund{value: 2 ether}();
        vm.prank(worker);
        e.submitMilestone(0, keccak256("ms0"), "ipfs://ms0", bytes32(0));

        vm.prank(verifierA);
        e.depositVerifierStake{value: stake}();
        uint256 verifierAfterDeposit = verifierA.balance;

        vm.prank(verifierA);
        e.castMilestoneVerifierVote(0, true, "");

        (,,,,, uint64 submittedAt,,,,,) = e.milestones(0);
        uint256 disputeEnd = uint256(submittedAt) + uint256(REVIEW) + uint256(DISPUTE);
        vm.warp(disputeEnd + 1);

        vm.prank(buyer);
        e.expireMilestoneNoQuorum(0, "ipfs://ms-expired-no-quorum");

        vm.prank(verifierA);
        e.withdrawStake();

        (,,,,,,, uint64 msDisputedAt, string memory msReason, TaskEscrow.MilestoneStatus msStatus,) = e.milestones(0);
        assertEq(uint8(msStatus), uint8(TaskEscrow.MilestoneStatus.Disputed));
        assertEq(msReason, "ipfs://ms-expired-no-quorum");
        assertEq(uint256(msDisputedAt), disputeEnd + 1);
        assertEq(verifierA.balance, verifierAfterDeposit + stake);
        assertFalse(e.quorumStaked(verifierA));
        assertEq(uint256(e.quorumStakeCount()), 0);
        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Funded));
    }

    function testFuzzVerifierStakeConservationSinglePanel(uint96 rawStake, bool approve) public {
        uint256 stake = uint256(bound(rawStake, 1, 10 ether));
        (TaskEscrow e,) = _createSingle(1, 1, stake, 0, address(0), bytes32(0));
        _fundAndSubmit(e, bytes32(0));

        vm.prank(verifierA);
        e.depositVerifierStake{value: stake}();

        uint256 buyerBefore = buyer.balance;
        uint256 verifierBefore = verifierA.balance;

        vm.prank(verifierA);
        e.castVerifierVote(approve, "ipfs://reason");

        // In a 1-of-1 panel, verifierA is always in the majority; pull stake back.
        vm.prank(verifierA);
        e.withdrawStake();

        assertEq(verifierA.balance, verifierBefore + stake);
        assertEq(buyer.balance, buyerBefore);
    }

    function _createSingle(
        uint8 threshold,
        uint8 count,
        uint256 verifierStakePerVerifier,
        uint8 serviceTier,
        address zkVerifierAddr,
        bytes32 circuitId
    ) internal returns (TaskEscrow escrow, uint256 escrowId) {
        address escrowAddr;
        (escrowId, escrowAddr) = factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifierPanel: _panel(),
                quorumThreshold: threshold,
                quorumVerifierCount: count,
                verifierStakePerVerifier: verifierStakePerVerifier,
                arbitrator: arbitrator,
                amount: 1 ether,
                workerStake: 0,
                submissionDeadline: uint64(block.timestamp + 7 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec-quorum-single"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                serviceTier: serviceTier,
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                zkVerifier: zkVerifierAddr,
                circuitId: circuitId,
                parentEscrow: address(0),
                milestones: new TaskEscrowFactory.CreateMilestoneParams[](0)
            })
        );
        escrow = TaskEscrow(escrowAddr);
    }

    function _createTwoMilestoneEscrow(uint8 threshold, uint8 count, uint256 verifierStakePerVerifier)
        internal
        returns (TaskEscrow escrow, uint256 escrowId)
    {
        TaskEscrowFactory.CreateMilestoneParams[] memory milestones = new TaskEscrowFactory.CreateMilestoneParams[](2);
        milestones[0] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 1 ether, submissionDeadline: uint64(block.timestamp + 7 days)
        });
        milestones[1] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 1 ether, submissionDeadline: uint64(block.timestamp + 14 days)
        });

        address escrowAddr;
        (escrowId, escrowAddr) = factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifierPanel: _panel(),
                quorumThreshold: threshold,
                quorumVerifierCount: count,
                verifierStakePerVerifier: verifierStakePerVerifier,
                arbitrator: arbitrator,
                amount: 2 ether,
                workerStake: 0,
                submissionDeadline: uint64(block.timestamp + 14 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec-quorum-milestone"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                serviceTier: 0,
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                zkVerifier: address(0),
                circuitId: bytes32(0),
                parentEscrow: address(0),
                milestones: milestones
            })
        );
        escrow = TaskEscrow(escrowAddr);
    }

    function _fundAndSubmit(TaskEscrow e, bytes32 proofHash) internal {
        uint256 amt = e.amount();
        vm.prank(buyer);
        e.fund{value: amt}();

        vm.prank(worker);
        e.submit(keccak256("submission"), "ipfs://submission", proofHash);
    }

    function _panel() internal view returns (address[7] memory panel) {
        panel[0] = verifierA;
        panel[1] = verifierB;
        panel[2] = verifierC;
    }
}
