// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

/// @title IEIP3009 -- Transfer With Authorization (EIP-3009)
/// @notice Subset of EIP-3009 used by USDC on Base for gasless transfers.
/// The escrow contract calls receiveWithAuthorization so that only the
/// designated `to` address (the escrow itself) can execute the transfer.
interface IEIP3009 {
    function receiveWithAuthorization(
        address from,
        address to,
        uint256 value,
        uint256 validAfter,
        uint256 validBefore,
        bytes32 nonce,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) external;
}
