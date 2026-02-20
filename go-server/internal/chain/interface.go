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

	// High-level escrow operations
	CreateEscrow(ctx context.Context, factory common.Address, p CreateEscrowParams) (*types.Transaction, error)
	Fund(ctx context.Context, escrow common.Address, amount *big.Int) (*types.Transaction, error)
	Submit(ctx context.Context, escrow common.Address, submissionHash [32]byte, submissionURI string) (*types.Transaction, error)
	ApproveByBuyer(ctx context.Context, escrow common.Address) (*types.Transaction, error)
	ApproveByVerifier(ctx context.Context, escrow common.Address) (*types.Transaction, error)
	RejectByVerifier(ctx context.Context, escrow common.Address, reasonURI string) (*types.Transaction, error)
	Dispute(ctx context.Context, escrow common.Address, reasonURI string) (*types.Transaction, error)
	EscalateSilence(ctx context.Context, escrow common.Address, reasonURI string) (*types.Transaction, error)
	ResolveDispute(ctx context.Context, escrow common.Address, workerAwardBps uint16, resolutionURI string) (*types.Transaction, error)
	ClaimTimeoutRefund(ctx context.Context, escrow common.Address) (*types.Transaction, error)
	ClaimArbitratorTimeout(ctx context.Context, escrow common.Address) (*types.Transaction, error)
	Status(ctx context.Context, escrow common.Address) (uint8, error)
}

// Compile-time check that *Client satisfies ChainClient.
var _ ChainClient = (*Client)(nil)
