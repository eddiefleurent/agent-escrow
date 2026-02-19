package chain

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type CreateEscrowParams struct {
	Buyer                    common.Address
	Worker                   common.Address
	Verifier                 common.Address
	Arbitrator               common.Address
	Amount                   *big.Int
	SubmissionDeadline       uint64
	ReviewPeriodSeconds      uint64
	DisputePeriodSeconds     uint64
	TaskSpecHash             [32]byte
	ArbitratorTimeoutSeconds uint64
}

func (c *Client) CreateEscrow(ctx context.Context, factory common.Address, p CreateEscrowParams) (*types.Transaction, error) {
	data, err := FactoryABI.Pack("createEscrow",
		p.Buyer,
		p.Worker,
		p.Verifier,
		p.Arbitrator,
		p.Amount,
		p.SubmissionDeadline,
		p.ReviewPeriodSeconds,
		p.DisputePeriodSeconds,
		p.TaskSpecHash,
		p.ArbitratorTimeoutSeconds,
	)
	if err != nil {
		return nil, fmt.Errorf("pack createEscrow: %w", err)
	}
	return c.SendTx(ctx, factory, data, nil)
}
