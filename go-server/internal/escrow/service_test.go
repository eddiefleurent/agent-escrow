package escrow

import (
	"errors"
	"math/big"
	"testing"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/ethereum/go-ethereum/common"
)

func TestValidationHelpers(t *testing.T) {
	t.Parallel()

	if !IsValidation(ErrValidation) {
		t.Fatal("expected ErrValidation to be detected")
	}
	if IsValidation(errors.New("other")) {
		t.Fatal("expected unrelated error not to be validation")
	}
}

func TestAddressAndTokenHelpers(t *testing.T) {
	t.Parallel()

	if got := pendingEscrowAddress("0xabc123"); got != "pending:abc123" {
		t.Fatalf("unexpected pending escrow address: %q", got)
	}

	addr, err := parseAddress("buyer", "0x1111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("expected valid address parse, got %v", err)
	}
	if addr == (common.Address{}) {
		t.Fatal("expected non-zero parsed address")
	}

	if _, err := parseAddress("buyer", "bad"); err == nil {
		t.Fatal("expected parseAddress to reject invalid input")
	}

	if !IsERC20Token("0x1111111111111111111111111111111111111111") {
		t.Fatal("expected non-zero token address to be ERC20")
	}
	if IsERC20Token(zeroAddress) {
		t.Fatal("expected zero-address token to be treated as ETH")
	}
	if NormalizeToken(zeroAddress) != "" {
		t.Fatal("expected NormalizeToken to convert zero address to empty string")
	}
}

func TestProofParsingHelpers(t *testing.T) {
	t.Parallel()

	hash, err := ParseProofHashHex("0x" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11" + "11")
	if err != nil {
		t.Fatalf("expected valid hash parse, got %v", err)
	}
	for i, b := range hash {
		if b != 0x11 {
			t.Fatalf("byte %d: expected 0x11, got 0x%x", i, b)
		}
	}

	if _, err := ParseProofHashHex("0x1234"); err == nil {
		t.Fatal("expected proof hash length validation error")
	}
	if _, err := ParseProofHashHex("  0x" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "22" + "  "); err != nil {
		t.Fatalf("expected surrounding whitespace in proof hash to be tolerated, got %v", err)
	}
	if _, err := ParseProofHexBytes("0x1234"); err != nil {
		t.Fatalf("expected valid proof bytes parse, got %v", err)
	}
	if _, err := ParseProofHexBytes("  0x1234  "); err != nil {
		t.Fatalf("expected surrounding whitespace in proof bytes to be tolerated, got %v", err)
	}
	if _, err := ParseProofHexBytes("1234"); err == nil {
		t.Fatal("expected missing 0x prefix to fail")
	}
}

func TestMilestoneAndStakeHelpers(t *testing.T) {
	t.Parallel()

	escrow := &storage.Escrow{WorkerStake: "1", VerifierStakePerVerifier: "0"}
	if !HasStake(escrow) {
		t.Fatal("expected HasStake true when worker stake is positive")
	}
	escrow.WorkerStake = "0"
	if HasStake(escrow) {
		t.Fatal("expected HasStake false when both stakes are zero")
	}

	idx := 0
	if got, err := validateMilestoneIndex(2, &idx); err != nil || got == nil || *got != 0 {
		t.Fatalf("expected valid milestone index, got %v err=%v", got, err)
	}
	if got, err := validateMilestoneIndex(1, nil); err != nil || got != nil {
		t.Fatalf("expected nil index to be valid for single-milestone escrow, got %v err=%v", got, err)
	}
	if _, err := validateMilestoneIndex(2, nil); err == nil {
		t.Fatal("expected required milestone index error for multi-milestone escrow")
	}
	if _, err := validateOptionalMilestoneIndex(1, nil); err != nil {
		t.Fatalf("expected optional nil index to succeed, got %v", err)
	}
}

func TestBuildCreateEscrowIntentIDDeterministic(t *testing.T) {
	t.Parallel()

	input := CreateEscrowInput{
		QuorumThreshold:          1,
		QuorumVerifierCount:      1,
		VerifierStakePerVerifier: big.NewInt(0),
		Amount:                   big.NewInt(100),
		WorkerStake:              big.NewInt(0),
		SubmissionDeadline:       1000,
		ReviewPeriodSeconds:      60,
		DisputePeriodSeconds:     60,
		ArbitratorTimeoutSeconds: 60,
		Token:                    common.Address{},
		ServiceTier:              0,
	}
	factory := common.HexToAddress("0x1234567890123456789012345678901234567890")
	buyer := common.HexToAddress("0x1111111111111111111111111111111111111111")
	worker := common.HexToAddress("0x2222222222222222222222222222222222222222")
	arbitrator := common.HexToAddress("0x3333333333333333333333333333333333333333")
	verifierPanel := []string{"0x4444444444444444444444444444444444444444"}

	id1, err := buildCreateEscrowIntentID(input, 84532, factory, buyer, worker, arbitrator, verifierPanel)
	if err != nil {
		t.Fatalf("buildCreateEscrowIntentID first call: %v", err)
	}
	id2, err := buildCreateEscrowIntentID(input, 84532, factory, buyer, worker, arbitrator, verifierPanel)
	if err != nil {
		t.Fatalf("buildCreateEscrowIntentID second call: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("expected deterministic intent id, got %q and %q", id1, id2)
	}
	if id1 == "" || len(id1) < 10 {
		t.Fatalf("expected non-empty intent id, got %q", id1)
	}
}
