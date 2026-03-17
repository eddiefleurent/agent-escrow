package chain

import (
	"context"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestEmergencyTxMethodsOfflineMode(t *testing.T) {
	t.Parallel()

	client, err := NewClient("", "", 84532)
	if err != nil {
		t.Fatalf("new offline client: %v", err)
	}

	ctx := context.Background()
	factory := common.HexToAddress("0x1234567890123456789012345678901234567890")
	target := common.HexToAddress("0x1111111111111111111111111111111111111111")
	escrowID := big.NewInt(7)

	cases := []struct {
		name string
		call func() error
	}{
		{name: "FreezeAddress", call: func() error { _, e := client.FreezeAddress(ctx, factory, target); return e }},
		{name: "UnfreezeAddress", call: func() error { _, e := client.UnfreezeAddress(ctx, factory, target); return e }},
		{name: "FreezeEscrow", call: func() error { _, e := client.FreezeEscrow(ctx, factory, escrowID); return e }},
		{name: "UnfreezeEscrow", call: func() error { _, e := client.UnfreezeEscrow(ctx, factory, escrowID); return e }},
		{name: "EmergencyResolve", call: func() error { _, e := client.EmergencyResolve(ctx, factory, escrowID, 5000); return e }},
	}

	for _, tc := range cases {
		err := tc.call()
		if err == nil {
			t.Fatalf("%s: expected error in offline mode", tc.name)
		}
		if !strings.Contains(err.Error(), "chain client not connected") {
			t.Fatalf("%s: expected offline-mode connection error, got %v", tc.name, err)
		}
	}
}

func TestIsFrozenAddressOfflineMode(t *testing.T) {
	t.Parallel()

	client, err := NewClient("", "", 84532)
	if err != nil {
		t.Fatalf("new offline client: %v", err)
	}

	_, err = client.IsFrozenAddress(
		context.Background(),
		common.HexToAddress("0x1234567890123456789012345678901234567890"),
		common.HexToAddress("0x1111111111111111111111111111111111111111"),
	)
	if err == nil {
		t.Fatal("expected error in offline mode")
	}
	if !strings.Contains(err.Error(), "call frozenAddresses") {
		t.Fatalf("expected call frozenAddresses context, got %v", err)
	}
}
