// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

/// @notice Library for milestone settlement math. Extracted to reduce TaskEscrow bytecode
/// and keep the factory under the EIP-170 24KB runtime size limit.
library MilestoneLib {
    struct SettleApprovedResult {
        uint256 workerNet;
        uint256 fee;
    }

    struct SettleResolvedResult {
        uint256 workerNet;
        uint256 buyerRefund;
        uint256 fee;
    }

    struct StakeResult {
        uint256 stakeReturn;
        uint256 stakeForfeited;
    }

    function settleMilestoneApproved(uint256 msAmount, uint16 protocolFeeBps)
        internal
        pure
        returns (SettleApprovedResult memory r)
    {
        uint256 grossWorker = msAmount;
        r.fee = (grossWorker * protocolFeeBps) / 10_000;
        r.workerNet = grossWorker - r.fee;
    }

    function settleMilestoneResolved(uint256 msAmount, uint16 workerAwardBps, uint16 protocolFeeBps)
        internal
        pure
        returns (SettleResolvedResult memory r)
    {
        uint256 workerGross = (msAmount * workerAwardBps) / 10_000;
        r.buyerRefund = msAmount - workerGross;
        r.fee = (workerGross * protocolFeeBps) / 10_000;
        r.workerNet = workerGross - r.fee;
    }

    function settleApproved(uint256 totalAmount, uint16 protocolFeeBps, uint256 workerStakeAmt, bool staked)
        internal
        pure
        returns (uint256 workerNet, uint256 fee, uint256 stakeReturn)
    {
        uint256 grossWorker = totalAmount;
        fee = (grossWorker * protocolFeeBps) / 10_000;
        workerNet = grossWorker - fee;
        stakeReturn = staked ? workerStakeAmt : 0;
    }

    function settleResolved(
        uint256 totalAmount,
        uint16 workerAwardBps,
        uint16 protocolFeeBps,
        uint256 workerStakeAmt,
        bool staked
    )
        internal
        pure
        returns (uint256 workerNet, uint256 buyerRefund, uint256 fee, uint256 stakeReturn, uint256 stakeForfeited)
    {
        uint256 workerGross = (totalAmount * workerAwardBps) / 10_000;
        buyerRefund = totalAmount - workerGross;
        fee = (workerGross * protocolFeeBps) / 10_000;
        workerNet = workerGross - fee;
        stakeReturn = staked ? (workerStakeAmt * workerAwardBps) / 10_000 : 0;
        stakeForfeited = staked ? workerStakeAmt - stakeReturn : 0;
    }
}
