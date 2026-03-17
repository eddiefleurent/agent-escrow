package chain

import (
	"context"
	"errors"
	"math"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestWaitMinedAndParseEscrowHappyPath(t *testing.T) {
	t.Parallel()

	txHash := common.HexToHash("0x1234")
	escrowAddr := common.HexToAddress("0x1111111111111111111111111111111111111111")

	receipt := &types.Receipt{
		Status: types.ReceiptStatusSuccessful,
		TxHash: txHash,
		Logs: []*types.Log{
			{
				Topics: []common.Hash{
					FactoryABI.Events["EscrowCreated"].ID,
					common.BigToHash(big.NewInt(42)),
					common.BytesToHash(escrowAddr.Bytes()),
				},
			},
		},
	}

	mc := NewMockClient()
	mc.Receipt = receipt

	got, err := WaitMinedAndParseEscrow(context.Background(), mc, txHash)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got.EscrowID != 42 {
		t.Fatalf("expected escrow id 42, got %d", got.EscrowID)
	}
	if got.EscrowAddress != escrowAddr {
		t.Fatalf("expected escrow address %s, got %s", escrowAddr.Hex(), got.EscrowAddress.Hex())
	}
}

func TestWaitMinedAndParseEscrowRevertedTx(t *testing.T) {
	t.Parallel()

	txHash := common.HexToHash("0x1234")
	mc := NewMockClient()
	mc.Receipt = &types.Receipt{
		Status: types.ReceiptStatusFailed,
		TxHash: txHash,
	}

	_, err := WaitMinedAndParseEscrow(context.Background(), mc, txHash)
	if err == nil {
		t.Fatal("expected reverted transaction error")
	}
}

func TestParseEscrowCreatedErrors(t *testing.T) {
	t.Parallel()

	txHash := common.HexToHash("0x1234")
	_, err := parseEscrowCreated(&types.Receipt{TxHash: txHash})
	if err == nil {
		t.Fatal("expected missing event error")
	}

	overflow := new(big.Int).Add(big.NewInt(math.MaxInt64), big.NewInt(1))
	receipt := &types.Receipt{
		TxHash: txHash,
		Logs: []*types.Log{
			{
				Topics: []common.Hash{
					FactoryABI.Events["EscrowCreated"].ID,
					common.BigToHash(overflow),
					common.BytesToHash(common.HexToAddress("0x1111111111111111111111111111111111111111").Bytes()),
				},
			},
		},
	}
	_, err = parseEscrowCreated(receipt)
	if err == nil {
		t.Fatal("expected overflow error")
	}
}

func TestWaitMinedHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	mc := NewMockClient()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := WaitMined(ctx, mc, common.HexToHash("0x1234"))
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}
