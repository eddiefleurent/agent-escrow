package chain

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// MilestoneParam describes a single milestone for multi-milestone escrow creation.
type MilestoneParam struct {
	Amount             *big.Int
	SubmissionDeadline uint64
}

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
	Milestones               []MilestoneParam
	BackupWorker             common.Address // Optional backup agent; address(0) means none
	BackupDeadlineExtension  uint64         // Seconds added to deadline when backup activates
}

// milestoneTuple matches the Solidity CreateMilestoneParams struct layout for ABI encoding.
type milestoneTuple struct {
	Amount             *big.Int
	SubmissionDeadline uint64
}

// createParamsTuple is the struct layout that matches the Solidity CreateParams struct
// used in the factory's createEscrow(CreateParams calldata) function.
// Field order must exactly match the Solidity struct for correct ABI encoding.
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
	BackupWorker             common.Address
	BackupDeadlineExtension  uint64
	Milestones               []milestoneTuple
}

func (c *Client) CreateEscrow(ctx context.Context, factory common.Address, p CreateEscrowParams) (*types.Transaction, error) {
	workerStake := p.WorkerStake
	if workerStake == nil {
		workerStake = big.NewInt(0)
	}
	milestones := make([]milestoneTuple, len(p.Milestones))
	for i, m := range p.Milestones {
		milestones[i] = milestoneTuple(m)
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
		BackupWorker:             p.BackupWorker,
		BackupDeadlineExtension:  p.BackupDeadlineExtension,
		Milestones:               milestones,
	}
	data, err := FactoryABI.Pack("createEscrow", tuple)
	if err != nil {
		return nil, fmt.Errorf("pack createEscrow: %w", err)
	}
	return c.SendTx(ctx, factory, data, nil)
}
