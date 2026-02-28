package chain

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// MockClient implements ChainClient for testing without a live RPC connection.
//
// Exported fields can be set directly before concurrent access begins (the
// typical single-goroutine test setup pattern). All interface methods acquire
// the internal RWMutex so concurrent reads and writes from background
// goroutines (e.g. the indexer) are safe.
type MockClient struct {
	mu sync.RWMutex

	addr    common.Address
	chainID *big.Int

	BlockNum    uint64
	BlockNumErr error

	Logs    []types.Log
	LogsErr error

	Receipt    *types.Receipt
	ReceiptErr error

	CallResult []byte
	CallErr    error

	SentTxs []MockTxRecord

	Delay time.Duration

	CreateEscrowErr        error
	FundErr                error
	FundWithAuthErr        error
	DepositStakeErr        error
	ApproveERC20Err        error
	SubmitErr              error
	VerifyAndApproveErr    error
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

	// Milestone-specific error fields
	SubmitMilestoneErr              error
	VerifyAndApproveMilestoneErr    error
	ApproveMilestoneBuyerErr        error
	ApproveMilestoneVerifierErr     error
	RejectMilestoneVerifierErr      error
	DisputeMilestoneErr             error
	EscalateMilestoneSilenceErr     error
	ResolveMilestoneDisputeErr      error
	ClaimMilestoneTimeoutErr        error
	ClaimMilestoneArbitratorTimeErr error
	AbortRemainingMilestonesErr     error

	// Backup agent error field
	ActivateBackupErr error

	// Emergency response protocol error fields
	FreezeAddressErr    error
	UnfreezeAddressErr  error
	FreezeEscrowErr     error
	UnfreezeEscrowErr   error
	EmergencyResolveErr error
	IsFrozenVal         bool
	IsFrozenErr         error
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

// Lock / Unlock expose the internal mutex for tests that need to mutate
// fields while a background goroutine is calling interface methods.
func (m *MockClient) Lock()   { m.mu.Lock() }
func (m *MockClient) Unlock() { m.mu.Unlock() }

func (m *MockClient) Address() common.Address { return m.addr }
func (m *MockClient) ChainID() *big.Int       { return m.chainID }

// applyDelay reads Delay under the lock, then waits outside the lock to avoid
// holding it for the duration of the sleep.
func (m *MockClient) applyDelay(ctx context.Context) error {
	m.mu.RLock()
	d := m.Delay
	m.mu.RUnlock()
	if d <= 0 {
		return nil
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *MockClient) BlockNumber(ctx context.Context) (uint64, error) {
	if err := m.applyDelay(ctx); err != nil {
		return 0, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.BlockNumErr != nil {
		return 0, m.BlockNumErr
	}
	return m.BlockNum, nil
}

func (m *MockClient) FilterLogs(ctx context.Context, _ []common.Address, _ [][]common.Hash, _, _ uint64) ([]types.Log, error) {
	if err := m.applyDelay(ctx); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
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
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.CallResult, m.CallErr
}

func (m *MockClient) TransactionReceipt(_ context.Context, _ common.Hash) (*types.Receipt, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.ReceiptErr != nil {
		return nil, m.ReceiptErr
	}
	if m.Receipt != nil {
		return m.Receipt, nil
	}
	return nil, errors.New("receipt not found")
}

func (m *MockClient) CreateEscrow(_ context.Context, _ common.Address, _ CreateEscrowParams) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.CreateEscrowErr != nil {
		return nil, m.CreateEscrowErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "createEscrow"})
	return makeFakeTx(), nil
}

func (m *MockClient) Fund(ctx context.Context, addr common.Address, amount *big.Int) (*types.Transaction, error) {
	if err := m.applyDelay(ctx); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FundErr != nil {
		return nil, m.FundErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "fund", To: addr, Value: amount})
	return makeFakeTx(), nil
}

func (m *MockClient) FundWithAuthorization(ctx context.Context, addr common.Address, from common.Address, validAfter, validBefore *big.Int, nonce [32]byte, v uint8, r, s [32]byte) (*types.Transaction, error) {
	if err := m.applyDelay(ctx); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FundWithAuthErr != nil {
		return nil, m.FundWithAuthErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "fundWithAuthorization", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) DepositStake(ctx context.Context, addr common.Address, stakeAmount *big.Int) (*types.Transaction, error) {
	if err := m.applyDelay(ctx); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.DepositStakeErr != nil {
		return nil, m.DepositStakeErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "depositStake", To: addr, Value: stakeAmount})
	return makeFakeTx(), nil
}

func (m *MockClient) ApproveERC20(_ context.Context, tokenAddr common.Address, spender common.Address, amount *big.Int) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ApproveERC20Err != nil {
		return nil, m.ApproveERC20Err
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "approveERC20", To: tokenAddr, Value: amount})
	return makeFakeTx(), nil
}

func (m *MockClient) Submit(_ context.Context, addr common.Address, _ [32]byte, _ string, _ [32]byte) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.SubmitErr != nil {
		return nil, m.SubmitErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "submit", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) VerifyAndApprove(_ context.Context, addr common.Address, _ []byte) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.VerifyAndApproveErr != nil {
		return nil, m.VerifyAndApproveErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "verifyAndApprove", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) ApproveByBuyer(_ context.Context, addr common.Address) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ApproveByBuyerErr != nil {
		return nil, m.ApproveByBuyerErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "approveByBuyer", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) ApproveByVerifier(_ context.Context, addr common.Address) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ApproveByVerifierErr != nil {
		return nil, m.ApproveByVerifierErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "approveByVerifier", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) RejectByVerifier(_ context.Context, addr common.Address, _ string) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.RejectByVerifierErr != nil {
		return nil, m.RejectByVerifierErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "rejectByVerifier", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) Dispute(_ context.Context, addr common.Address, _ string) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.DisputeErr != nil {
		return nil, m.DisputeErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "dispute", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) EscalateSilence(_ context.Context, addr common.Address, _ string) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.EscalateSilenceErr != nil {
		return nil, m.EscalateSilenceErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "escalateSilence", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) ResolveDispute(_ context.Context, addr common.Address, _ uint16, _ string) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ResolveDisputeErr != nil {
		return nil, m.ResolveDisputeErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "resolveDispute", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) ClaimTimeoutRefund(_ context.Context, addr common.Address) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ClaimTimeoutRefundErr != nil {
		return nil, m.ClaimTimeoutRefundErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "claimTimeoutRefund", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) ClaimArbitratorTimeout(_ context.Context, addr common.Address) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ClaimArbitratorTimeErr != nil {
		return nil, m.ClaimArbitratorTimeErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "claimArbitratorTimeout", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) Status(_ context.Context, _ common.Address) (uint8, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.StatusVal, m.StatusErr
}

func (m *MockClient) SubmitMilestone(_ context.Context, addr common.Address, idx uint8, _ [32]byte, _ string, _ [32]byte) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.SubmitMilestoneErr != nil {
		return nil, m.SubmitMilestoneErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "submitMilestone", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) VerifyAndApproveMilestone(_ context.Context, addr common.Address, _ uint8, _ []byte) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.VerifyAndApproveMilestoneErr != nil {
		return nil, m.VerifyAndApproveMilestoneErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "verifyAndApproveMilestone", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) ApproveMilestoneByBuyer(_ context.Context, addr common.Address, _ uint8) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ApproveMilestoneBuyerErr != nil {
		return nil, m.ApproveMilestoneBuyerErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "approveMilestoneByBuyer", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) ApproveMilestoneByVerifier(_ context.Context, addr common.Address, _ uint8) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ApproveMilestoneVerifierErr != nil {
		return nil, m.ApproveMilestoneVerifierErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "approveMilestoneByVerifier", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) RejectMilestoneByVerifier(_ context.Context, addr common.Address, _ uint8, _ string) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.RejectMilestoneVerifierErr != nil {
		return nil, m.RejectMilestoneVerifierErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "rejectMilestoneByVerifier", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) DisputeMilestone(_ context.Context, addr common.Address, _ uint8, _ string) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.DisputeMilestoneErr != nil {
		return nil, m.DisputeMilestoneErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "disputeMilestone", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) EscalateMilestoneSilence(_ context.Context, addr common.Address, _ uint8, _ string) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.EscalateMilestoneSilenceErr != nil {
		return nil, m.EscalateMilestoneSilenceErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "escalateMilestoneSilence", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) ResolveMilestoneDispute(_ context.Context, addr common.Address, _ uint8, _ uint16, _ string) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ResolveMilestoneDisputeErr != nil {
		return nil, m.ResolveMilestoneDisputeErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "resolveMilestoneDispute", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) ClaimMilestoneTimeoutRefund(_ context.Context, addr common.Address, _ uint8) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ClaimMilestoneTimeoutErr != nil {
		return nil, m.ClaimMilestoneTimeoutErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "claimMilestoneTimeoutRefund", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) ClaimMilestoneArbitratorTimeout(_ context.Context, addr common.Address, _ uint8) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ClaimMilestoneArbitratorTimeErr != nil {
		return nil, m.ClaimMilestoneArbitratorTimeErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "claimMilestoneArbitratorTimeout", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) AbortRemainingMilestones(_ context.Context, addr common.Address) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.AbortRemainingMilestonesErr != nil {
		return nil, m.AbortRemainingMilestonesErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "abortRemainingMilestones", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) ActivateBackup(_ context.Context, addr common.Address) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ActivateBackupErr != nil {
		return nil, m.ActivateBackupErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "activateBackup", To: addr})
	return makeFakeTx(), nil
}

func (m *MockClient) FreezeAddress(_ context.Context, _ common.Address, target common.Address) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FreezeAddressErr != nil {
		return nil, m.FreezeAddressErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "freezeAddress", To: target})
	return makeFakeTx(), nil
}

func (m *MockClient) UnfreezeAddress(_ context.Context, _ common.Address, target common.Address) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.UnfreezeAddressErr != nil {
		return nil, m.UnfreezeAddressErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "unfreezeAddress", To: target})
	return makeFakeTx(), nil
}

func (m *MockClient) FreezeEscrow(_ context.Context, factory common.Address, _ *big.Int) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FreezeEscrowErr != nil {
		return nil, m.FreezeEscrowErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "freezeEscrow", To: factory})
	return makeFakeTx(), nil
}

func (m *MockClient) UnfreezeEscrow(_ context.Context, factory common.Address, _ *big.Int) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.UnfreezeEscrowErr != nil {
		return nil, m.UnfreezeEscrowErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "unfreezeEscrow", To: factory})
	return makeFakeTx(), nil
}

func (m *MockClient) EmergencyResolve(_ context.Context, factory common.Address, _ *big.Int, _ uint16) (*types.Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.EmergencyResolveErr != nil {
		return nil, m.EmergencyResolveErr
	}
	m.SentTxs = append(m.SentTxs, MockTxRecord{Method: "emergencyResolve", To: factory})
	return makeFakeTx(), nil
}

func (m *MockClient) IsFrozenAddress(_ context.Context, _ common.Address, _ common.Address) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.IsFrozenVal, m.IsFrozenErr
}

// MakeEscrowCreatedReceipt builds a fake receipt containing an EscrowCreated event log
// for use in tests that exercise receipt parsing.
func MakeEscrowCreatedReceipt(escrowID int64, escrowAddr, buyer common.Address) *types.Receipt {
	escrowCreatedID := FactoryABI.Events["EscrowCreated"].ID

	// Indexed: escrowId (topic1), escrow address (topic2), buyer (topic3)
	idBytes := common.BigToHash(big.NewInt(escrowID))
	addrBytes := common.BytesToHash(escrowAddr.Bytes())
	buyerBytes := common.BytesToHash(buyer.Bytes())

	// Non-indexed: worker, verifier, arbitrator, taskSpecHash, token, serviceTier, zkVerifier, circuitId
	nonIndexed, err := FactoryABI.Events["EscrowCreated"].Inputs.NonIndexed().Pack(
		common.HexToAddress("0x2222222222222222222222222222222222222222"),
		common.HexToAddress("0x3333333333333333333333333333333333333333"),
		common.HexToAddress("0x4444444444444444444444444444444444444444"),
		[32]byte{0x01},
		common.Address{},
		uint8(0),
		common.Address{},
		[32]byte{},
	)
	if err != nil {
		panic("failed to pack EscrowCreated non-indexed args: " + err.Error())
	}

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
