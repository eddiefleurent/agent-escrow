package chain

import (
	"context"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestCreateEscrowPacksTupleBeforeOfflineSend(t *testing.T) {
	t.Parallel()

	client, err := NewClient("", "", 84532)
	if err != nil {
		t.Fatalf("new offline client: %v", err)
	}

	var circuitID [32]byte
	circuitID[0] = 0x42

	params := CreateEscrowParams{
		Buyer:                    common.HexToAddress("0x1111111111111111111111111111111111111111"),
		Worker:                   common.HexToAddress("0x2222222222222222222222222222222222222222"),
		Arbitrator:               common.HexToAddress("0x3333333333333333333333333333333333333333"),
		Amount:                   big.NewInt(1),
		VerifierStakePerVerifier: big.NewInt(0),
		WorkerStake:              big.NewInt(0),
		SubmissionDeadline:       100,
		ReviewPeriodSeconds:      10,
		DisputePeriodSeconds:     20,
		ArbitratorTimeoutSeconds: 30,
		CircuitID:                circuitID,
	}

	_, err = client.CreateEscrow(context.Background(), common.HexToAddress("0x1234567890123456789012345678901234567890"), params)
	if err == nil {
		t.Fatal("expected offline mode error")
	}
	if strings.Contains(err.Error(), "pack createEscrow") {
		t.Fatalf("expected packing to succeed before offline send, got %v", err)
	}
	if !strings.Contains(err.Error(), "chain client not connected") {
		t.Fatalf("expected offline send error, got %v", err)
	}
}

func TestCreateEscrowPacksNonZeroCircuitID(t *testing.T) {
	t.Parallel()

	var circuitID [32]byte
	circuitID[0] = 0x42

	params := CreateEscrowParams{
		Buyer:                    common.HexToAddress("0x1111111111111111111111111111111111111111"),
		Worker:                   common.HexToAddress("0x2222222222222222222222222222222222222222"),
		Arbitrator:               common.HexToAddress("0x3333333333333333333333333333333333333333"),
		Amount:                   big.NewInt(1),
		VerifierStakePerVerifier: big.NewInt(0),
		WorkerStake:              big.NewInt(0),
		SubmissionDeadline:       100,
		ReviewPeriodSeconds:      10,
		DisputePeriodSeconds:     20,
		ArbitratorTimeoutSeconds: 30,
		CircuitID:                circuitID,
	}

	calldata, err := packCreateEscrow(params)
	if err != nil {
		t.Fatalf("pack createEscrow: %v", err)
	}

	// Verify the non-zero circuitID was actually encoded into the calldata
	// (not silently zeroed due to reflection mapping mismatch).
	expected := "4200000000000000000000000000000000000000000000000000000000000000"
	if !strings.Contains(hex.EncodeToString(calldata), expected) {
		t.Fatalf("expected non-zero circuitID to be present in packed calldata; got %s", hex.EncodeToString(calldata))
	}
}
