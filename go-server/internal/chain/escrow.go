package chain

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Fund sends the fund() transaction. For ETH escrows, amount is sent as msg.value.
// For ERC20 escrows, pass amount as nil/zero (the buyer must approve the token first via ApproveERC20).
func (c *Client) Fund(ctx context.Context, escrow common.Address, amount *big.Int) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("fund")
	if err != nil {
		return nil, fmt.Errorf("pack fund: %w", err)
	}
	return c.SendTx(ctx, escrow, data, amount)
}

// FundWithAuthorization calls fundWithAuthorization on the escrow contract
// using an EIP-3009 signed authorization for gasless ERC20 funding.
func (c *Client) FundWithAuthorization(ctx context.Context, escrow common.Address, from common.Address, validAfter, validBefore *big.Int, nonce [32]byte, v uint8, r, s [32]byte) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("fundWithAuthorization", from, validAfter, validBefore, nonce, v, r, s)
	if err != nil {
		return nil, fmt.Errorf("pack fundWithAuthorization: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

// DepositStake sends the depositStake() transaction. For ETH escrows, stakeAmount is sent as msg.value.
// For ERC20 escrows, pass stakeAmount as nil/zero (the worker must approve the token first via ApproveERC20).
func (c *Client) DepositStake(ctx context.Context, escrow common.Address, stakeAmount *big.Int) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("depositStake")
	if err != nil {
		return nil, fmt.Errorf("pack depositStake: %w", err)
	}
	return c.SendTx(ctx, escrow, data, stakeAmount)
}

// ApproveERC20 calls the ERC20 approve(spender, amount) method on the given token contract.
func (c *Client) ApproveERC20(ctx context.Context, token common.Address, spender common.Address, amount *big.Int) (*types.Transaction, error) {
	data, err := ERC20ABI.Pack("approve", spender, amount)
	if err != nil {
		return nil, fmt.Errorf("pack approve: %w", err)
	}
	return c.SendTx(ctx, token, data, nil)
}

func (c *Client) Submit(ctx context.Context, escrow common.Address, submissionHash [32]byte, submissionURI string, proofHash [32]byte) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("submit", submissionHash, submissionURI, proofHash)
	if err != nil {
		return nil, fmt.Errorf("pack submit: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) VerifyAndApprove(ctx context.Context, escrow common.Address, proof []byte) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("verifyAndApprove", proof)
	if err != nil {
		return nil, fmt.Errorf("pack verifyAndApprove: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) ApproveByBuyer(ctx context.Context, escrow common.Address) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("approveByBuyer")
	if err != nil {
		return nil, fmt.Errorf("pack approveByBuyer: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) ApproveByVerifier(ctx context.Context, escrow common.Address) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("approveByVerifier")
	if err != nil {
		return nil, fmt.Errorf("pack approveByVerifier: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) RejectByVerifier(ctx context.Context, escrow common.Address, reasonURI string) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("rejectByVerifier", reasonURI)
	if err != nil {
		return nil, fmt.Errorf("pack rejectByVerifier: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) Dispute(ctx context.Context, escrow common.Address, reasonURI string) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("dispute", reasonURI)
	if err != nil {
		return nil, fmt.Errorf("pack dispute: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) EscalateSilence(ctx context.Context, escrow common.Address, reasonURI string) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("escalateSilence", reasonURI)
	if err != nil {
		return nil, fmt.Errorf("pack escalateSilence: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) ResolveDispute(ctx context.Context, escrow common.Address, workerAwardBps uint16, resolutionURI string) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("resolveDispute", workerAwardBps, resolutionURI)
	if err != nil {
		return nil, fmt.Errorf("pack resolveDispute: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) ClaimTimeoutRefund(ctx context.Context, escrow common.Address) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("claimTimeoutRefund")
	if err != nil {
		return nil, fmt.Errorf("pack claimTimeoutRefund: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) ClaimArbitratorTimeout(ctx context.Context, escrow common.Address) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("claimArbitratorTimeout")
	if err != nil {
		return nil, fmt.Errorf("pack claimArbitratorTimeout: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) Status(ctx context.Context, escrow common.Address) (uint8, error) {
	data, err := EscrowABI.Pack("status")
	if err != nil {
		return 0, fmt.Errorf("pack status: %w", err)
	}
	result, err := c.CallContract(ctx, escrow, data)
	if err != nil {
		return 0, fmt.Errorf("call status: %w", err)
	}
	values, err := EscrowABI.Unpack("status", result)
	if err != nil {
		return 0, fmt.Errorf("unpack status: %w", err)
	}
	status, ok := values[0].(uint8)
	if !ok {
		return 0, fmt.Errorf("unexpected status type: %T", values[0])
	}
	return status, nil
}

// Milestone-specific operations (V2 multi-milestone)

func (c *Client) SubmitMilestone(ctx context.Context, escrow common.Address, milestoneIndex uint8, submissionHash [32]byte, submissionURI string, proofHash [32]byte) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("submitMilestone", milestoneIndex, submissionHash, submissionURI, proofHash)
	if err != nil {
		return nil, fmt.Errorf("pack submitMilestone: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) VerifyAndApproveMilestone(ctx context.Context, escrow common.Address, milestoneIndex uint8, proof []byte) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("verifyAndApproveMilestone", milestoneIndex, proof)
	if err != nil {
		return nil, fmt.Errorf("pack verifyAndApproveMilestone: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) ApproveMilestoneByBuyer(ctx context.Context, escrow common.Address, milestoneIndex uint8) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("approveMilestoneByBuyer", milestoneIndex)
	if err != nil {
		return nil, fmt.Errorf("pack approveMilestoneByBuyer: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) ApproveMilestoneByVerifier(ctx context.Context, escrow common.Address, milestoneIndex uint8) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("approveMilestoneByVerifier", milestoneIndex)
	if err != nil {
		return nil, fmt.Errorf("pack approveMilestoneByVerifier: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) RejectMilestoneByVerifier(ctx context.Context, escrow common.Address, milestoneIndex uint8, reasonURI string) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("rejectMilestoneByVerifier", milestoneIndex, reasonURI)
	if err != nil {
		return nil, fmt.Errorf("pack rejectMilestoneByVerifier: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) DisputeMilestone(ctx context.Context, escrow common.Address, milestoneIndex uint8, reasonURI string) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("disputeMilestone", milestoneIndex, reasonURI)
	if err != nil {
		return nil, fmt.Errorf("pack disputeMilestone: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) EscalateMilestoneSilence(ctx context.Context, escrow common.Address, milestoneIndex uint8, reasonURI string) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("escalateMilestoneSilence", milestoneIndex, reasonURI)
	if err != nil {
		return nil, fmt.Errorf("pack escalateMilestoneSilence: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) ResolveMilestoneDispute(ctx context.Context, escrow common.Address, milestoneIndex uint8, workerAwardBps uint16, resolutionURI string) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("resolveMilestoneDispute", milestoneIndex, workerAwardBps, resolutionURI)
	if err != nil {
		return nil, fmt.Errorf("pack resolveMilestoneDispute: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) ClaimMilestoneTimeoutRefund(ctx context.Context, escrow common.Address, milestoneIndex uint8) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("claimMilestoneTimeoutRefund", milestoneIndex)
	if err != nil {
		return nil, fmt.Errorf("pack claimMilestoneTimeoutRefund: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) ClaimMilestoneArbitratorTimeout(ctx context.Context, escrow common.Address, milestoneIndex uint8) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("claimMilestoneArbitratorTimeout", milestoneIndex)
	if err != nil {
		return nil, fmt.Errorf("pack claimMilestoneArbitratorTimeout: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) AbortRemainingMilestones(ctx context.Context, escrow common.Address) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("abortRemainingMilestones")
	if err != nil {
		return nil, fmt.Errorf("pack abortRemainingMilestones: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}

func (c *Client) ActivateBackup(ctx context.Context, escrow common.Address) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("activateBackup")
	if err != nil {
		return nil, fmt.Errorf("pack activateBackup: %w", err)
	}
	return c.SendTx(ctx, escrow, data, nil)
}
