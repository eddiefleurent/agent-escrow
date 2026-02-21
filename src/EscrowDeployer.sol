// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {TaskEscrow} from "./TaskEscrow.sol";

/// @notice Separate deployer to keep the factory under the EIP-170 24KB limit.
/// The factory delegates TaskEscrow creation to this contract, avoiding
/// embedding the full TaskEscrow bytecode in the factory's own runtime code.
contract EscrowDeployer {
    function deploy(TaskEscrow.Params memory p) external returns (address) {
        TaskEscrow instance = new TaskEscrow(p);
        return address(instance);
    }
}
