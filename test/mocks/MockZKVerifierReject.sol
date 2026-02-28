// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {IZKVerifier} from "../../src/IZKVerifier.sol";

contract MockZKVerifierReject is IZKVerifier {
    function verifyProof(bytes32, bytes calldata) external pure override returns (bool) {
        return false;
    }
}
