package bidding

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/ethereum/go-ethereum/common"
)

const (
	testBuyerAddr       = "0x1000000000000000000000000000000000000001"
	testWorkerAddr      = "0x2000000000000000000000000000000000000002"
	testWorkerAltAddr   = "0x3000000000000000000000000000000000000003"
	testVerifierAddr    = "0x4000000000000000000000000000000000000004"
	testArbitratorAddr  = "0x5000000000000000000000000000000000000005"
	testFactoryAddr     = "0x6000000000000000000000000000000000000006"
	testParentEscrowHex = "0x7000000000000000000000000000000000000007"
)

func openBiddingTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newBiddingService(t *testing.T, rebidCooldownSeconds int64) (*Service, *storage.DB, *chain.MockClient) {
	t.Helper()
	db := openBiddingTestDB(t)
	mock := chain.NewMockClient()
	cfg := &config.Config{
		FactoryAddress:       testFactoryAddr,
		ChainID:              84532,
		RebidCooldownSeconds: rebidCooldownSeconds,
	}
	idx := indexer.New(db, mock, cfg.FactoryAddress, indexer.WithStartBlock(0))
	return &Service{
		DB:    db,
		Chain: mock,
		Idx:   idx,
		Cfg:   cfg,
	}, db, mock
}

func createParentEscrow(t *testing.T, db *storage.DB, activeWorker string) *storage.Escrow {
	t.Helper()
	ctx := context.Background()
	task, err := db.CreateTask(ctx, "parent task", "", "0xparent")
	if err != nil {
		t.Fatalf("create parent task: %v", err)
	}
	parent, err := db.CreateEscrow(ctx, &storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  84532,
		FactoryAddress:           testFactoryAddr,
		EscrowAddress:            testParentEscrowHex,
		EscrowID:                 1,
		Buyer:                    testBuyerAddr,
		Worker:                   testWorkerAddr,
		Verifier:                 testVerifierAddr,
		Arbitrator:               testArbitratorAddr,
		Amount:                   "100",
		WorkerStake:              "0",
		Token:                    "",
		Status:                   "created",
		SubmissionDeadline:       time.Now().Add(24 * time.Hour).Unix(),
		ReviewPeriodSeconds:      60,
		DisputePeriodSeconds:     60,
		ArbitratorTimeoutSeconds: 60,
		ActiveWorker:             activeWorker,
	})
	if err != nil {
		t.Fatalf("create parent escrow: %v", err)
	}
	return parent
}

func TestCreateRFQ_ParentCooldownEnforced(t *testing.T) {
	svc, db, _ := newBiddingService(t, 120)
	ctx := context.Background()
	parent := createParentEscrow(t, db, testBuyerAddr)

	now := time.Now().Unix()
	params := CreateRFQParams{
		Title:                    "rfq-parent-1",
		Description:              "desc",
		Buyer:                    testBuyerAddr,
		Token:                    "",
		BudgetMin:                "10",
		BudgetMax:                "20",
		Deadline:                 now + 3600,
		ReviewPeriodSeconds:      60,
		DisputePeriodSeconds:     60,
		ArbitratorTimeoutSeconds: 60,
		Verifier:                 testVerifierAddr,
		Arbitrator:               testArbitratorAddr,
		WorkerStake:              "0",
		MilestonesJSON:           "[]",
		RequirementsJSON:         "{}",
		RequiredCredentialsJSON:  "[]",
		ServiceTier:              0,
		CommitDeadline:           now + 600,
		RevealDeadline:           now + 1200,
		ExpiresAt:                now + 1800,
		ParentEscrowID:           &parent.ID,
	}
	if _, err := svc.CreateRFQ(ctx, params); err != nil {
		t.Fatalf("create first rfq: %v", err)
	}

	params.Title = "rfq-parent-2"
	_, err := svc.CreateRFQ(ctx, params)
	if err == nil {
		t.Fatal("expected cooldown error on immediate re-bid")
	}
	var cooldownErr *RebidCooldownError
	if !errors.As(err, &cooldownErr) {
		t.Fatalf("expected RebidCooldownError, got: %v", err)
	}
	if cooldownErr.RetryAfterSeconds() <= 0 {
		t.Fatalf("expected positive retry delay, got %d", cooldownErr.RetryAfterSeconds())
	}
}

func TestCreateRFQ_ParentCooldownDisabled(t *testing.T) {
	svc, db, _ := newBiddingService(t, 0)
	ctx := context.Background()
	parent := createParentEscrow(t, db, testBuyerAddr)

	now := time.Now().Unix()
	params := CreateRFQParams{
		Title:                    "rfq-parent-disabled-1",
		Description:              "desc",
		Buyer:                    testBuyerAddr,
		Token:                    "",
		BudgetMin:                "10",
		BudgetMax:                "20",
		Deadline:                 now + 3600,
		ReviewPeriodSeconds:      60,
		DisputePeriodSeconds:     60,
		ArbitratorTimeoutSeconds: 60,
		Verifier:                 testVerifierAddr,
		Arbitrator:               testArbitratorAddr,
		WorkerStake:              "0",
		MilestonesJSON:           "[]",
		RequirementsJSON:         "{}",
		RequiredCredentialsJSON:  "[]",
		ServiceTier:              0,
		CommitDeadline:           now + 600,
		RevealDeadline:           now + 1200,
		ExpiresAt:                now + 1800,
		ParentEscrowID:           &parent.ID,
	}
	if _, err := svc.CreateRFQ(ctx, params); err != nil {
		t.Fatalf("create first rfq: %v", err)
	}
	params.Title = "rfq-parent-disabled-2"
	if _, err := svc.CreateRFQ(ctx, params); err != nil {
		t.Fatalf("create second rfq with cooldown disabled: %v", err)
	}
}

func TestAcceptBid_ForwardsParentEscrowAddressToChain(t *testing.T) {
	svc, db, mock := newBiddingService(t, 0)
	ctx := context.Background()
	parent := createParentEscrow(t, db, testBuyerAddr)

	now := time.Now().Unix()
	rfq, err := db.CreateRFQ(ctx, &storage.RFQ{
		Title:                    "rfq-accept",
		Description:              "desc",
		SpecHash:                 "0xabc",
		Buyer:                    testBuyerAddr,
		Token:                    "",
		BudgetMin:                "100",
		BudgetMax:                "300",
		Deadline:                 now + 3600,
		ReviewPeriodSeconds:      60,
		DisputePeriodSeconds:     60,
		ArbitratorTimeoutSeconds: 60,
		Verifier:                 testVerifierAddr,
		Arbitrator:               testArbitratorAddr,
		WorkerStake:              "0",
		MilestonesJSON:           "[]",
		RequirementsJSON:         "{}",
		RequiredCredentialsJSON:  "[]",
		BiddingMode:              "sealed",
		CommitDeadline:           now - 600,
		RevealDeadline:           now - 300,
		ServiceTier:              0,
		ParentEscrowID:           &parent.ID,
		Status:                   "open",
		ExpiresAt:                now + 7200,
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	bid, err := db.CreateBid(ctx, &storage.Bid{
		RFQID:              rfq.ID,
		Bidder:             testWorkerAltAddr,
		Amount:             "200",
		EstimatedDuration:  3600,
		ReputationBond:     "0",
		MilestonesJSON:     "[]",
		Message:            "",
		Status:             "pending",
		ExpiresAt:          now + 1200,
		CredentialsJSON:    "[]",
		CredentialVerified: false,
	})
	if err != nil {
		t.Fatalf("create bid: %v", err)
	}

	_, err = db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:         rfq.ID,
		Bidder:        bid.Bidder,
		Commitment:    "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Nonce:         "nonce-1",
		Status:        "revealed",
		RevealedBidID: &bid.ID,
	})
	if err != nil {
		t.Fatalf("create revealed bid commit: %v", err)
	}

	mock.Receipt = chain.MakeEscrowCreatedReceipt(
		42,
		common.HexToAddress("0x9000000000000000000000000000000000000009"),
		common.HexToAddress(testBuyerAddr),
	)

	result, err := svc.AcceptBid(ctx, AcceptBidParams{
		RFQID:  rfq.ID,
		BidID:  bid.ID,
		Caller: testBuyerAddr,
	})
	if err != nil {
		t.Fatalf("accept bid: %v", err)
	}
	if result == nil || result.Escrow == nil {
		t.Fatal("expected accept bid result with escrow")
	}
	if mock.LastCreateEscrowParams == nil {
		t.Fatal("expected mock to capture createEscrow params")
	}
	if mock.LastCreateEscrowParams.ParentEscrow != common.HexToAddress(testParentEscrowHex) {
		t.Fatalf(
			"expected parent escrow %s, got %s",
			testParentEscrowHex,
			mock.LastCreateEscrowParams.ParentEscrow.Hex(),
		)
	}
}
