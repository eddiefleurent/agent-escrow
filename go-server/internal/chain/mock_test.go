package chain

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

func TestNewMockClientDefaults(t *testing.T) {
	t.Parallel()

	mc := NewMockClient()
	if mc.Address() == (common.Address{}) {
		t.Fatal("expected non-zero default mock address")
	}
	if got := mc.ChainID(); got == nil || got.Int64() != 84532 {
		t.Fatalf("expected chain id 84532, got %v", got)
	}
}

func TestMockClientTransactionReceiptBehavior(t *testing.T) {
	t.Parallel()

	mc := NewMockClient()
	_, err := mc.TransactionReceipt(context.Background(), common.HexToHash("0x1"))
	if err == nil {
		t.Fatal("expected receipt-not-found error when receipt is unset")
	}

	wantErr := errors.New("boom")
	mc.ReceiptErr = wantErr
	_, err = mc.TransactionReceipt(context.Background(), common.HexToHash("0x1"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped receipt error, got %v", err)
	}
}

func TestMockClientFundRecordsTransaction(t *testing.T) {
	t.Parallel()

	mc := NewMockClient()
	addr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	amount := big.NewInt(123)

	_, err := mc.Fund(context.Background(), addr, amount)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(mc.SentTxs) != 1 {
		t.Fatalf("expected one recorded tx, got %d", len(mc.SentTxs))
	}
	if mc.SentTxs[0].Method != "fund" {
		t.Fatalf("expected method fund, got %q", mc.SentTxs[0].Method)
	}
}

func TestMockClientDelayHonorsContext(t *testing.T) {
	t.Parallel()

	mc := NewMockClient()
	mc.Delay = 100 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	_, err := mc.BlockNumber(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}

func TestMockClientCreateEscrowStoresParams(t *testing.T) {
	t.Parallel()

	mc := NewMockClient()
	factory := common.HexToAddress("0x1234567890123456789012345678901234567890")
	params := CreateEscrowParams{
		Buyer:                    common.HexToAddress("0x1111111111111111111111111111111111111111"),
		Worker:                   common.HexToAddress("0x2222222222222222222222222222222222222222"),
		Arbitrator:               common.HexToAddress("0x3333333333333333333333333333333333333333"),
		Amount:                   big.NewInt(1),
		SubmissionDeadline:       100,
		ReviewPeriodSeconds:      10,
		DisputePeriodSeconds:     20,
		ArbitratorTimeoutSeconds: 30,
	}

	_, err := mc.CreateEscrow(context.Background(), factory, params)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if mc.LastCreateEscrowParams == nil {
		t.Fatal("expected LastCreateEscrowParams to be recorded")
	}
	if mc.LastCreateEscrowFactory != factory {
		t.Fatalf("expected factory %s, got %s", factory.Hex(), mc.LastCreateEscrowFactory.Hex())
	}
}
