package chain

import (
	"context"
	"errors"
	"testing"

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
