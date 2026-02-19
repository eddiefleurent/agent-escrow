// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Script} from "forge-std/Script.sol";
import {TaskEscrowFactory} from "../src/TaskEscrowFactory.sol";

contract DeployFactory is Script {
    function run() external returns (TaskEscrowFactory factory) {
        uint256 deployerPrivateKey = vm.envUint("PRIVATE_KEY");
        address treasury = vm.envAddress("TREASURY");
        address owner = vm.envAddress("OWNER");
        uint16 protocolFeeBps = uint16(vm.envUint("PROTOCOL_FEE_BPS"));

        vm.startBroadcast(deployerPrivateKey);
        factory = new TaskEscrowFactory(protocolFeeBps, treasury, owner);
        vm.stopBroadcast();
    }
}

