package chain

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// CreateEscrowResult holds on-chain identifiers extracted from the EscrowCreated event.
type CreateEscrowResult struct {
	EscrowAddress common.Address
	EscrowID      int64
}

// WaitMinedAndParseEscrow polls for the tx receipt and parses the EscrowCreated event
// to extract the on-chain escrow address and ID. The caller provides any ChainClient
// so this works with both the real client and test mocks.
func WaitMinedAndParseEscrow(ctx context.Context, cc ChainClient, txHash common.Hash) (*CreateEscrowResult, error) {
	receipt, err := WaitMined(ctx, cc, txHash)
	if err != nil {
		return nil, err
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("transaction reverted (status %d)", receipt.Status)
	}
	return parseEscrowCreated(receipt)
}

// WaitMined polls for a transaction receipt until it is available or the context expires.
func WaitMined(ctx context.Context, cc ChainClient, txHash common.Hash) (*types.Receipt, error) {
	const maxAttempts = 60
	delay := 500 * time.Millisecond

	for range maxAttempts {
		receipt, err := cc.TransactionReceipt(ctx, txHash)
		if err == nil && receipt != nil {
			return receipt, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}

		if delay < 4*time.Second {
			delay = delay * 3 / 2
		}
	}
	return nil, fmt.Errorf("receipt not found after %d attempts for tx %s", maxAttempts, txHash.Hex())
}

func parseEscrowCreated(receipt *types.Receipt) (*CreateEscrowResult, error) {
	escrowCreatedID := FactoryABI.Events["EscrowCreated"].ID

	for _, lg := range receipt.Logs {
		if len(lg.Topics) < 3 || lg.Topics[0] != escrowCreatedID {
			continue
		}
		escrowIDBig := new(big.Int).SetBytes(lg.Topics[1].Bytes())
		if !escrowIDBig.IsInt64() {
			return nil, fmt.Errorf("escrowID overflows int64: %s", escrowIDBig.String())
		}
		escrowAddr := common.BytesToAddress(lg.Topics[2].Bytes())
		return &CreateEscrowResult{
			EscrowAddress: escrowAddr,
			EscrowID:      escrowIDBig.Int64(),
		}, nil
	}
	return nil, fmt.Errorf("EscrowCreated event not found in receipt (tx %s)", receipt.TxHash.Hex())
}
