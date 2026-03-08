// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Test} from "forge-std/Test.sol";
import {EscrowDeployer} from "../src/EscrowDeployer.sol";
import {TaskEscrow} from "../src/TaskEscrow.sol";

contract EscrowDeployerTest is Test {
    uint256 private constant MAX_RUNTIME_CODE_SIZE = 24_576;

    function testConstructorSupportsCurrentTaskEscrowInitcodeLength() public {
        uint256 initcodeLength = type(TaskEscrow).creationCode.length;

        assertLe(initcodeLength, MAX_RUNTIME_CODE_SIZE * 2, "TaskEscrow initcode exceeds deployer capacity");

        EscrowDeployer deployer = new EscrowDeployer();
        assertEq(deployer.taskEscrowInitcodeLength(), initcodeLength);
        assertTrue(deployer.taskEscrowInitcodeStorePart1() != address(0));

        if (initcodeLength > MAX_RUNTIME_CODE_SIZE) {
            assertTrue(deployer.taskEscrowInitcodeStorePart2() != address(0));
        } else {
            assertEq(deployer.taskEscrowInitcodeStorePart2(), address(0));
        }
    }

    function testDeployCreatesValidEscrow() public {
        EscrowDeployer deployer = new EscrowDeployer();

        address[7] memory panel;
        panel[0] = address(0x4);

        TaskEscrow.CreateMilestoneParams[] memory milestones = new TaskEscrow.CreateMilestoneParams[](0);

        TaskEscrow.Params memory p = TaskEscrow.Params({
            factory: address(this),
            buyer: address(0x1),
            worker: address(0x2),
            verifierPanel: panel,
            quorumThreshold: 1,
            quorumVerifierCount: 1,
            verifierStakePerVerifier: 0,
            arbitrator: address(0x3),
            amount: 1 ether,
            workerStake: 0,
            submissionDeadline: uint64(block.timestamp + 1 days),
            reviewPeriodSeconds: 3600,
            disputePeriodSeconds: 3600,
            taskSpecHash: keccak256("test"),
            protocolFeeBpsSnapshot: 100,
            treasurySnapshot: address(0x5),
            arbitratorTimeoutSeconds: 7200,
            token: address(0),
            serviceTier: 0,
            backupWorker: address(0),
            backupDeadlineExtension: 0,
            zkVerifier: address(0),
            circuitId: bytes32(0),
            milestones: milestones
        });

        address escrow = deployer.deploy(p);
        assertTrue(escrow != address(0), "deploy must return a non-zero address");

        TaskEscrow te = TaskEscrow(payable(escrow));
        assertEq(te.buyer(), address(0x1));
        assertEq(te.worker(), address(0x2));
        assertEq(te.arbitrator(), address(0x3));
        assertEq(te.amount(), 1 ether);
    }
}
