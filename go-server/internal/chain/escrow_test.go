package chain

import (
	"context"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestEscrowAdditionalMethodsOfflineMode(t *testing.T) {
	t.Parallel()

	client, err := NewClient("", "", 84532)
	if err != nil {
		t.Fatalf("new offline client: %v", err)
	}
	ctx := context.Background()
	escrow := common.HexToAddress("0x1111111111111111111111111111111111111111")
	hash := [32]byte{}

	cases := []struct {
		name string
		call func() error
	}{
		{name: "DepositVerifierStake", call: func() error { _, e := client.DepositVerifierStake(ctx, escrow, big.NewInt(1)); return e }},
		{name: "WithdrawStake", call: func() error { _, e := client.WithdrawStake(ctx, escrow); return e }},
		{name: "ApproveERC20", call: func() error { _, e := client.ApproveERC20(ctx, escrow, escrow, big.NewInt(1)); return e }},
		{name: "VerifyAndApprove", call: func() error { _, e := client.VerifyAndApprove(ctx, escrow, []byte{0x01}); return e }},
		{name: "CastVerifierVote", call: func() error { _, e := client.CastVerifierVote(ctx, escrow, true, "ipfs://reason"); return e }},
		{name: "Dispute", call: func() error { _, e := client.Dispute(ctx, escrow, "ipfs://reason"); return e }},
		{name: "EscalateSilence", call: func() error { _, e := client.EscalateSilence(ctx, escrow, "ipfs://reason"); return e }},
		{name: "ClaimArbitratorTimeout", call: func() error { _, e := client.ClaimArbitratorTimeout(ctx, escrow); return e }},
		{name: "SubmitMilestone", call: func() error {
			_, e := client.SubmitMilestone(ctx, escrow, 0, hash, "ipfs://submission", hash)
			return e
		}},
		{name: "VerifyAndApproveMilestone", call: func() error { _, e := client.VerifyAndApproveMilestone(ctx, escrow, 0, []byte{0x01}); return e }},
		{name: "ApproveMilestoneByBuyer", call: func() error { _, e := client.ApproveMilestoneByBuyer(ctx, escrow, 0); return e }},
		{name: "CastMilestoneVerifierVote", call: func() error {
			_, e := client.CastMilestoneVerifierVote(ctx, escrow, 0, true, "ipfs://reason")
			return e
		}},
		{name: "DisputeMilestone", call: func() error { _, e := client.DisputeMilestone(ctx, escrow, 0, "ipfs://reason"); return e }},
		{name: "EscalateMilestoneSilence", call: func() error { _, e := client.EscalateMilestoneSilence(ctx, escrow, 0, "ipfs://reason"); return e }},
		{name: "ResolveMilestoneDispute", call: func() error {
			_, e := client.ResolveMilestoneDispute(ctx, escrow, 0, 5000, "ipfs://resolution")
			return e
		}},
		{name: "ClaimMilestoneTimeoutRefund", call: func() error { _, e := client.ClaimMilestoneTimeoutRefund(ctx, escrow, 0); return e }},
		{name: "ClaimMilestoneArbitratorTimeout", call: func() error { _, e := client.ClaimMilestoneArbitratorTimeout(ctx, escrow, 0); return e }},
		{name: "AbortRemainingMilestones", call: func() error { _, e := client.AbortRemainingMilestones(ctx, escrow); return e }},
	}

	for _, tc := range cases {
		err := tc.call()
		if err == nil {
			t.Fatalf("%s: expected offline mode error", tc.name)
		}
		if !strings.Contains(err.Error(), "chain client not connected") {
			t.Fatalf("%s: expected offline mode error, got %v", tc.name, err)
		}
	}
}
