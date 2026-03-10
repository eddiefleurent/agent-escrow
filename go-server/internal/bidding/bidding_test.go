package bidding

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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

func openBiddingFileTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "bidding.db"))
	if err != nil {
		t.Fatalf("open file-backed test db: %v", err)
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

func createSealedRFQForBiddingTests(t *testing.T, db *storage.DB, now int64) *storage.RFQ {
	t.Helper()
	rfq, err := db.CreateRFQ(context.Background(), &storage.RFQ{
		Title:                    "sealed-rfq",
		Description:              "desc",
		SpecHash:                 "0xabc",
		Buyer:                    testBuyerAddr,
		Token:                    "",
		BudgetMin:                "100",
		BudgetMax:                "500",
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
		CommitDeadline:           now + 600,
		RevealDeadline:           now + 1200,
		ServiceTier:              0,
		Status:                   "open",
		ExpiresAt:                now + 1800,
	})
	if err != nil {
		t.Fatalf("create sealed rfq: %v", err)
	}
	return rfq
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

func TestCommitBid_ReplacesPriorCommittedBid(t *testing.T) {
	svc, db, _ := newBiddingService(t, 0)
	ctx := context.Background()
	now := time.Now().Unix()
	rfq := createSealedRFQForBiddingTests(t, db, now)

	first, err := svc.CommitBid(ctx, CommitBidParams{
		RFQID:      rfq.ID,
		Bidder:     testWorkerAddr,
		Commitment: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Nonce:      "nonce-1",
	})
	if err != nil {
		t.Fatalf("commit first bid: %v", err)
	}
	second, err := svc.CommitBid(ctx, CommitBidParams{
		RFQID:      rfq.ID,
		Bidder:     testWorkerAddr,
		Commitment: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Nonce:      "nonce-2",
	})
	if err != nil {
		t.Fatalf("commit replacement bid: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("expected replacement commit to create a new record")
	}

	updatedFirst, err := db.GetBidCommit(ctx, first.ID)
	if err != nil {
		t.Fatalf("get first commit: %v", err)
	}
	if updatedFirst.Status != "superseded" {
		t.Fatalf("expected first commit to be superseded, got %q", updatedFirst.Status)
	}

	activeCount, err := db.CountActiveBidCommitsByRFQBidder(ctx, rfq.ID, common.HexToAddress(testWorkerAddr).Hex())
	if err != nil {
		t.Fatalf("count active commits: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("expected 1 active commit after replacement, got %d", activeCount)
	}
}

func TestCommitBid_NonRevealCooldownEnforced(t *testing.T) {
	svc, db, _ := newBiddingService(t, 0)
	ctx := context.Background()
	now := time.Now().Unix()
	rfq := createSealedRFQForBiddingTests(t, db, now)

	if err := db.UpsertSealedBidderDiscipline(
		ctx,
		common.HexToAddress(testWorkerAddr).Hex(),
		now+600,
	); err != nil {
		t.Fatalf("seed sealed bidder discipline: %v", err)
	}

	_, err := svc.CommitBid(ctx, CommitBidParams{
		RFQID:      rfq.ID,
		Bidder:     testWorkerAddr,
		Commitment: "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		Nonce:      "nonce-new",
	})
	if err == nil {
		t.Fatal("expected cooldown error")
	}
	var cooldownErr *SealedBidCooldownError
	if !errors.As(err, &cooldownErr) {
		t.Fatalf("expected SealedBidCooldownError, got %v", err)
	}
}

func TestFinalizeSealedBidding_SelectsDeterministicBestBid(t *testing.T) {
	svc, db, _ := newBiddingService(t, 0)
	ctx := context.Background()
	now := time.Now().Unix()

	rfq, err := db.CreateRFQ(ctx, &storage.RFQ{
		Title:                    "rfq-finalize",
		Description:              "desc",
		SpecHash:                 "0xabc",
		Buyer:                    testBuyerAddr,
		Token:                    "",
		BudgetMin:                "100",
		BudgetMax:                "500",
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
		CommitDeadline:           now - 1200,
		RevealDeadline:           now - 600,
		ServiceTier:              0,
		Status:                   "open",
		ExpiresAt:                now + 1800,
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	createRevealedBid := func(bidder, amount string, duration int64, nonce string) *storage.Bid {
		t.Helper()
		bid, createErr := db.CreateBid(ctx, &storage.Bid{
			RFQID:              rfq.ID,
			Bidder:             bidder,
			Amount:             amount,
			EstimatedDuration:  duration,
			ReputationBond:     "0",
			MilestonesJSON:     "[]",
			Message:            "",
			Status:             "pending",
			ExpiresAt:          now + 1200,
			CredentialsJSON:    "[]",
			CredentialVerified: true,
		})
		if createErr != nil {
			t.Fatalf("create bid %s: %v", nonce, createErr)
		}
		_, createErr = db.CreateBidCommit(ctx, &storage.BidCommit{
			RFQID:         rfq.ID,
			Bidder:        bidder,
			Commitment:    fmt.Sprintf("0x%064x", len(nonce)),
			Nonce:         nonce,
			Status:        "revealed",
			RevealedBidID: &bid.ID,
		})
		if createErr != nil {
			t.Fatalf("create commit %s: %v", nonce, createErr)
		}
		return bid
	}

	slower := createRevealedBid(testWorkerAddr, "200", 7200, "a1")
	faster := createRevealedBid(testWorkerAltAddr, "200", 3600, "b2")
	_, err = db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:      rfq.ID,
		Bidder:     common.HexToAddress("0x8000000000000000000000000000000000000008").Hex(),
		Commitment: "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		Nonce:      "hidden",
		Status:     "committed",
	})
	if err != nil {
		t.Fatalf("create unrevealed commit: %v", err)
	}

	summary, err := svc.FinalizeSealedBidding(ctx, rfq.ID)
	if err != nil {
		t.Fatalf("finalize sealed bidding: %v", err)
	}
	if summary.BestBidID == nil || *summary.BestBidID != faster.ID {
		t.Fatalf("expected faster equal-price bid %d to win, got %+v", faster.ID, summary.BestBidID)
	}
	if len(summary.EligibleBidIDs) != 2 {
		t.Fatalf("expected 2 eligible bids, got %d", len(summary.EligibleBidIDs))
	}

	updatedRFQ, err := db.GetRFQ(ctx, rfq.ID)
	if err != nil {
		t.Fatalf("get rfq after finalize: %v", err)
	}
	if updatedRFQ.SealedBidStatus != "finalized" {
		t.Fatalf("expected rfq sealed bid status finalized, got %q", updatedRFQ.SealedBidStatus)
	}
	if updatedRFQ.BestBidID == nil || *updatedRFQ.BestBidID != faster.ID {
		t.Fatalf("expected rfq best bid %d, got %+v", faster.ID, updatedRFQ.BestBidID)
	}
	if updatedRFQ.SealedBidSelectionRule == "" {
		t.Fatal("expected sealed bid selection rule to be recorded")
	}

	hidden, err := db.GetBidCommitByRFQBidderNonce(ctx, rfq.ID, common.HexToAddress("0x8000000000000000000000000000000000000008").Hex(), "hidden")
	if err != nil {
		t.Fatalf("get hidden commit: %v", err)
	}
	if hidden.Status != "expired" {
		t.Fatalf("expected hidden commit to expire, got %q", hidden.Status)
	}

	if slower.ID == faster.ID {
		t.Fatal("expected distinct bids in test fixture")
	}
}

func TestFinalizeSealedBidding_IsIdempotentAfterInitialFinalization(t *testing.T) {
	svc, db, _ := newBiddingService(t, 0)
	ctx := context.Background()
	now := time.Now().Unix()

	rfq, err := db.CreateRFQ(ctx, &storage.RFQ{
		Title:                    "rfq-finalize-idempotent",
		Description:              "desc",
		SpecHash:                 "0xabc",
		Buyer:                    testBuyerAddr,
		Token:                    "",
		BudgetMin:                "100",
		BudgetMax:                "500",
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
		CommitDeadline:           now - 1200,
		RevealDeadline:           now - 600,
		ServiceTier:              0,
		Status:                   "open",
		ExpiresAt:                now + 1800,
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	createRevealedBid := func(bidder, amount string, duration int64, nonce string, expiresAt int64) *storage.Bid {
		t.Helper()
		bid, createErr := db.CreateBid(ctx, &storage.Bid{
			RFQID:              rfq.ID,
			Bidder:             bidder,
			Amount:             amount,
			EstimatedDuration:  duration,
			ReputationBond:     "0",
			MilestonesJSON:     "[]",
			Message:            "",
			Status:             "pending",
			ExpiresAt:          expiresAt,
			CredentialsJSON:    "[]",
			CredentialVerified: true,
		})
		if createErr != nil {
			t.Fatalf("create bid %s: %v", nonce, createErr)
		}
		_, createErr = db.CreateBidCommit(ctx, &storage.BidCommit{
			RFQID:         rfq.ID,
			Bidder:        bidder,
			Commitment:    fmt.Sprintf("0x%064x", len(nonce)),
			Nonce:         nonce,
			Status:        "revealed",
			RevealedBidID: &bid.ID,
		})
		if createErr != nil {
			t.Fatalf("create commit %s: %v", nonce, createErr)
		}
		return bid
	}

	winningBid := createRevealedBid(testWorkerAddr, "200", 1800, "winner", now+300)
	fallbackBid := createRevealedBid(testWorkerAltAddr, "210", 1200, "fallback", now+1200)

	firstSummary, err := svc.FinalizeSealedBidding(ctx, rfq.ID)
	if err != nil {
		t.Fatalf("first finalize sealed bidding: %v", err)
	}
	if firstSummary.BestBidID == nil || *firstSummary.BestBidID != winningBid.ID {
		t.Fatalf("expected initial best bid %d, got %+v", winningBid.ID, firstSummary.BestBidID)
	}

	if _, err := db.SQLDB().ExecContext(ctx, "UPDATE bids SET expires_at = ? WHERE id = ?", now-1, winningBid.ID); err != nil {
		t.Fatalf("expire winning bid after finalize: %v", err)
	}

	secondSummary, err := svc.FinalizeSealedBidding(ctx, rfq.ID)
	if err != nil {
		t.Fatalf("second finalize sealed bidding: %v", err)
	}
	if secondSummary.BestBidID == nil || *secondSummary.BestBidID != winningBid.ID {
		t.Fatalf("expected persisted best bid %d, got %+v", winningBid.ID, secondSummary.BestBidID)
	}
	if len(secondSummary.EligibleBidIDs) != 0 {
		t.Fatalf("expected idempotent summary to avoid recomputing eligible bids, got %d", len(secondSummary.EligibleBidIDs))
	}

	updatedRFQ, err := db.GetRFQ(ctx, rfq.ID)
	if err != nil {
		t.Fatalf("get rfq after second finalize: %v", err)
	}
	if updatedRFQ.BestBidID == nil || *updatedRFQ.BestBidID != winningBid.ID {
		t.Fatalf("expected persisted best bid to remain %d, got %+v", winningBid.ID, updatedRFQ.BestBidID)
	}
	if updatedRFQ.BestBidID != nil && *updatedRFQ.BestBidID == fallbackBid.ID {
		t.Fatalf("expected fallback bid %d not to replace persisted winner", fallbackBid.ID)
	}
}

func TestFinalizeSealedBidding_NoValidReveals(t *testing.T) {
	svc, db, _ := newBiddingService(t, 0)
	ctx := context.Background()
	now := time.Now().Unix()

	rfq, err := db.CreateRFQ(ctx, &storage.RFQ{
		Title:                    "rfq-no-reveals",
		Description:              "desc",
		SpecHash:                 "0xabc",
		Buyer:                    testBuyerAddr,
		Token:                    "",
		BudgetMin:                "100",
		BudgetMax:                "500",
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
		CommitDeadline:           now - 1200,
		RevealDeadline:           now - 600,
		ServiceTier:              0,
		Status:                   "open",
		ExpiresAt:                now + 1800,
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	_, err = db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:      rfq.ID,
		Bidder:     common.HexToAddress(testWorkerAddr).Hex(),
		Commitment: "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		Nonce:      "never-revealed",
		Status:     "committed",
	})
	if err != nil {
		t.Fatalf("create hidden commit: %v", err)
	}

	summary, err := svc.FinalizeSealedBidding(ctx, rfq.ID)
	if err != nil {
		t.Fatalf("finalize sealed bidding: %v", err)
	}
	if summary.BestBidID != nil {
		t.Fatalf("expected no best bid, got %+v", summary.BestBidID)
	}
	if len(summary.EligibleBidIDs) != 0 {
		t.Fatalf("expected no eligible bids, got %d", len(summary.EligibleBidIDs))
	}

	updatedRFQ, err := db.GetRFQ(ctx, rfq.ID)
	if err != nil {
		t.Fatalf("get rfq after finalize: %v", err)
	}
	if updatedRFQ.SealedBidStatus != "no_valid_reveals" {
		t.Fatalf("expected no_valid_reveals status, got %q", updatedRFQ.SealedBidStatus)
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

func TestAcceptBid_AllowsPersistedBestBidAfterPriorSealedFinalization(t *testing.T) {
	svc, db, mock := newBiddingService(t, 0)
	ctx := context.Background()
	now := time.Now().Unix()

	rfq, err := db.CreateRFQ(ctx, &storage.RFQ{
		Title:                    "rfq-accept-persisted-best",
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
		Status:                   "open",
		ExpiresAt:                now + 7200,
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	winningBid, err := db.CreateBid(ctx, &storage.Bid{
		RFQID:              rfq.ID,
		Bidder:             testWorkerAddr,
		Amount:             "200",
		EstimatedDuration:  1800,
		ReputationBond:     "0",
		MilestonesJSON:     "[]",
		Message:            "",
		Status:             "pending",
		ExpiresAt:          now + 1200,
		CredentialsJSON:    "[]",
		CredentialVerified: false,
	})
	if err != nil {
		t.Fatalf("create winning bid: %v", err)
	}
	_, err = db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:         rfq.ID,
		Bidder:        winningBid.Bidder,
		Commitment:    "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Nonce:         "winner",
		Status:        "revealed",
		RevealedBidID: &winningBid.ID,
	})
	if err != nil {
		t.Fatalf("create winning bid commit: %v", err)
	}

	losingBid, err := db.CreateBid(ctx, &storage.Bid{
		RFQID:              rfq.ID,
		Bidder:             testWorkerAltAddr,
		Amount:             "220",
		EstimatedDuration:  2400,
		ReputationBond:     "0",
		MilestonesJSON:     "[]",
		Message:            "",
		Status:             "pending",
		ExpiresAt:          now + 1200,
		CredentialsJSON:    "[]",
		CredentialVerified: false,
	})
	if err != nil {
		t.Fatalf("create losing bid: %v", err)
	}
	_, err = db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:         rfq.ID,
		Bidder:        losingBid.Bidder,
		Commitment:    "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Nonce:         "loser",
		Status:        "revealed",
		RevealedBidID: &losingBid.ID,
	})
	if err != nil {
		t.Fatalf("create losing bid commit: %v", err)
	}

	firstSummary, err := svc.FinalizeSealedBidding(ctx, rfq.ID)
	if err != nil {
		t.Fatalf("finalize sealed bidding: %v", err)
	}
	if firstSummary.BestBidID == nil || *firstSummary.BestBidID != winningBid.ID {
		t.Fatalf("expected persisted best bid %d, got %+v", winningBid.ID, firstSummary.BestBidID)
	}

	mock.Receipt = chain.MakeEscrowCreatedReceipt(
		42,
		common.HexToAddress("0x9000000000000000000000000000000000000009"),
		common.HexToAddress(testBuyerAddr),
	)

	result, err := svc.AcceptBid(ctx, AcceptBidParams{
		RFQID:  rfq.ID,
		BidID:  winningBid.ID,
		Caller: testBuyerAddr,
	})
	if err != nil {
		t.Fatalf("accept persisted best bid: %v", err)
	}
	if result == nil || result.Escrow == nil {
		t.Fatal("expected accept bid result with escrow")
	}
}

func TestAcceptBid_RejectsLosingEligibleBidOnInitialSealedFinalization(t *testing.T) {
	svc, db, _ := newBiddingService(t, 0)
	ctx := context.Background()
	now := time.Now().Unix()

	rfq, err := db.CreateRFQ(ctx, &storage.RFQ{
		Title:                    "rfq-accept-initial-finalization-winner-only",
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
		Status:                   "open",
		ExpiresAt:                now + 7200,
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	winningBid, err := db.CreateBid(ctx, &storage.Bid{
		RFQID:              rfq.ID,
		Bidder:             testWorkerAddr,
		Amount:             "200",
		EstimatedDuration:  1800,
		ReputationBond:     "0",
		MilestonesJSON:     "[]",
		Message:            "",
		Status:             "pending",
		ExpiresAt:          now + 1200,
		CredentialsJSON:    "[]",
		CredentialVerified: false,
	})
	if err != nil {
		t.Fatalf("create winning bid: %v", err)
	}
	_, err = db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:         rfq.ID,
		Bidder:        winningBid.Bidder,
		Commitment:    "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Nonce:         "winner",
		Status:        "revealed",
		RevealedBidID: &winningBid.ID,
	})
	if err != nil {
		t.Fatalf("create winning bid commit: %v", err)
	}

	losingBid, err := db.CreateBid(ctx, &storage.Bid{
		RFQID:              rfq.ID,
		Bidder:             testWorkerAltAddr,
		Amount:             "200",
		EstimatedDuration:  2400,
		ReputationBond:     "0",
		MilestonesJSON:     "[]",
		Message:            "",
		Status:             "pending",
		ExpiresAt:          now + 1200,
		CredentialsJSON:    "[]",
		CredentialVerified: false,
	})
	if err != nil {
		t.Fatalf("create losing bid: %v", err)
	}
	_, err = db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:         rfq.ID,
		Bidder:        losingBid.Bidder,
		Commitment:    "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Nonce:         "loser",
		Status:        "revealed",
		RevealedBidID: &losingBid.ID,
	})
	if err != nil {
		t.Fatalf("create losing bid commit: %v", err)
	}

	_, err = svc.AcceptBid(ctx, AcceptBidParams{
		RFQID:  rfq.ID,
		BidID:  losingBid.ID,
		Caller: testBuyerAddr,
	})
	if err == nil {
		t.Fatal("expected losing eligible bid to be rejected")
	}
	if err.Error() != "bid is not the selected best bid for this sealed RFQ" {
		t.Fatalf("expected best-bid rejection, got %v", err)
	}

	updatedRFQ, err := db.GetRFQ(ctx, rfq.ID)
	if err != nil {
		t.Fatalf("get updated rfq: %v", err)
	}
	if updatedRFQ.SealedBidStatus != "finalized" {
		t.Fatalf("expected finalized sealed bid status, got %q", updatedRFQ.SealedBidStatus)
	}
	if updatedRFQ.BestBidID == nil || *updatedRFQ.BestBidID != winningBid.ID {
		t.Fatalf("expected persisted best bid %d, got %+v", winningBid.ID, updatedRFQ.BestBidID)
	}
}

func TestCommitBid_SupersedesPriorCommittedBidForBidder(t *testing.T) {
	svc, db, _ := newBiddingService(t, 0)
	ctx := context.Background()
	now := time.Now().Unix()

	rfq, err := db.CreateRFQ(ctx, &storage.RFQ{
		Title:                    "rfq-supersede",
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
		CommitDeadline:           now + 600,
		RevealDeadline:           now + 1200,
		ServiceTier:              0,
		Status:                   "open",
		ExpiresAt:                now + 1800,
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	first, err := svc.CommitBid(ctx, CommitBidParams{
		RFQID:      rfq.ID,
		Bidder:     testWorkerAddr,
		Commitment: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Nonce:      "nonce-1",
	})
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}

	second, err := svc.CommitBid(ctx, CommitBidParams{
		RFQID:      rfq.ID,
		Bidder:     testWorkerAddr,
		Commitment: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Nonce:      "nonce-2",
	})
	if err != nil {
		t.Fatalf("second commit: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("expected a replacement commit, got the same id %d", second.ID)
	}

	updatedFirst, err := db.GetBidCommit(ctx, first.ID)
	if err != nil {
		t.Fatalf("get first commit: %v", err)
	}
	if updatedFirst.Status != "superseded" {
		t.Fatalf("expected first commit status superseded, got %q", updatedFirst.Status)
	}

	activeCount, err := db.CountActiveBidCommitsByRFQBidder(ctx, rfq.ID, common.HexToAddress(testWorkerAddr).Hex())
	if err != nil {
		t.Fatalf("count active commits: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("expected exactly one active commit, got %d", activeCount)
	}
}

func TestFinalizeSealedBidding_ExpiresUnrevealedAndSelectsBestBid(t *testing.T) {
	svc, db, _ := newBiddingService(t, 0)
	ctx := context.Background()
	now := time.Now().Unix()

	rfq, err := db.CreateRFQ(ctx, &storage.RFQ{
		Title:                    "rfq-finalize",
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
		RevealDeadline:           now - 60,
		ServiceTier:              0,
		Status:                   "open",
		ExpiresAt:                now + 1800,
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	unrevealed, err := db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:      rfq.ID,
		Bidder:     testWorkerAddr,
		Commitment: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Nonce:      "nonce-unrevealed",
		Status:     "committed",
	})
	if err != nil {
		t.Fatalf("create unrevealed commit: %v", err)
	}

	bidA, err := db.CreateBid(ctx, &storage.Bid{
		RFQID:              rfq.ID,
		Bidder:             testWorkerAltAddr,
		Amount:             "200",
		EstimatedDuration:  40,
		ReputationBond:     "0",
		MilestonesJSON:     "[]",
		Message:            "",
		Status:             "pending",
		ExpiresAt:          now + 1200,
		CredentialsJSON:    "[]",
		CredentialVerified: true,
	})
	if err != nil {
		t.Fatalf("create bidA: %v", err)
	}
	_, err = db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:         rfq.ID,
		Bidder:        bidA.Bidder,
		Commitment:    "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Nonce:         "nonce-a",
		Status:        "revealed",
		RevealedBidID: &bidA.ID,
	})
	if err != nil {
		t.Fatalf("create commitA: %v", err)
	}

	bidB, err := db.CreateBid(ctx, &storage.Bid{
		RFQID:              rfq.ID,
		Bidder:             "0x8000000000000000000000000000000000000008",
		Amount:             "200",
		EstimatedDuration:  20,
		ReputationBond:     "0",
		MilestonesJSON:     "[]",
		Message:            "",
		Status:             "pending",
		ExpiresAt:          now + 1200,
		CredentialsJSON:    "[]",
		CredentialVerified: true,
	})
	if err != nil {
		t.Fatalf("create bidB: %v", err)
	}
	_, err = db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:         rfq.ID,
		Bidder:        bidB.Bidder,
		Commitment:    "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Nonce:         "nonce-b",
		Status:        "revealed",
		RevealedBidID: &bidB.ID,
	})
	if err != nil {
		t.Fatalf("create commitB: %v", err)
	}

	summary, err := svc.FinalizeSealedBidding(ctx, rfq.ID)
	if err != nil {
		t.Fatalf("finalize sealed bidding: %v", err)
	}
	if summary == nil {
		t.Fatal("expected finalization summary")
	}
	if summary.BestBidID == nil || *summary.BestBidID != bidB.ID {
		t.Fatalf("expected best bid %d, got %+v", bidB.ID, summary.BestBidID)
	}
	if summary.SealedBidStatus != "finalized" {
		t.Fatalf("expected sealed_bid_status finalized, got %q", summary.SealedBidStatus)
	}

	updatedRFQ, err := db.GetRFQ(ctx, rfq.ID)
	if err != nil {
		t.Fatalf("get updated rfq: %v", err)
	}
	if updatedRFQ.SealedBidStatus != "finalized" {
		t.Fatalf("expected persisted sealed_bid_status finalized, got %q", updatedRFQ.SealedBidStatus)
	}
	if updatedRFQ.SealedBidSelectionRule == "" {
		t.Fatal("expected sealed_bid_selection_rule to be populated")
	}
	if updatedRFQ.BestBidID == nil || *updatedRFQ.BestBidID != bidB.ID {
		t.Fatalf("expected persisted best bid %d, got %+v", bidB.ID, updatedRFQ.BestBidID)
	}
	if updatedRFQ.SealedBidFinalizedAt == 0 {
		t.Fatal("expected sealed_bid_finalized_at to be populated")
	}

	updatedCommit, err := db.GetBidCommit(ctx, unrevealed.ID)
	if err != nil {
		t.Fatalf("get updated unrevealed commit: %v", err)
	}
	if updatedCommit.Status != "expired" {
		t.Fatalf("expected unrevealed commit expired, got %q", updatedCommit.Status)
	}
}

func TestFinalizeSealedBidding_AppliesCooldownToNonRevealBidder(t *testing.T) {
	svc, db, _ := newBiddingService(t, 0)
	ctx := context.Background()
	now := time.Now().Unix()

	rfqExpired, err := db.CreateRFQ(ctx, &storage.RFQ{
		Title:                    "rfq-expired",
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
		RevealDeadline:           now - 60,
		ServiceTier:              0,
		Status:                   "open",
		ExpiresAt:                now + 1800,
	})
	if err != nil {
		t.Fatalf("create expired rfq: %v", err)
	}
	_, err = db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:      rfqExpired.ID,
		Bidder:     testWorkerAddr,
		Commitment: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Nonce:      "nonce-expired",
		Status:     "committed",
	})
	if err != nil {
		t.Fatalf("create non-reveal commit: %v", err)
	}

	if _, err := svc.FinalizeSealedBidding(ctx, rfqExpired.ID); err != nil {
		t.Fatalf("finalize sealed bidding: %v", err)
	}

	rfqOpen, err := db.CreateRFQ(ctx, &storage.RFQ{
		Title:                    "rfq-open",
		Description:              "desc",
		SpecHash:                 "0xabc",
		Buyer:                    testBuyerAddr,
		Token:                    "",
		BudgetMin:                "100",
		BudgetMax:                "300",
		Deadline:                 now + 7200,
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
		CommitDeadline:           now + 600,
		RevealDeadline:           now + 1200,
		ServiceTier:              0,
		Status:                   "open",
		ExpiresAt:                now + 3600,
	})
	if err != nil {
		t.Fatalf("create open rfq: %v", err)
	}

	_, err = svc.CommitBid(ctx, CommitBidParams{
		RFQID:      rfqOpen.ID,
		Bidder:     testWorkerAddr,
		Commitment: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Nonce:      "nonce-new",
	})
	if err == nil {
		t.Fatal("expected sealed-bid cooldown rejection")
	}
	var cooldownErr *SealedBidCooldownError
	if !errors.As(err, &cooldownErr) {
		t.Fatalf("expected SealedBidCooldownError, got %v", err)
	}

	discipline, err := db.GetSealedBidderDiscipline(ctx, common.HexToAddress(testWorkerAddr).Hex())
	if err != nil {
		t.Fatalf("get bidder discipline: %v", err)
	}
	if discipline.NonRevealCount != 1 {
		t.Fatalf("expected non_reveal_count 1, got %d", discipline.NonRevealCount)
	}
	if discipline.CooldownUntil <= now {
		t.Fatalf("expected cooldown_until in the future, got %d", discipline.CooldownUntil)
	}
}

func TestFinalizeSealedBidding_ImmediateTxBlocksLateReveal(t *testing.T) {
	db := openBiddingFileTestDB(t)
	mock := chain.NewMockClient()
	svc := &Service{
		DB:    db,
		Chain: mock,
		Idx:   indexer.New(db, mock, testFactoryAddr, indexer.WithStartBlock(0)),
		Cfg: &config.Config{
			FactoryAddress: testFactoryAddr,
			ChainID:        84532,
		},
	}

	ctx := context.Background()
	now := time.Now().Unix()
	rfq, err := db.CreateRFQ(ctx, &storage.RFQ{
		Title:                    "rfq-finalize-lock",
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
		RevealDeadline:           now - 60,
		ServiceTier:              0,
		Status:                   "open",
		ExpiresAt:                now + 1800,
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	bestBid, err := db.CreateBid(ctx, &storage.Bid{
		RFQID:              rfq.ID,
		Bidder:             testWorkerAltAddr,
		Amount:             "150",
		EstimatedDuration:  20,
		ReputationBond:     "0",
		MilestonesJSON:     "[]",
		Message:            "",
		Status:             "pending",
		ExpiresAt:          now + 1200,
		CredentialsJSON:    "[]",
		CredentialVerified: true,
	})
	if err != nil {
		t.Fatalf("create best bid: %v", err)
	}
	if _, err := db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:         rfq.ID,
		Bidder:        bestBid.Bidder,
		Commitment:    "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Nonce:         "winner",
		Status:        "revealed",
		RevealedBidID: &bestBid.ID,
	}); err != nil {
		t.Fatalf("create winning commit: %v", err)
	}

	lateCommit, err := db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:      rfq.ID,
		Bidder:     common.HexToAddress("0x8000000000000000000000000000000000000008").Hex(),
		Commitment: "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Nonce:      "late",
		Status:     "committed",
	})
	if err != nil {
		t.Fatalf("create late commit: %v", err)
	}

	immediateTx, err := db.BeginImmediateTx(ctx)
	if err != nil {
		t.Fatalf("begin immediate tx: %v", err)
	}
	defer immediateTx.Rollback(ctx)

	currentRFQ, err := immediateTx.GetRFQ(ctx, rfq.ID)
	if err != nil {
		t.Fatalf("get rfq in immediate tx: %v", err)
	}
	candidates, err := svc.eligibleSealedBidCandidatesOn(ctx, immediateTx, currentRFQ, time.Unix(now, 0).UTC())
	if err != nil {
		t.Fatalf("eligible candidates in immediate tx: %v", err)
	}
	if len(candidates) != 1 || candidates[0].bid.ID != bestBid.ID {
		t.Fatalf("expected only pre-existing revealed bid %d, got %+v", bestBid.ID, candidates)
	}

	revealResult := make(chan error, 1)
	go func() {
		tx, txErr := db.BeginTx(ctx)
		if txErr != nil {
			revealResult <- txErr
			return
		}
		bid, createErr := db.CreateBidTx(ctx, tx, &storage.Bid{
			RFQID:              rfq.ID,
			Bidder:             lateCommit.Bidder,
			Amount:             "140",
			EstimatedDuration:  10,
			ReputationBond:     "0",
			MilestonesJSON:     "[]",
			Message:            "",
			Status:             "pending",
			ExpiresAt:          now + 1200,
			CredentialsJSON:    "[]",
			CredentialVerified: true,
		})
		if createErr != nil {
			tx.Rollback()
			revealResult <- createErr
			return
		}
		if updateErr := db.UpdateBidCommitRevealTx(ctx, tx, lateCommit.ID, bid.ID); updateErr != nil {
			tx.Rollback()
			revealResult <- updateErr
			return
		}
		revealResult <- tx.Commit()
	}()

	var revealErr error
	time.Sleep(100 * time.Millisecond)
	select {
	case revealErr = <-revealResult:
	default:
	}

	committed, err := immediateTx.ListCommittedBidCommitsByRFQ(ctx, rfq.ID)
	if err != nil {
		t.Fatalf("list committed commits in immediate tx: %v", err)
	}
	if len(committed) != 1 || committed[0].ID != lateCommit.ID {
		t.Fatalf("expected only late committed bid in tx, got %+v", committed)
	}
	if err := immediateTx.ExpireCommittedBidCommits(ctx, rfq.ID); err != nil {
		t.Fatalf("expire committed bids in immediate tx: %v", err)
	}
	if err := immediateTx.UpdateRFQSealedBiddingState(
		ctx,
		rfq.ID,
		"finalized",
		sealedBidSelectionRule,
		&bestBid.ID,
		now,
	); err != nil {
		t.Fatalf("update sealed bidding state in immediate tx: %v", err)
	}
	if err := immediateTx.Commit(ctx); err != nil {
		t.Fatalf("commit immediate tx: %v", err)
	}

	if revealErr == nil {
		revealErr = <-revealResult
	}
	if revealErr == nil {
		t.Fatal("expected late reveal to fail once finalization has started")
	}
	if !errors.Is(revealErr, sql.ErrNoRows) && !strings.Contains(revealErr.Error(), "SQLITE_BUSY") {
		t.Fatalf("expected late reveal to fail with commit expiry or sqlite lock, got %v", revealErr)
	}

	updatedCommit, err := db.GetBidCommit(ctx, lateCommit.ID)
	if err != nil {
		t.Fatalf("get late commit: %v", err)
	}
	if updatedCommit.Status != "expired" {
		t.Fatalf("expected late commit expired, got %q", updatedCommit.Status)
	}

	updatedRFQ, err := db.GetRFQ(ctx, rfq.ID)
	if err != nil {
		t.Fatalf("get updated rfq: %v", err)
	}
	if updatedRFQ.BestBidID == nil || *updatedRFQ.BestBidID != bestBid.ID {
		t.Fatalf("expected finalized best bid %d, got %+v", bestBid.ID, updatedRFQ.BestBidID)
	}

	bids, err := db.ListBidsByRFQ(ctx, rfq.ID)
	if err != nil {
		t.Fatalf("list bids after finalization: %v", err)
	}
	if len(bids) != 1 || bids[0].ID != bestBid.ID {
		t.Fatalf("expected only finalized winning bid to persist, got %+v", bids)
	}
}

func TestCommitBid_ReplacesExistingCommittedBidForBidder(t *testing.T) {
	svc, db, _ := newBiddingService(t, 0)
	ctx := context.Background()
	now := time.Now().Unix()

	rfq, err := db.CreateRFQ(ctx, &storage.RFQ{
		Title:                    "rfq-commit-replace",
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
		CommitDeadline:           now + 600,
		RevealDeadline:           now + 1200,
		ServiceTier:              0,
		Status:                   "open",
		ExpiresAt:                now + 1800,
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	first, err := svc.CommitBid(ctx, CommitBidParams{
		RFQID:      rfq.ID,
		Bidder:     testWorkerAddr,
		Commitment: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Nonce:      "nonce-1",
	})
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}

	second, err := svc.CommitBid(ctx, CommitBidParams{
		RFQID:      rfq.ID,
		Bidder:     testWorkerAddr,
		Commitment: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Nonce:      "nonce-2",
	})
	if err != nil {
		t.Fatalf("second commit: %v", err)
	}

	updatedFirst, err := db.GetBidCommit(ctx, first.ID)
	if err != nil {
		t.Fatalf("get first commit: %v", err)
	}
	if updatedFirst.Status != "superseded" {
		t.Fatalf("expected first commit status superseded, got %q", updatedFirst.Status)
	}

	updatedSecond, err := db.GetBidCommit(ctx, second.ID)
	if err != nil {
		t.Fatalf("get second commit: %v", err)
	}
	if updatedSecond.Status != "committed" {
		t.Fatalf("expected second commit status committed, got %q", updatedSecond.Status)
	}

	activeCount, err := db.CountActiveBidCommitsByRFQBidder(ctx, rfq.ID, common.HexToAddress(testWorkerAddr).Hex())
	if err != nil {
		t.Fatalf("count active commits: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("expected 1 active commit after replacement, got %d", activeCount)
	}
}
