// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {TaskEscrow} from "./TaskEscrow.sol";

/// @notice Deploy-time bytecode container.
/// The constructor returns `code` as this instance's runtime bytecode.
contract BytecodeStore {
    constructor(bytes memory code) payable {
        assembly ("memory-safe") {
            return(add(code, 0x20), mload(code))
        }
    }
}

/// @notice Separate deployer to keep the factory under the EIP-170 24KB limit.
/// The factory delegates TaskEscrow creation to this contract, avoiding
/// embedding the full TaskEscrow bytecode in the factory's own runtime code.
contract EscrowDeployer {
    address public immutable taskEscrowInitcodeStore;

    constructor() {
        taskEscrowInitcodeStore = address(new BytecodeStore(type(TaskEscrow).creationCode));
    }

    function deploy(TaskEscrow.Params memory p) external returns (address) {
        bytes memory ctorArgs = abi.encode(p);
        bytes memory initcode = _readInitcode();
        bytes memory creation = bytes.concat(initcode, ctorArgs);
        address escrow;
        assembly ("memory-safe") {
            escrow := create(0, add(creation, 0x20), mload(creation))
            if iszero(escrow) {
                returndatacopy(0, 0, returndatasize())
                revert(0, returndatasize())
            }
        }
        return escrow;
    }

    function _readInitcode() internal view returns (bytes memory out) {
        address store = taskEscrowInitcodeStore;
        uint256 size;
        assembly ("memory-safe") {
            size := extcodesize(store)
        }
        out = new bytes(size);
        assembly ("memory-safe") {
            extcodecopy(store, add(out, 0x20), 0, size)
        }
    }
}
