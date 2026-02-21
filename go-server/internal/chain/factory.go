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
	WorkerStake              *big.Int
	SubmissionDeadline       uint64
	ReviewPeriodSeconds      uint64
	DisputePeriodSeconds     uint64
	TaskSpecHash             [32]byte
	ArbitratorTimeoutSeconds uint64
	Token                    common.Address // address(0) for ETH, non-zero for ERC20
}

// createParamsTuple is the struct layout that matches the Solidity CreateParams struct
// used in the factory's createEscrow(CreateParams calldata) function.
type createParamsTuple struct {
	Buyer                    common.Address
	Worker                   common.Address
	Verifier                 common.Address
	Arbitrator               common.Address
	Amount                   *big.Int
	WorkerStake              *big.Int
	SubmissionDeadline       uint64
	ReviewPeriodSeconds      uint64
	DisputePeriodSeconds     uint64
	TaskSpecHash             [32]byte
	ArbitratorTimeoutSeconds uint64
	Token                    common.Address
}

func (c *Client) CreateEscrow(ctx context.Context, factory common.Address, p CreateEscrowParams) (*types.Transaction, error) {
	workerStake := p.WorkerStake
	if workerStake == nil {
		workerStake = big.NewInt(0)
	}
	tuple := createParamsTuple{
		Buyer:                    p.Buyer,
		Worker:                   p.Worker,
		Verifier:                 p.Verifier,
		Arbitrator:               p.Arbitrator,
		Amount:                   p.Amount,
		WorkerStake:              workerStake,
		SubmissionDeadline:       p.SubmissionDeadline,
		ReviewPeriodSeconds:      p.ReviewPeriodSeconds,
		DisputePeriodSeconds:     p.DisputePeriodSeconds,
		TaskSpecHash:             p.TaskSpecHash,
		ArbitratorTimeoutSeconds: p.ArbitratorTimeoutSeconds,
		Token:                    p.Token,
	}
	data, err := FactoryABI.Pack("createEscrow", tuple)
	if err != nil {
		return nil, fmt.Errorf("pack createEscrow: %w", err)
	}
	return c.SendTx(ctx, factory, data, nil)
}
