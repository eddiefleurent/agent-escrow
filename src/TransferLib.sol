// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {IERC20} from "./interfaces/IERC20.sol";
import {IEIP3009} from "./interfaces/IEIP3009.sol";

/// @notice Library for token transfer operations. Uses public functions to force
/// DELEGATECALL linkage, reducing TaskEscrow bytecode and keeping the
/// EscrowDeployer under the EIP-170 24KB runtime size limit.
library TransferLib {
    error TransferFailed();
    error InsufficientReceived();

    function send(address token, address to, uint256 value) public {
        if (token == address(0)) {
            (bool ok,) = payable(to).call{value: value}("");
            if (!ok) revert TransferFailed();
        } else {
            _safeTransfer(IERC20(token), to, value);
        }
    }

    function receiveERC20(address token, uint256 value) public {
        uint256 bb = IERC20(token).balanceOf(address(this));
        _safeTransferFrom(IERC20(token), msg.sender, address(this), value);
        if (IERC20(token).balanceOf(address(this)) - bb != value) revert InsufficientReceived();
    }

    function receiveEIP3009(
        address token,
        address from,
        uint256 value,
        uint256 validAfter,
        uint256 validBefore,
        bytes32 nonce,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) public {
        uint256 bb = IERC20(token).balanceOf(address(this));
        IEIP3009(token).receiveWithAuthorization(from, address(this), value, validAfter, validBefore, nonce, v, r, s);
        if (IERC20(token).balanceOf(address(this)) - bb != value) revert InsufficientReceived();
    }

    function _safeTransfer(IERC20 _token, address to, uint256 value) private {
        (bool success, bytes memory data) =
            address(_token).call(abi.encodeWithSelector(_token.transfer.selector, to, value));
        if (!success || (data.length > 0 && !abi.decode(data, (bool)))) revert TransferFailed();
    }

    function _safeTransferFrom(IERC20 _token, address from, address to, uint256 value) private {
        (bool success, bytes memory data) =
            address(_token).call(abi.encodeWithSelector(_token.transferFrom.selector, from, to, value));
        if (!success || (data.length > 0 && !abi.decode(data, (bool)))) revert TransferFailed();
    }
}
