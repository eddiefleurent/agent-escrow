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
    uint256 private constant MAX_RUNTIME_CODE_SIZE = 24_576;
    error InitcodeTooLarge(uint256 initcodeLength, uint256 maxSupportedLength);

    address public immutable taskEscrowInitcodeStorePart1;
    address public immutable taskEscrowInitcodeStorePart2;
    uint32 public immutable taskEscrowInitcodeLength;

    constructor() {
        bytes memory initcode = type(TaskEscrow).creationCode;
        uint256 len = initcode.length;
        if (len > (MAX_RUNTIME_CODE_SIZE * 2)) {
            revert InitcodeTooLarge(len, MAX_RUNTIME_CODE_SIZE * 2);
        }
        taskEscrowInitcodeLength = uint32(len);

        uint256 part1Len = len;
        if (part1Len > MAX_RUNTIME_CODE_SIZE) {
            part1Len = MAX_RUNTIME_CODE_SIZE;
        }

        taskEscrowInitcodeStorePart1 = address(new BytecodeStore(_slice(initcode, 0, part1Len)));

        if (part1Len < len) {
            taskEscrowInitcodeStorePart2 = address(new BytecodeStore(_slice(initcode, part1Len, len - part1Len)));
        }
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
        out = new bytes(taskEscrowInitcodeLength);
        uint256 copied;

        copied = _copyStoreCode(taskEscrowInitcodeStorePart1, out, copied);

        if (taskEscrowInitcodeStorePart2 != address(0)) {
            // Offset return value is not needed here because part 2 is the final store.
            _copyStoreCode(taskEscrowInitcodeStorePart2, out, copied);
        }
    }

    function _copyStoreCode(address store, bytes memory out, uint256 offset)
        internal
        view
        returns (uint256 nextOffset)
    {
        uint256 size;
        assembly ("memory-safe") {
            size := extcodesize(store)
        }
        nextOffset = offset + size;
        assembly ("memory-safe") {
            extcodecopy(store, add(add(out, 0x20), offset), 0, size)
        }
    }

    function _slice(bytes memory data, uint256 start, uint256 len) internal pure returns (bytes memory out) {
        out = new bytes(len);
        for (uint256 i = 0; i < len; i++) {
            out[i] = data[start + i];
        }
    }
}
