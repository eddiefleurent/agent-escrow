// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

/// @notice Library to offload validation logic from the escrow contract,
/// reducing runtime bytecode to stay under EIP-170 24KB.
library FactoryLib {
    /// @dev Returns true if any pair of core roles collide, or if backupWorker
    /// (when non-zero) collides with any core role.
    function rolesCollide(address buyer, address _worker, address _verifier, address _arbitrator, address _backupWorker)
        internal
        pure
        returns (bool)
    {
        if (
            buyer == _worker || buyer == _verifier || buyer == _arbitrator || _worker == _verifier
                || _worker == _arbitrator || _verifier == _arbitrator
        ) {
            return true;
        }
        if (_backupWorker != address(0)) {
            if (
                _backupWorker == buyer || _backupWorker == _worker || _backupWorker == _verifier
                    || _backupWorker == _arbitrator
            ) {
                return true;
            }
        }
        return false;
    }
}
