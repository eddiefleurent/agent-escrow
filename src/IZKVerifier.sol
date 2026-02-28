// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

interface IZKVerifier {
    function verifyProof(bytes32 circuitId, bytes calldata proof) external view returns (bool);
}
