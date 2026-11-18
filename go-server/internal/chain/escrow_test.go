package chain

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
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

type escrowGetterRPC struct {
	address common.Address
	results map[string][]byte
}

func (s *escrowGetterRPC) Call(args struct {
	To    common.Address `json:"to"`
	Input hexutil.Bytes  `json:"input"`
}, block string) (hexutil.Bytes, error) {
	result, ok := s.results[args.Input.String()]
	if !ok || args.To != s.address || block != "latest" {
		return nil, fmt.Errorf("unexpected eth_call to %s with input %s at %s", args.To, args.Input, block)
	}
	return result, nil
}

func TestMilestoneGetters(t *testing.T) {
	t.Parallel()

	const wantStatus uint8 = 3
	const wantCount uint8 = 2
	const index uint8 = 1
	escrow := common.HexToAddress("0x123456")
	statusCall, err := EscrowABI.Pack("milestones", big.NewInt(int64(index)))
	if err != nil {
		t.Fatal(err)
	}
	statusResult, err := EscrowABI.Methods["milestones"].Outputs.Pack(
		big.NewInt(100), uint64(200), [32]byte{1}, "ipfs://submission",
		[32]byte{2}, uint64(120), uint64(0), uint64(130), "ipfs://dispute", wantStatus, uint16(5000),
	)
	if err != nil {
		t.Fatal(err)
	}
	countCall, err := EscrowABI.Pack("milestoneCount")
	if err != nil {
		t.Fatal(err)
	}
	countResult, err := EscrowABI.Methods["milestoneCount"].Outputs.Pack(wantCount)
	if err != nil {
		t.Fatal(err)
	}

	server := rpc.NewServer()
	t.Cleanup(server.Stop)
	if err := server.RegisterName("eth", &escrowGetterRPC{
		address: escrow,
		results: map[string][]byte{
			hexutil.Encode(statusCall): statusResult,
			hexutil.Encode(countCall):  countResult,
		},
	}); err != nil {
		t.Fatal(err)
	}
	client := &Client{eth: ethclient.NewClient(rpc.DialInProc(server))}
	t.Cleanup(client.eth.Close)

	status, err := client.MilestoneStatus(t.Context(), escrow, index)
	if err != nil || status != wantStatus {
		t.Fatalf("MilestoneStatus = %d, %v; want %d", status, err, wantStatus)
	}
	count, err := client.MilestoneCount(t.Context(), escrow)
	if err != nil || count != wantCount {
		t.Fatalf("MilestoneCount = %d, %v; want %d", count, err, wantCount)
	}
}
