package chain

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// ChainClient abstracts on-chain operations for testability.
// Both the real Client and test mocks implement this interface.
type ChainClient interface {
	Address() common.Address
	ChainID() *big.Int
	BlockNumber(ctx context.Context) (uint64, error)
	FilterLogs(ctx context.Context, addresses []common.Address, topics [][]common.Hash, fromBlock, toBlock uint64) ([]types.Log, error)
	SendTx(ctx context.Context, to common.Address, data []byte, value *big.Int) (*types.Transaction, error)
	CallContract(ctx context.Context, to common.Address, data []byte) ([]byte, error)
	TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error)

	// High-level escrow operations (V1 single-milestone)
	CreateEscrow(ctx context.Context, factory common.Address, p CreateEscrowParams) (*types.Transaction, error)
	Fund(ctx context.Context, escrow common.Address, amount *big.Int) (*types.Transaction, error)
	CancelBeforeFunding(ctx context.Context, escrow common.Address) (*types.Transaction, error)
	FundWithAuthorization(ctx context.Context, escrow common.Address, from common.Address, validAfter, validBefore *big.Int, nonce [32]byte, v uint8, r, s [32]byte) (*types.Transaction, error)
	DepositStake(ctx context.Context, escrow common.Address, stakeAmount *big.Int) (*types.Transaction, error)
	ApproveERC20(ctx context.Context, token common.Address, spender common.Address, amount *big.Int) (*types.Transaction, error)
	Submit(ctx context.Context, escrow common.Address, submissionHash [32]byte, submissionURI string, proofHash [32]byte) (*types.Transaction, error)
	VerifyAndApprove(ctx context.Context, escrow common.Address, proof []byte) (*types.Transaction, error)
	ApproveByBuyer(ctx context.Context, escrow common.Address) (*types.Transaction, error)
	DepositVerifierStake(ctx context.Context, escrow common.Address, stakeAmount *big.Int) (*types.Transaction, error)
	WithdrawStake(ctx context.Context, escrow common.Address) (*types.Transaction, error)
	CastVerifierVote(ctx context.Context, escrow common.Address, approve bool, reasonURI string) (*types.Transaction, error)
	Dispute(ctx context.Context, escrow common.Address, reasonURI string) (*types.Transaction, error)
	EscalateSilence(ctx context.Context, escrow common.Address, reasonURI string) (*types.Transaction, error)
	ResolveDispute(ctx context.Context, escrow common.Address, workerAwardBps uint16, resolutionURI string) (*types.Transaction, error)
	ClaimTimeoutRefund(ctx context.Context, escrow common.Address) (*types.Transaction, error)
	ClaimArbitratorTimeout(ctx context.Context, escrow common.Address) (*types.Transaction, error)
	Status(ctx context.Context, escrow common.Address) (uint8, error)

	// Milestone-specific operations (V2 multi-milestone)
	SubmitMilestone(ctx context.Context, escrow common.Address, milestoneIndex uint8, submissionHash [32]byte, submissionURI string, proofHash [32]byte) (*types.Transaction, error)
	VerifyAndApproveMilestone(ctx context.Context, escrow common.Address, milestoneIndex uint8, proof []byte) (*types.Transaction, error)
	ApproveMilestoneByBuyer(ctx context.Context, escrow common.Address, milestoneIndex uint8) (*types.Transaction, error)
	CastMilestoneVerifierVote(ctx context.Context, escrow common.Address, milestoneIndex uint8, approve bool, reasonURI string) (*types.Transaction, error)
	DisputeMilestone(ctx context.Context, escrow common.Address, milestoneIndex uint8, reasonURI string) (*types.Transaction, error)
	EscalateMilestoneSilence(ctx context.Context, escrow common.Address, milestoneIndex uint8, reasonURI string) (*types.Transaction, error)
	ResolveMilestoneDispute(ctx context.Context, escrow common.Address, milestoneIndex uint8, workerAwardBps uint16, resolutionURI string) (*types.Transaction, error)
	ClaimMilestoneTimeoutRefund(ctx context.Context, escrow common.Address, milestoneIndex uint8) (*types.Transaction, error)
	ClaimMilestoneArbitratorTimeout(ctx context.Context, escrow common.Address, milestoneIndex uint8) (*types.Transaction, error)
	AbortRemainingMilestones(ctx context.Context, escrow common.Address) (*types.Transaction, error)

	// Backup agent operations
	ActivateBackup(ctx context.Context, escrow common.Address) (*types.Transaction, error)

	// Emergency response protocol (paper §4.9)
	FreezeAddress(ctx context.Context, factory common.Address, target common.Address) (*types.Transaction, error)
	UnfreezeAddress(ctx context.Context, factory common.Address, target common.Address) (*types.Transaction, error)
	FreezeEscrow(ctx context.Context, factory common.Address, escrowID *big.Int) (*types.Transaction, error)
	UnfreezeEscrow(ctx context.Context, factory common.Address, escrowID *big.Int) (*types.Transaction, error)
	EmergencyResolve(ctx context.Context, factory common.Address, escrowID *big.Int, workerAwardBps uint16) (*types.Transaction, error)
	IsFrozenAddress(ctx context.Context, factory common.Address, target common.Address) (bool, error)
}

// Compile-time check that *Client satisfies ChainClient.
var _ ChainClient = (*Client)(nil)
