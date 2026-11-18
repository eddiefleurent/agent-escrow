// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {BaseEscrowTest} from "./BaseEscrowTest.sol";
import {TaskEscrowFactory} from "../src/TaskEscrowFactory.sol";
import {TaskEscrow} from "../src/TaskEscrow.sol";
import {MockZKVerifier} from "./mocks/MockZKVerifier.sol";

contract TaskEscrowZKVerificationTest is BaseEscrowTest {
    MockZKVerifier internal zk;

    function setUp() public override {
        super.setUp();
        zk = new MockZKVerifier();
        vm.deal(buyer, 10 ether);
    }

    function testVerifyAndApproveHappyPath() public {
        TaskEscrow e = _createEscrow(address(zk), keccak256("circuit-a"), _emptyMilestones());
        _fund(e);

        bytes memory proof = abi.encodePacked("proof-ok");
        bytes32 pHash = keccak256(proof);
        vm.prank(worker);
        e.submit(keccak256("submission"), "ipfs://submission", pHash);

        vm.prank(verifier);
        e.verifyAndApprove(proof);

        assertEq(uint8(e.status()), uint8(TaskEscrow.Status.Settled));
        assertEq(e.proofHash(), pHash);
    }

    function testVerifyAndApproveRevertsWhenVerifierNotConfigured() public {
        TaskEscrow e = _createEscrow(address(0), bytes32(0), _emptyMilestones());
        _fund(e);

        bytes memory proof = abi.encodePacked("proof-missing-verifier");
        vm.prank(worker);
        e.submit(keccak256("submission"), "ipfs://submission", keccak256(proof));

        vm.prank(verifier);
        vm.expectRevert(TaskEscrow.NoVerifierConfigured.selector);
        e.verifyAndApprove(proof);
    }

    function testVerifyAndApproveRevertsOnProofHashMismatch() public {
        TaskEscrow e = _createEscrow(address(zk), keccak256("circuit-b"), _emptyMilestones());
        _fund(e);

        vm.prank(worker);
        e.submit(keccak256("submission"), "ipfs://submission", keccak256("different-proof"));

        vm.prank(verifier);
        vm.expectRevert(TaskEscrow.ProofHashMismatch.selector);
        e.verifyAndApprove(abi.encodePacked("actual-proof"));
    }

    function testVerifyAndApproveMilestoneHappyPath() public {
        TaskEscrow e = _createEscrow(address(zk), keccak256("circuit-ms"), _twoMilestones());
        _fund(e);

        bytes memory proof = abi.encodePacked("milestone-proof");
        bytes32 pHash = keccak256(proof);
        vm.prank(worker);
        e.submitMilestone(0, keccak256("ms0"), "ipfs://ms0", pHash);

        vm.prank(verifier);
        e.verifyAndApproveMilestone(0, proof);

        (,,,,,,,,, TaskEscrow.MilestoneStatus ms0Status,) = e.milestones(0);
        assertEq(uint8(ms0Status), uint8(TaskEscrow.MilestoneStatus.Approved));
        assertEq(e.currentMilestone(), 1);
    }

    function testVerifyAndApproveMilestoneRevertsWhenVerifierNotConfigured() public {
        TaskEscrow e = _createEscrow(address(0), bytes32(0), _twoMilestones());
        _fund(e);

        bytes memory proof = abi.encodePacked("milestone-proof-no-verifier");
        bytes32 pHash = keccak256(proof);
        vm.prank(worker);
        e.submitMilestone(0, keccak256("ms0"), "ipfs://ms0", pHash);

        vm.prank(verifier);
        vm.expectRevert(TaskEscrow.NoVerifierConfigured.selector);
        e.verifyAndApproveMilestone(0, proof);
    }

    function testVerifyAndApproveMilestoneRevertsOnProofHashMismatch() public {
        TaskEscrow e = _createEscrow(address(zk), keccak256("circuit-ms-mismatch"), _twoMilestones());
        _fund(e);

        bytes memory submittedProof = abi.encodePacked("milestone-proof-submitted");
        vm.prank(worker);
        e.submitMilestone(0, keccak256("ms0"), "ipfs://ms0", keccak256(submittedProof));

        vm.prank(verifier);
        vm.expectRevert(TaskEscrow.ProofHashMismatch.selector);
        e.verifyAndApproveMilestone(0, abi.encodePacked("milestone-proof-different"));
    }

    function testVerifyAndApproveMilestoneRevertsWhenVerifierRejectsProof() public {
        TaskEscrow e = _createEscrow(address(zk), keccak256("circuit-ms-reject"), _twoMilestones());
        _fund(e);

        bytes memory proof = abi.encodePacked("milestone-proof-rejected");
        vm.prank(worker);
        e.submitMilestone(0, keccak256("ms0"), "ipfs://ms0", keccak256(proof));

        zk.setShouldVerify(false);
        vm.prank(verifier);
        vm.expectRevert(TaskEscrow.ProofVerificationFailed.selector);
        e.verifyAndApproveMilestone(0, proof);
    }

    function testVerifyAndApproveRevertsWhenVerifierRejectsProof() public {
        TaskEscrow e = _createEscrow(address(zk), keccak256("circuit-c"), _emptyMilestones());
        _fund(e);

        bytes memory proof = abi.encodePacked("proof-rejected");
        vm.prank(worker);
        e.submit(keccak256("submission"), "ipfs://submission", keccak256(proof));

        zk.setShouldVerify(false);
        vm.prank(verifier);
        vm.expectRevert(TaskEscrow.ProofVerificationFailed.selector);
        e.verifyAndApprove(proof);
    }

    function testCreateEscrowRevertsOnMismatchedVerifierConfiguration() public {
        vm.expectRevert(TaskEscrow.InvalidVerifierConfiguration.selector);
        _createEscrow(address(zk), bytes32(0), _emptyMilestones());

        vm.expectRevert(TaskEscrow.InvalidVerifierConfiguration.selector);
        _createEscrow(address(0), keccak256("circuit-without-verifier"), _emptyMilestones());
    }

    function testCreateEscrowRevertsWhenVerifierIsNotAContract() public {
        vm.expectRevert(TaskEscrow.InvalidAddress.selector);
        _createEscrow(makeAddr("not-a-contract"), keccak256("circuit-eoa"), _emptyMilestones());
    }

    function _emptyMilestones() internal pure returns (TaskEscrowFactory.CreateMilestoneParams[] memory ms) {
        ms = new TaskEscrowFactory.CreateMilestoneParams[](0);
    }

    function _twoMilestones() internal view returns (TaskEscrowFactory.CreateMilestoneParams[] memory ms) {
        ms = new TaskEscrowFactory.CreateMilestoneParams[](2);
        ms[0] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 0.5 ether, submissionDeadline: uint64(block.timestamp + 3 days)
        });
        ms[1] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 0.5 ether, submissionDeadline: uint64(block.timestamp + 7 days)
        });
    }

    function _createEscrow(
        address zkVerifierAddr,
        bytes32 circuitId,
        TaskEscrowFactory.CreateMilestoneParams[] memory milestones
    ) internal returns (TaskEscrow) {
        TaskEscrowFactory.CreateParams memory params = _defaultParams();
        params.serviceTier = 1;
        params.zkVerifier = zkVerifierAddr;
        params.circuitId = circuitId;
        params.milestones = milestones;
        return _create(params);
    }
}
