// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

/// @notice Library to offload validation logic from the escrow contract,
/// reducing runtime bytecode to stay under EIP-170 24KB.
library FactoryLib {
    /// @dev Returns true if any pair of core roles collide, if any panel member
    /// collides with core roles, or if panel members are not distinct.
    function rolesCollide(
        address buyer,
        address _worker,
        address _arbitrator,
        address _backupWorker,
        address[7] memory _panel,
        uint8 _panelCount
    ) public pure returns (bool) {
        if (buyer == _worker || buyer == _arbitrator || _worker == _arbitrator) return true;
        if (_backupWorker != address(0)) {
            if (_backupWorker == buyer || _backupWorker == _worker || _backupWorker == _arbitrator) return true;
        }

        for (uint8 i = 0; i < _panelCount; i++) {
            address panelVerifier = _panel[i];
            if (
                panelVerifier == address(0) || panelVerifier == buyer || panelVerifier == _worker
                    || panelVerifier == _arbitrator
            ) return true;
            if (_backupWorker != address(0) && panelVerifier == _backupWorker) return true;

            for (uint8 j = i + 1; j < _panelCount; j++) {
                if (panelVerifier == _panel[j]) return true;
            }
        }

        return false;
    }
}
