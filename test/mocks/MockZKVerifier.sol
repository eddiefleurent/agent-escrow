// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {IZKVerifier} from "../../src/IZKVerifier.sol";

contract MockZKVerifier is IZKVerifier {
    bool public shouldVerify = true;

    function setShouldVerify(bool value) external {
        shouldVerify = value;
    }

    function verifyProof(bytes32, bytes calldata) external view override returns (bool) {
        return shouldVerify;
    }
}
