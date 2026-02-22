package chain

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func (c *Client) FreezeAddress(ctx context.Context, factory common.Address, target common.Address) (*types.Transaction, error) {
	data, err := FactoryABI.Pack("freezeAddress", target)
	if err != nil {
		return nil, fmt.Errorf("pack freezeAddress: %w", err)
	}
	return c.SendTx(ctx, factory, data, nil)
}

func (c *Client) UnfreezeAddress(ctx context.Context, factory common.Address, target common.Address) (*types.Transaction, error) {
	data, err := FactoryABI.Pack("unfreezeAddress", target)
	if err != nil {
		return nil, fmt.Errorf("pack unfreezeAddress: %w", err)
	}
	return c.SendTx(ctx, factory, data, nil)
}

func (c *Client) FreezeEscrow(ctx context.Context, factory common.Address, escrowID *big.Int) (*types.Transaction, error) {
	data, err := FactoryABI.Pack("freezeEscrow", escrowID)
	if err != nil {
		return nil, fmt.Errorf("pack freezeEscrow: %w", err)
	}
	return c.SendTx(ctx, factory, data, nil)
}

func (c *Client) UnfreezeEscrow(ctx context.Context, factory common.Address, escrowID *big.Int) (*types.Transaction, error) {
	data, err := FactoryABI.Pack("unfreezeEscrow", escrowID)
	if err != nil {
		return nil, fmt.Errorf("pack unfreezeEscrow: %w", err)
	}
	return c.SendTx(ctx, factory, data, nil)
}

func (c *Client) EmergencyResolve(ctx context.Context, factory common.Address, escrowID *big.Int, workerAwardBps uint16) (*types.Transaction, error) {
	data, err := FactoryABI.Pack("emergencyResolve", escrowID, workerAwardBps)
	if err != nil {
		return nil, fmt.Errorf("pack emergencyResolve: %w", err)
	}
	return c.SendTx(ctx, factory, data, nil)
}

func (c *Client) IsFrozenAddress(ctx context.Context, factory common.Address, target common.Address) (bool, error) {
	data, err := FactoryABI.Pack("frozenAddresses", target)
	if err != nil {
		return false, fmt.Errorf("pack frozenAddresses: %w", err)
	}
	result, err := c.CallContract(ctx, factory, data)
	if err != nil {
		return false, fmt.Errorf("call frozenAddresses: %w", err)
	}
	values, err := FactoryABI.Unpack("frozenAddresses", result)
	if err != nil {
		return false, fmt.Errorf("unpack frozenAddresses: %w", err)
	}
	frozen, ok := values[0].(bool)
	if !ok {
		return false, fmt.Errorf("unexpected frozenAddresses type: %T", values[0])
	}
	return frozen, nil
}
