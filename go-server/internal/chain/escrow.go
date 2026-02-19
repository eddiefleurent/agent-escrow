package chain

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func (c *Client) Fund(ctx context.Context, escrow common.Address, amount *big.Int) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("fund")
	if err != nil {
		return nil, fmt.Errorf("pack fund: %w", err)
	}
	return c.SendTx(ctx, escrow, data, amount)
}

func (c *Client) Submit(ctx context.Context, escrow common.Address, submissionHash [32]byte, submissionURI string) (*types.Transaction, error) {
	data, err := EscrowABI.Pack("submit", submissionHash, submissionURI)
	if err != nil {
		return nil, fmt.Errorf("pack submit: %w", err)
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
	return values[0].(uint8), nil
}
