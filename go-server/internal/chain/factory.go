//lint:file-ignore ST1003 ABI tuple field names must match Solidity members.
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
	VerifierPanel            [7]common.Address
	QuorumThreshold          uint8
	QuorumVerifierCount      uint8
	VerifierStakePerVerifier *big.Int
	Arbitrator               common.Address
	Amount                   *big.Int
	WorkerStake              *big.Int
	SubmissionDeadline       uint64
	ReviewPeriodSeconds      uint64
	DisputePeriodSeconds     uint64
	TaskSpecHash             [32]byte
	ArbitratorTimeoutSeconds uint64
	Token                    common.Address // address(0) for ETH, non-zero for ERC20
	ServiceTier              uint8          // 0 = low_assurance (optimistic), 1 = high_assurance (verifier required)
	Milestones               []MilestoneParam
	BackupWorker             common.Address // Optional backup agent; address(0) means none
	BackupDeadlineExtension  uint64         // Seconds added to deadline when backup activates
	ZKVerifier               common.Address // Optional zk verifier contract; address(0) disables on-chain proof checks
	CircuitID                [32]byte       // bytes32 circuit identifier used by zkVerifier
	ParentEscrow             common.Address // Optional parent escrow address for sub-delegation surcharge logic
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
	VerifierPanel            [7]common.Address
	QuorumThreshold          uint8
	QuorumVerifierCount      uint8
	VerifierStakePerVerifier *big.Int
	Arbitrator               common.Address
	Amount                   *big.Int
	WorkerStake              *big.Int
	SubmissionDeadline       uint64
	ReviewPeriodSeconds      uint64
	DisputePeriodSeconds     uint64
	TaskSpecHash             [32]byte
	ArbitratorTimeoutSeconds uint64
	Token                    common.Address
	ServiceTier              uint8
	BackupWorker             common.Address
	BackupDeadlineExtension  uint64
	ZkVerifier               common.Address
	//nolint:staticcheck // ABI tuple field must match Solidity's `circuitId`.
	//revive:disable-next-line:var-naming // ABI tuple field must match Solidity's `circuitId`.
	CircuitId    [32]byte
	ParentEscrow common.Address
	Milestones   []milestoneTuple
}

func (c *Client) CreateEscrow(ctx context.Context, factory common.Address, p CreateEscrowParams) (*types.Transaction, error) {
	workerStake := p.WorkerStake
	if workerStake == nil {
		workerStake = big.NewInt(0)
	}
	verifierStakePerVerifier := p.VerifierStakePerVerifier
	if verifierStakePerVerifier == nil {
		verifierStakePerVerifier = big.NewInt(0)
	}
	milestones := make([]milestoneTuple, len(p.Milestones))
	for i, m := range p.Milestones {
		milestones[i] = milestoneTuple(m)
	}
	tuple := createParamsTuple{
		Buyer:                    p.Buyer,
		Worker:                   p.Worker,
		VerifierPanel:            p.VerifierPanel,
		QuorumThreshold:          p.QuorumThreshold,
		QuorumVerifierCount:      p.QuorumVerifierCount,
		VerifierStakePerVerifier: verifierStakePerVerifier,
		Arbitrator:               p.Arbitrator,
		Amount:                   p.Amount,
		WorkerStake:              workerStake,
		SubmissionDeadline:       p.SubmissionDeadline,
		ReviewPeriodSeconds:      p.ReviewPeriodSeconds,
		DisputePeriodSeconds:     p.DisputePeriodSeconds,
		TaskSpecHash:             p.TaskSpecHash,
		ArbitratorTimeoutSeconds: p.ArbitratorTimeoutSeconds,
		Token:                    p.Token,
		ServiceTier:              p.ServiceTier,
		BackupWorker:             p.BackupWorker,
		BackupDeadlineExtension:  p.BackupDeadlineExtension,
		ZkVerifier:               p.ZKVerifier,
		CircuitId:                p.CircuitID,
		ParentEscrow:             p.ParentEscrow,
		Milestones:               milestones,
	}
	data, err := FactoryABI.Pack("createEscrow", tuple)
	if err != nil {
		return nil, fmt.Errorf("pack createEscrow: %w", err)
	}
	return c.SendTx(ctx, factory, data, nil)
}
