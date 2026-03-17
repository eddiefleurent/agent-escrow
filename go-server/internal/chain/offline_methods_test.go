package chain

import (
	"context"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func newOfflineClient(t *testing.T) *Client {
	t.Helper()
	client, err := NewClient("", "", 84532)
	if err != nil {
		t.Fatalf("new offline client: %v", err)
	}
	return client
}

func TestCreateEscrowOfflineMode(t *testing.T) {
	t.Parallel()

	client := newOfflineClient(t)

	_, err := client.CreateEscrow(context.Background(),
		common.HexToAddress("0x1234567890123456789012345678901234567890"),
		CreateEscrowParams{
			Buyer:                    common.HexToAddress("0x1111111111111111111111111111111111111111"),
			Worker:                   common.HexToAddress("0x2222222222222222222222222222222222222222"),
			Arbitrator:               common.HexToAddress("0x3333333333333333333333333333333333333333"),
			Amount:                   big.NewInt(1),
			SubmissionDeadline:       100,
			ReviewPeriodSeconds:      10,
			DisputePeriodSeconds:     20,
			ArbitratorTimeoutSeconds: 30,
		},
	)
	if err == nil {
		t.Fatal("expected offline mode error")
	}
	if !strings.Contains(err.Error(), "chain client not connected") {
		t.Fatalf("expected offline mode error, got %v", err)
	}
}

func TestEscrowMethodsOfflineMode(t *testing.T) {
	t.Parallel()

	client := newOfflineClient(t)
	ctx := context.Background()
	escrow := common.HexToAddress("0x1111111111111111111111111111111111111111")
	nonce := [32]byte{}
	hash := [32]byte{}

	cases := []struct {
		name string
		call func() error
	}{
		{name: "Fund", call: func() error { _, e := client.Fund(ctx, escrow, big.NewInt(1)); return e }},
		{name: "CancelBeforeFunding", call: func() error { _, e := client.CancelBeforeFunding(ctx, escrow); return e }},
		{name: "FundWithAuthorization", call: func() error {
			_, e := client.FundWithAuthorization(ctx, escrow, escrow, big.NewInt(0), big.NewInt(1), nonce, 27, hash, hash)
			return e
		}},
		{name: "DepositStake", call: func() error { _, e := client.DepositStake(ctx, escrow, big.NewInt(1)); return e }},
		{name: "Submit", call: func() error { _, e := client.Submit(ctx, escrow, hash, "ipfs://submission", hash); return e }},
		{name: "ApproveByBuyer", call: func() error { _, e := client.ApproveByBuyer(ctx, escrow); return e }},
		{name: "ResolveDispute", call: func() error { _, e := client.ResolveDispute(ctx, escrow, 5000, "ipfs://resolution"); return e }},
		{name: "ClaimTimeoutRefund", call: func() error { _, e := client.ClaimTimeoutRefund(ctx, escrow); return e }},
		{name: "ActivateBackup", call: func() error { _, e := client.ActivateBackup(ctx, escrow); return e }},
	}

	for _, tc := range cases {
		err := tc.call()
		if err == nil {
			t.Errorf("%s: expected offline mode error", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), "chain client not connected") {
			t.Errorf("%s: expected offline mode error, got %v", tc.name, err)
		}
	}
}

func TestStatusOfflineMode(t *testing.T) {
	t.Parallel()

	client := newOfflineClient(t)

	_, err := client.Status(context.Background(), common.HexToAddress("0x1111111111111111111111111111111111111111"))
	if err == nil {
		t.Fatal("expected offline mode error")
	}
	if !strings.Contains(err.Error(), "call status") {
		t.Fatalf("expected call status context in error, got %v", err)
	}
}
