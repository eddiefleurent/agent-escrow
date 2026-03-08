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
}
