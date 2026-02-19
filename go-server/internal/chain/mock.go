package chain

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// MockClient implements ChainClient for testing without a live RPC connection.
// Set the exported fields/functions to control behavior in tests.
type MockClient struct {
	mu sync.Mutex

	addr    common.Address
	chainID *big.Int

	BlockNum    uint64
	BlockNumErr error

	Logs    []types.Log
	LogsErr error

	// Receipt to return from TransactionReceipt. If nil, returns "not found" error.
	Receipt    *types.Receipt
	ReceiptErr error

	CallResult []byte
	CallErr    error

	// Track calls for assertions
	SentTxs []MockTxRecord

	// Per-method error overrides
	CreateEscrowErr        error
	FundErr                error
	SubmitErr              error
	ApproveByBuyerErr      error
	ApproveByVerifierErr   error
	RejectByVerifierErr    error
	DisputeErr             error
	EscalateSilenceErr     error
	ResolveDisputeErr      error
	ClaimTimeoutRefundErr  error
	ClaimArbitratorTimeErr error
	StatusVal              uint8
	StatusErr              error
}

type MockTxRecord struct {
	Method string
	To     common.Address
	Value  *big.Int
}

func NewMockClient() *MockClient {
	return &MockClient{
		addr:    common.HexToAddress("0x1111111111111111111111111111111111111111"),
		chainID: big.NewInt(84532),
	}
}

func (m *MockClient) Address() common.Address { return m.addr }
func (m *MockClient) ChainID() *big.Int       { return m.chainID }

func (m *MockClient) BlockNumber(_ context.Context) (uint64, error) {
	if m.BlockNumErr != nil {
		return 0, m.BlockNumErr
	}
	return m.BlockNum, nil
}

func (m *MockClient) FilterLogs(_ context.Context, _ []common.Address, _ [][]common.Hash, _, _ uint64) ([]types.Log, error) {
	if m.LogsErr != nil {
		return nil, m.LogsErr
	}
	return m.Logs, nil
}

func (m *MockClient) SendTx(_ context.Context, to common.Address, _ []byte, value *big.Int) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SentTxs = append(m.SentTxs, MockTxRecord{To: to, Value: value})
	return makeFakeTx(), nil
}

func (m *MockClient) CallContract(_ context.Context, _ common.Address, _ []byte) ([]byte, error) {
	return m.CallResult, m.CallErr
}

func (m *MockClient) TransactionReceipt(_ context.Context, _ common.Hash) (*types.Receipt, error) {
	if m.ReceiptErr != nil {
		return nil, m.ReceiptErr
	}
	if m.Receipt != nil {
		return m.Receipt, nil
	}
	return nil, fmt.Errorf("receipt not found")
}

func (m *MockClient) CreateEscrow(_ context.Context, _ common.Address, _ CreateEscrowParams) (*types.Transaction, error) {
	if m.CreateEscrowErr != nil {
		return nil, m.CreateEscrowErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "createEscrow"})
	return makeFakeTx(), nil
}

func (m *MockClient) Fund(_ context.Context, addr common.Address, amount *big.Int) (*types.Transaction, error) {
	if m.FundErr != nil {
		return nil, m.FundErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "fund", To: addr, Value: amount})
	return makeFakeTx(), nil
}

func (m *MockClient) Submit(_ context.Context, addr common.Address, _ [32]byte, _ string) (*types.Transaction, error) {
	if m.SubmitErr != nil {
		return nil, m.SubmitErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "submit", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) ApproveByBuyer(_ context.Context, addr common.Address) (*types.Transaction, error) {
	if m.ApproveByBuyerErr != nil {
		return nil, m.ApproveByBuyerErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "approveByBuyer", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) ApproveByVerifier(_ context.Context, addr common.Address) (*types.Transaction, error) {
	if m.ApproveByVerifierErr != nil {
		return nil, m.ApproveByVerifierErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "approveByVerifier", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) RejectByVerifier(_ context.Context, addr common.Address, _ string) (*types.Transaction, error) {
	if m.RejectByVerifierErr != nil {
		return nil, m.RejectByVerifierErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "rejectByVerifier", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) Dispute(_ context.Context, addr common.Address, _ string) (*types.Transaction, error) {
	if m.DisputeErr != nil {
		return nil, m.DisputeErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "dispute", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) EscalateSilence(_ context.Context, addr common.Address, _ string) (*types.Transaction, error) {
	if m.EscalateSilenceErr != nil {
		return nil, m.EscalateSilenceErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "escalateSilence", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) ResolveDispute(_ context.Context, addr common.Address, _ uint16, _ string) (*types.Transaction, error) {
	if m.ResolveDisputeErr != nil {
		return nil, m.ResolveDisputeErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "resolveDispute", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) ClaimTimeoutRefund(_ context.Context, addr common.Address) (*types.Transaction, error) {
	if m.ClaimTimeoutRefundErr != nil {
		return nil, m.ClaimTimeoutRefundErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "claimTimeoutRefund", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) ClaimArbitratorTimeout(_ context.Context, addr common.Address) (*types.Transaction, error) {
	if m.ClaimArbitratorTimeErr != nil {
		return nil, m.ClaimArbitratorTimeErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "claimArbitratorTimeout", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) Status(_ context.Context, _ common.Address) (uint8, error) {
	return m.StatusVal, m.StatusErr
}

// MakeEscrowCreatedReceipt builds a fake receipt containing an EscrowCreated event log
// for use in tests that exercise receipt parsing.
func MakeEscrowCreatedReceipt(escrowID int64, escrowAddr, buyer common.Address) *types.Receipt {
	escrowCreatedID := FactoryABI.Events["EscrowCreated"].ID

	// Indexed: escrowId (topic1), escrow address (topic2), buyer (topic3)
	idBytes := common.BigToHash(big.NewInt(escrowID))
	addrBytes := common.BytesToHash(escrowAddr.Bytes())
	buyerBytes := common.BytesToHash(buyer.Bytes())

	// Non-indexed: worker, verifier, arbitrator, taskSpecHash
	nonIndexed, _ := FactoryABI.Events["EscrowCreated"].Inputs.NonIndexed().Pack(
		common.HexToAddress("0x2222222222222222222222222222222222222222"),
		common.HexToAddress("0x3333333333333333333333333333333333333333"),
		common.HexToAddress("0x4444444444444444444444444444444444444444"),
		[32]byte{0x01},
	)

	return &types.Receipt{
		Status: types.ReceiptStatusSuccessful,
		Logs: []*types.Log{
			{
				Topics: []common.Hash{escrowCreatedID, idBytes, addrBytes, buyerBytes},
				Data:   nonIndexed,
			},
		},
	}
}

func makeFakeTx() *types.Transaction {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("failed to generate random bytes for fake tx: " + err.Error())
	}
	return types.NewTransaction(0, common.Address{}, big.NewInt(0), 21000, big.NewInt(1), b[:])
}

var _ ChainClient = (*MockClient)(nil)
