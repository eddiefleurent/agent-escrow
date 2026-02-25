package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCreateAndGetTask(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	task, err := db.CreateTask(ctx, "Test Task", "A description", "0xabc123")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.ID == 0 {
		t.Fatal("expected non-zero task ID")
	}
	if task.Title != "Test Task" {
		t.Fatalf("expected title 'Test Task', got %q", task.Title)
	}

	got, err := db.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.SpecHash != "0xabc123" {
		t.Fatalf("expected spec hash '0xabc123', got %q", got.SpecHash)
	}
}

func TestCreateAndGetEscrow(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	task, err := db.CreateTask(ctx, "Task", "", "0x123")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	escrow, err := db.CreateEscrow(ctx, &Escrow{
		TaskID:                   task.ID,
		ChainID:                  84532,
		FactoryAddress:           "0xFactory",
		EscrowAddress:            "0xEscrow1",
		EscrowID:                 0,
		Buyer:                    "0xBuyer",
		Worker:                   "0xWorker",
		Verifier:                 "0xVerifier",
		Arbitrator:               "0xArbitrator",
		Amount:                   "1000000000000000000",
		Status:                   "created",
		SubmissionDeadline:       1700000000,
		ReviewPeriodSeconds:      86400,
		DisputePeriodSeconds:     172800,
		ArbitratorTimeoutSeconds: 604800,
	})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}
	if escrow.ID == 0 {
		t.Fatal("expected non-zero escrow ID")
	}

	got, err := db.GetEscrow(ctx, escrow.ID)
	if err != nil {
		t.Fatalf("get escrow: %v", err)
	}
	if got.Buyer != "0xBuyer" {
		t.Fatalf("expected buyer '0xBuyer', got %q", got.Buyer)
	}
	if got.ArbitratorTimeoutSeconds != 604800 {
		t.Fatalf("expected arb timeout 604800, got %d", got.ArbitratorTimeoutSeconds)
	}
}

func TestGetEscrowByAddress(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	task, err := db.CreateTask(ctx, "Task", "", "0x123")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = db.CreateEscrow(ctx, &Escrow{
		TaskID:         task.ID,
		ChainID:        84532,
		FactoryAddress: "0xFactory",
		EscrowAddress:  "0xUniqueAddr",
		Buyer:          "0xBuyer",
		Worker:         "0xWorker",
		Verifier:       "0xVerifier",
		Arbitrator:     "0xArbitrator",
		Amount:         "100",
		Status:         "created",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	got, err := db.GetEscrowByAddress(ctx, "0xUniqueAddr")
	if err != nil {
		t.Fatalf("get by address: %v", err)
	}
	if got.EscrowAddress != "0xUniqueAddr" {
		t.Fatalf("expected address '0xUniqueAddr', got %q", got.EscrowAddress)
	}
}

func TestUpdateEscrowStatus(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	task, err := db.CreateTask(ctx, "Task", "", "0x123")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	escrow, err := db.CreateEscrow(ctx, &Escrow{
		TaskID:         task.ID,
		ChainID:        84532,
		FactoryAddress: "0xFactory",
		EscrowAddress:  "0xAddr",
		Buyer:          "0xBuyer",
		Worker:         "0xWorker",
		Verifier:       "0xVerifier",
		Arbitrator:     "0xArbitrator",
		Amount:         "100",
		Status:         "created",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	if err := db.UpdateEscrowStatus(ctx, escrow.ID, "funded"); err != nil {
		t.Fatalf("update status: %v", err)
	}

	got, err := db.GetEscrow(ctx, escrow.ID)
	if err != nil {
		t.Fatalf("get escrow: %v", err)
	}
	if got.Status != "funded" {
		t.Fatalf("expected status 'funded', got %q", got.Status)
	}
}

func TestListEscrows(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	task, err := db.CreateTask(ctx, "Task", "", "0x123")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = db.CreateEscrow(ctx, &Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF", EscrowAddress: "0xE1",
		Buyer: "0xBuyer", Worker: "0xWorker", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "created",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}
	_, err = db.CreateEscrow(ctx, &Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF", EscrowAddress: "0xE2",
		Buyer: "0xBuyer", Worker: "0xOtherWorker", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "200", Status: "funded",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	// List all
	all, err := db.ListEscrows(ctx, "", "", "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 escrows, got %d", len(all))
	}

	// Filter by status
	funded, err := db.ListEscrows(ctx, "", "", "funded")
	if err != nil {
		t.Fatalf("list funded: %v", err)
	}
	if len(funded) != 1 {
		t.Fatalf("expected 1 funded, got %d", len(funded))
	}

	// Filter by role
	byWorker, err := db.ListEscrows(ctx, "worker", "0xWorker", "")
	if err != nil {
		t.Fatalf("list by worker: %v", err)
	}
	if len(byWorker) != 1 {
		t.Fatalf("expected 1 for worker, got %d", len(byWorker))
	}
}

func TestSubmissions(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	task, err := db.CreateTask(ctx, "Task", "", "0x123")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	escrow, err := db.CreateEscrow(ctx, &Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF", EscrowAddress: "0xE",
		Buyer: "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "submitted",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	sub, err := db.CreateSubmission(ctx, escrow.ID, "0xhash123", "ipfs://result")
	if err != nil {
		t.Fatalf("create submission: %v", err)
	}
	if sub.SubmissionHash != "0xhash123" {
		t.Fatalf("expected hash '0xhash123', got %q", sub.SubmissionHash)
	}

	subs, err := db.GetSubmissionsByEscrow(ctx, escrow.ID)
	if err != nil {
		t.Fatalf("get submissions: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected 1 submission, got %d", len(subs))
	}
}

func TestDisputes(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	task, err := db.CreateTask(ctx, "Task", "", "0x123")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	escrow, err := db.CreateEscrow(ctx, &Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF", EscrowAddress: "0xE",
		Buyer: "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "disputed",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	disp, err := db.CreateDispute(ctx, escrow.ID, "0xBuyer", "ipfs://reason")
	if err != nil {
		t.Fatalf("create dispute: %v", err)
	}
	if disp.Status != "open" {
		t.Fatalf("expected status 'open', got %q", disp.Status)
	}

	err = db.UpdateDispute(ctx, disp.ID, "ipfs://resolution", 5000)
	if err != nil {
		t.Fatalf("update dispute: %v", err)
	}

	updated, err := db.GetDispute(ctx, disp.ID)
	if err != nil {
		t.Fatalf("get dispute after update: %v", err)
	}
	if updated.ResolutionURI != "ipfs://resolution" {
		t.Fatalf("expected resolution URI 'ipfs://resolution', got %q", updated.ResolutionURI)
	}
	if updated.WorkerAwardBps == nil || *updated.WorkerAwardBps != 5000 {
		t.Fatalf("expected worker award bps 5000, got %v", updated.WorkerAwardBps)
	}
	if updated.Status != "resolved" {
		t.Fatalf("expected status 'resolved', got %q", updated.Status)
	}
}

func TestChainLogIdempotent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	err := db.CreateChainLog(ctx, "0xtxhash", 0, 12345, "EscrowFunded", "0xAddr", "{}")
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}

	// Duplicate should not error (INSERT OR IGNORE)
	err = db.CreateChainLog(ctx, "0xtxhash", 0, 12345, "EscrowFunded", "0xAddr", "{}")
	if err != nil {
		t.Fatalf("duplicate insert: %v", err)
	}

	exists, err := db.ChainLogExists(ctx, "0xtxhash", 0)
	if err != nil {
		t.Fatalf("check exists: %v", err)
	}
	if !exists {
		t.Fatal("expected log to exist")
	}

	exists, err = db.ChainLogExists(ctx, "0xother", 0)
	if err != nil {
		t.Fatalf("check not exists: %v", err)
	}
	if exists {
		t.Fatal("expected log to not exist")
	}
}

// RFQ and Bid tests

func TestCreateAndGetRFQ(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rfq, err := db.CreateRFQ(ctx, &RFQ{
		Title:                    "Build a widget",
		Description:              "Build a high-quality widget",
		SpecHash:                 "0xabc",
		Buyer:                    "0xBuyer",
		Token:                    "",
		BudgetMin:                "100",
		BudgetMax:                "500",
		Deadline:                 1800000000,
		ReviewPeriodSeconds:      86400,
		DisputePeriodSeconds:     172800,
		ArbitratorTimeoutSeconds: 604800,
		Verifier:                 "0xVerifier",
		Arbitrator:               "0xArbitrator",
		WorkerStake:              "0",
		MilestonesJSON:           "[]",
		RequirementsJSON:         `{"tags":["go"]}`,
		Status:                   "open",
		ExpiresAt:                1900000000,
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}
	if rfq.ID == 0 {
		t.Fatal("expected non-zero rfq ID")
	}
	if rfq.Title != "Build a widget" {
		t.Fatalf("expected title 'Build a widget', got %q", rfq.Title)
	}
	if rfq.Status != "open" {
		t.Fatalf("expected status 'open', got %q", rfq.Status)
	}

	got, err := db.GetRFQ(ctx, rfq.ID)
	if err != nil {
		t.Fatalf("get rfq: %v", err)
	}
	if got.BudgetMax != "500" {
		t.Fatalf("expected budget_max '500', got %q", got.BudgetMax)
	}
	if got.RequirementsJSON != `{"tags":["go"]}` {
		t.Fatalf("expected requirements_json, got %q", got.RequirementsJSON)
	}
}

func TestListRFQs(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	_, err := db.CreateRFQ(ctx, &RFQ{
		Title: "RFQ 1", Description: "desc", SpecHash: "0x1", Buyer: "0xBuyer1",
		BudgetMin: "100", BudgetMax: "500", Deadline: 1800000000,
		ReviewPeriodSeconds: 86400, DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: 1900000000, MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("create rfq 1: %v", err)
	}
	_, err = db.CreateRFQ(ctx, &RFQ{
		Title: "RFQ 2", Description: "desc", SpecHash: "0x2", Buyer: "0xBuyer2",
		BudgetMin: "200", BudgetMax: "600", Deadline: 1800000000,
		ReviewPeriodSeconds: 86400, DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "closed", ExpiresAt: 1900000000, MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("create rfq 2: %v", err)
	}

	all, err := db.ListRFQs(ctx, "", "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 rfqs, got %d", len(all))
	}

	open, err := db.ListRFQs(ctx, "open", "")
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("expected 1 open rfq, got %d", len(open))
	}

	byBuyer, err := db.ListRFQs(ctx, "", "0xBuyer1")
	if err != nil {
		t.Fatalf("list by buyer: %v", err)
	}
	if len(byBuyer) != 1 {
		t.Fatalf("expected 1 rfq for buyer, got %d", len(byBuyer))
	}
}

func TestUpdateRFQStatus(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rfq, err := db.CreateRFQ(ctx, &RFQ{
		Title: "RFQ", Description: "desc", SpecHash: "0x1", Buyer: "0xBuyer",
		BudgetMin: "100", BudgetMax: "500", Deadline: 1800000000,
		ReviewPeriodSeconds: 86400, DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: 1900000000, MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	if err := db.UpdateRFQStatus(ctx, rfq.ID, "closed"); err != nil {
		t.Fatalf("update status: %v", err)
	}

	got, err := db.GetRFQ(ctx, rfq.ID)
	if err != nil {
		t.Fatalf("get rfq: %v", err)
	}
	if got.Status != "closed" {
		t.Fatalf("expected status 'closed', got %q", got.Status)
	}
}

func TestCreateAndGetBid(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rfq, err := db.CreateRFQ(ctx, &RFQ{
		Title: "RFQ", Description: "desc", SpecHash: "0x1", Buyer: "0xBuyer",
		BudgetMin: "100", BudgetMax: "500", Deadline: 1800000000,
		ReviewPeriodSeconds: 86400, DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: 1900000000, MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	bid, err := db.CreateBid(ctx, &Bid{
		RFQID:             rfq.ID,
		Bidder:            "0xWorker",
		Amount:            "300",
		EstimatedDuration: 3600,
		ReputationBond:    "50",
		MilestonesJSON:    "[]",
		Message:           "I can do this",
		Status:            "pending",
		ExpiresAt:         1850000000,
	})
	if err != nil {
		t.Fatalf("create bid: %v", err)
	}
	if bid.ID == 0 {
		t.Fatal("expected non-zero bid ID")
	}
	if bid.Bidder != "0xWorker" {
		t.Fatalf("expected bidder '0xWorker', got %q", bid.Bidder)
	}
	if bid.EscrowID != nil {
		t.Fatalf("expected nil escrow_id, got %v", bid.EscrowID)
	}

	got, err := db.GetBid(ctx, bid.ID)
	if err != nil {
		t.Fatalf("get bid: %v", err)
	}
	if got.Amount != "300" {
		t.Fatalf("expected amount '300', got %q", got.Amount)
	}
}

func TestListBidsByRFQ(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rfq, err := db.CreateRFQ(ctx, &RFQ{
		Title: "RFQ", Description: "desc", SpecHash: "0x1", Buyer: "0xBuyer",
		BudgetMin: "100", BudgetMax: "500", Deadline: 1800000000,
		ReviewPeriodSeconds: 86400, DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: 1900000000, MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	for i := range 3 {
		_, err := db.CreateBid(ctx, &Bid{
			RFQID: rfq.ID, Bidder: "0xWorker", Amount: "200",
			Status: "pending", ExpiresAt: 1850000000, MilestonesJSON: "[]",
		})
		if err != nil {
			t.Fatalf("create bid %d: %v", i, err)
		}
	}

	bids, err := db.ListBidsByRFQ(ctx, rfq.ID)
	if err != nil {
		t.Fatalf("list bids: %v", err)
	}
	if len(bids) != 3 {
		t.Fatalf("expected 3 bids, got %d", len(bids))
	}
}

func TestListBidsByBidder(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rfq, err := db.CreateRFQ(ctx, &RFQ{
		Title: "RFQ", Description: "desc", SpecHash: "0x1", Buyer: "0xBuyer",
		BudgetMin: "100", BudgetMax: "500", Deadline: 1800000000,
		ReviewPeriodSeconds: 86400, DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: 1900000000, MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	_, err = db.CreateBid(ctx, &Bid{
		RFQID: rfq.ID, Bidder: "0xWorkerA", Amount: "200",
		Status: "pending", ExpiresAt: 1850000000, MilestonesJSON: "[]",
	})
	if err != nil {
		t.Fatalf("create bid: %v", err)
	}
	_, err = db.CreateBid(ctx, &Bid{
		RFQID: rfq.ID, Bidder: "0xWorkerB", Amount: "300",
		Status: "pending", ExpiresAt: 1850000000, MilestonesJSON: "[]",
	})
	if err != nil {
		t.Fatalf("create bid: %v", err)
	}

	bidsA, err := db.ListBidsByBidder(ctx, "0xWorkerA")
	if err != nil {
		t.Fatalf("list bids: %v", err)
	}
	if len(bidsA) != 1 {
		t.Fatalf("expected 1 bid for WorkerA, got %d", len(bidsA))
	}
}

func TestAcceptBidAndRejectPending(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rfq, err := db.CreateRFQ(ctx, &RFQ{
		Title: "RFQ", Description: "desc", SpecHash: "0x1", Buyer: "0xBuyer",
		BudgetMin: "100", BudgetMax: "500", Deadline: 1800000000,
		ReviewPeriodSeconds: 86400, DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: 1900000000, MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	task, err := db.CreateTask(ctx, "Task", "", "0x123")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	escrow, err := db.CreateEscrow(ctx, &Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF", EscrowAddress: "0xE",
		Buyer: "0xBuyer", Worker: "0xWorkerA", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "200", Status: "created",
	})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}

	bid1, err := db.CreateBid(ctx, &Bid{
		RFQID: rfq.ID, Bidder: "0xWorkerA", Amount: "200",
		Status: "pending", ExpiresAt: 1850000000, MilestonesJSON: "[]",
	})
	if err != nil {
		t.Fatalf("create bid 1: %v", err)
	}
	bid2, err := db.CreateBid(ctx, &Bid{
		RFQID: rfq.ID, Bidder: "0xWorkerB", Amount: "300",
		Status: "pending", ExpiresAt: 1850000000, MilestonesJSON: "[]",
	})
	if err != nil {
		t.Fatalf("create bid 2: %v", err)
	}

	if err := db.AcceptBid(ctx, bid1.ID, escrow.ID); err != nil {
		t.Fatalf("accept bid: %v", err)
	}
	if err := db.RejectPendingBids(ctx, rfq.ID, bid1.ID); err != nil {
		t.Fatalf("reject pending: %v", err)
	}

	accepted, err := db.GetBid(ctx, bid1.ID)
	if err != nil {
		t.Fatalf("get accepted bid: %v", err)
	}
	if accepted.Status != "accepted" {
		t.Fatalf("expected status 'accepted', got %q", accepted.Status)
	}
	if accepted.EscrowID == nil || *accepted.EscrowID != escrow.ID {
		t.Fatalf("expected escrow_id %d, got %v", escrow.ID, accepted.EscrowID)
	}

	rejected, err := db.GetBid(ctx, bid2.ID)
	if err != nil {
		t.Fatalf("get rejected bid: %v", err)
	}
	if rejected.Status != "rejected" {
		t.Fatalf("expected status 'rejected', got %q", rejected.Status)
	}
}

func TestBidCommitQueriesAndExpiry(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rfq, err := db.CreateRFQ(ctx, &RFQ{
		Title: "RFQ", Description: "desc", SpecHash: "0x1", Buyer: "0xBuyer",
		BudgetMin: "100", BudgetMax: "500", Deadline: 1800000000,
		ReviewPeriodSeconds: 86400, DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: 1900000000, MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	commitA, err := db.CreateBidCommit(ctx, &BidCommit{
		RFQID:      rfq.ID,
		Bidder:     "0xWorkerA",
		Commitment: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Nonce:      "n1",
		Status:     "committed",
	})
	if err != nil {
		t.Fatalf("create commit A: %v", err)
	}
	commitB, err := db.CreateBidCommit(ctx, &BidCommit{
		RFQID:      rfq.ID,
		Bidder:     "0xWorkerA",
		Commitment: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Nonce:      "n2",
		Status:     "revealed",
	})
	if err != nil {
		t.Fatalf("create commit B: %v", err)
	}

	gotByNonce, err := db.GetBidCommitByRFQBidderNonce(ctx, rfq.ID, "0xWorkerA", "n1")
	if err != nil {
		t.Fatalf("get by nonce: %v", err)
	}
	if gotByNonce.ID != commitA.ID {
		t.Fatalf("expected commit id %d, got %d", commitA.ID, gotByNonce.ID)
	}

	gotByCommitment, err := db.GetBidCommitByRFQBidderCommitment(ctx, rfq.ID, "0xWorkerA", commitB.Commitment)
	if err != nil {
		t.Fatalf("get by commitment: %v", err)
	}
	if gotByCommitment.ID != commitB.ID {
		t.Fatalf("expected commit id %d, got %d", commitB.ID, gotByCommitment.ID)
	}

	activeCount, err := db.CountActiveBidCommitsByRFQBidder(ctx, rfq.ID, "0xWorkerA")
	if err != nil {
		t.Fatalf("count active: %v", err)
	}
	if activeCount != 2 {
		t.Fatalf("expected 2 active commits, got %d", activeCount)
	}

	recentCount, err := db.CountRecentBidCommitsByRFQBidder(ctx, rfq.ID, "0xWorkerA", 60, time.Now())
	if err != nil {
		t.Fatalf("count recent: %v", err)
	}
	if recentCount != 2 {
		t.Fatalf("expected 2 recent commits, got %d", recentCount)
	}

	if err := db.ExpireCommittedBidCommits(ctx, rfq.ID); err != nil {
		t.Fatalf("expire committed commits: %v", err)
	}

	updatedA, err := db.GetBidCommitByRFQBidderNonce(ctx, rfq.ID, "0xWorkerA", "n1")
	if err != nil {
		t.Fatalf("get updated commit A: %v", err)
	}
	if updatedA.Status != "expired" {
		t.Fatalf("expected commit A status expired, got %q", updatedA.Status)
	}

	recentAfterExpiry, err := db.CountRecentBidCommitsByRFQBidder(ctx, rfq.ID, "0xWorkerA", 60, time.Now())
	if err != nil {
		t.Fatalf("count recent after expiry: %v", err)
	}
	if recentAfterExpiry != 1 {
		t.Fatalf("expected 1 recent active commit after expiry, got %d", recentAfterExpiry)
	}

	updatedB, err := db.GetBidCommitByRFQBidderNonce(ctx, rfq.ID, "0xWorkerA", "n2")
	if err != nil {
		t.Fatalf("get updated commit B: %v", err)
	}
	if updatedB.Status != "revealed" {
		t.Fatalf("expected commit B status revealed, got %q", updatedB.Status)
	}
}

func TestCreateBidCommit_DuplicateErrorMapping(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rfq, err := db.CreateRFQ(ctx, &RFQ{
		Title: "RFQ", Description: "desc", SpecHash: "0x1", Buyer: "0xBuyer",
		BudgetMin: "100", BudgetMax: "500", Deadline: 1800000000,
		ReviewPeriodSeconds: 86400, DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: 1900000000, MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	_, err = db.CreateBidCommit(ctx, &BidCommit{
		RFQID:      rfq.ID,
		Bidder:     "0xWorkerA",
		Commitment: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Nonce:      "n1",
		Status:     "committed",
	})
	if err != nil {
		t.Fatalf("create baseline commit: %v", err)
	}

	_, err = db.CreateBidCommit(ctx, &BidCommit{
		RFQID:      rfq.ID,
		Bidder:     "0xWorkerA",
		Commitment: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Nonce:      "n1",
		Status:     "committed",
	})
	if !errors.Is(err, ErrDuplicateBidCommitNonce) {
		t.Fatalf("expected ErrDuplicateBidCommitNonce, got %v", err)
	}

	_, err = db.CreateBidCommit(ctx, &BidCommit{
		RFQID:      rfq.ID,
		Bidder:     "0xWorkerA",
		Commitment: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Nonce:      "n2",
		Status:     "committed",
	})
	if !errors.Is(err, ErrDuplicateBidCommitCommitment) {
		t.Fatalf("expected ErrDuplicateBidCommitCommitment, got %v", err)
	}
}

func TestCursor(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	block, err := db.GetCursor(ctx, 84532, "indexer")
	if err != nil {
		t.Fatalf("get cursor: %v", err)
	}
	if block != 0 {
		t.Fatalf("expected 0, got %d", block)
	}

	if err := db.SetCursor(ctx, 84532, "indexer", 100); err != nil {
		t.Fatalf("set cursor: %v", err)
	}

	block, err = db.GetCursor(ctx, 84532, "indexer")
	if err != nil {
		t.Fatalf("get cursor after set: %v", err)
	}
	if block != 100 {
		t.Fatalf("expected 100, got %d", block)
	}

	// Update existing
	if err := db.SetCursor(ctx, 84532, "indexer", 200); err != nil {
		t.Fatalf("update cursor: %v", err)
	}
	block, err = db.GetCursor(ctx, 84532, "indexer")
	if err != nil {
		t.Fatalf("get cursor: %v", err)
	}
	if block != 200 {
		t.Fatalf("expected 200, got %d", block)
	}
}

// Emergency response protocol tests

func TestFrozenAddresses(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// Upsert and list
	if err := db.UpsertFrozenAddress(ctx, "0xBadActor", "suspicious activity", "0xAdmin"); err != nil {
		t.Fatalf("upsert frozen address: %v", err)
	}
	if err := db.UpsertFrozenAddress(ctx, "0xAnotherBad", "fraud", "0xAdmin"); err != nil {
		t.Fatalf("upsert second frozen address: %v", err)
	}

	addrs, err := db.ListFrozenAddresses(ctx)
	if err != nil {
		t.Fatalf("list frozen addresses: %v", err)
	}
	if len(addrs) != 2 {
		t.Fatalf("expected 2 frozen addresses, got %d", len(addrs))
	}
	if addrs[0].Address != "0xAnotherBad" && addrs[0].Address != "0xBadActor" {
		t.Fatalf("unexpected address in list: %q", addrs[0].Address)
	}
	if addrs[0].Reason == "" || addrs[0].FrozenBy == "" {
		t.Fatalf("expected reason and frozen_by to be set")
	}

	// IsFrozenAddress
	frozen, err := db.IsFrozenAddress(ctx, "0xBadActor")
	if err != nil {
		t.Fatalf("is frozen: %v", err)
	}
	if !frozen {
		t.Fatal("expected 0xBadActor to be frozen")
	}
	frozen, err = db.IsFrozenAddress(ctx, "0xGoodActor")
	if err != nil {
		t.Fatalf("is frozen (good): %v", err)
	}
	if frozen {
		t.Fatal("expected 0xGoodActor to not be frozen")
	}

	// Upsert updates existing
	if err := db.UpsertFrozenAddress(ctx, "0xBadActor", "updated reason", "0xNewAdmin"); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	addrs, err = db.ListFrozenAddresses(ctx)
	if err != nil {
		t.Fatalf("list after upsert: %v", err)
	}
	var found *FrozenAddress
	for _, a := range addrs {
		if a.Address == "0xBadActor" {
			found = a
			break
		}
	}
	if found == nil || found.Reason != "updated reason" || found.FrozenBy != "0xNewAdmin" {
		t.Fatalf("expected upsert to update: got %+v", found)
	}

	// Delete
	if err := db.DeleteFrozenAddress(ctx, "0xBadActor"); err != nil {
		t.Fatalf("delete frozen address: %v", err)
	}
	frozen, err = db.IsFrozenAddress(ctx, "0xBadActor")
	if err != nil {
		t.Fatalf("is frozen after delete: %v", err)
	}
	if frozen {
		t.Fatal("expected 0xBadActor to not be frozen after delete")
	}
	addrs, err = db.ListFrozenAddresses(ctx)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(addrs) != 1 {
		t.Fatalf("expected 1 frozen address after delete, got %d", len(addrs))
	}
}

func TestEmergencyActions(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// Create actions
	for i := range 5 {
		err := db.CreateEmergencyAction(ctx, "freeze", "0xTarget", "escrow-1", "reason", fmt.Sprintf("0xtx%d", i))
		if err != nil {
			t.Fatalf("create emergency action %d: %v", i, err)
		}
	}

	// List with limit/offset
	all, err := db.ListEmergencyActions(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("expected 5 actions, got %d", len(all))
	}
	if all[0].Action != "freeze" || all[0].Target != "0xTarget" || all[0].EscrowID != "escrow-1" {
		t.Fatalf("unexpected action fields: %+v", all[0])
	}
	if all[0].TxHash == "" || all[0].CreatedAt.IsZero() {
		t.Fatalf("expected tx_hash and created_at to be set")
	}

	// Pagination
	page1, err := db.ListEmergencyActions(ctx, 2, 0)
	if err != nil {
		t.Fatalf("list limit 2: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(page1))
	}
	page2, err := db.ListEmergencyActions(ctx, 2, 2)
	if err != nil {
		t.Fatalf("list offset 2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("expected 2 actions on page 2, got %d", len(page2))
	}
	if page1[0].ID == page2[0].ID {
		t.Fatal("expected different IDs across pages")
	}
}

func TestUpdateEscrowFrozen(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	task, err := db.CreateTask(ctx, "Task", "", "0x123")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	escrow, err := db.CreateEscrow(ctx, &Escrow{
		TaskID:         task.ID,
		ChainID:        84532,
		FactoryAddress: "0xF",
		EscrowAddress:  "0xE",
		Buyer:          "0xBuyer",
		Worker:         "0xWorker",
		Verifier:       "0xV",
		Arbitrator:     "0xA",
		Amount:         "100",
		Status:         "created",
		Frozen:         false,
	})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}

	got, err := db.GetEscrow(ctx, escrow.ID)
	if err != nil {
		t.Fatalf("get escrow: %v", err)
	}
	if got.Frozen {
		t.Fatal("expected escrow to not be frozen initially")
	}

	if err := db.UpdateEscrowFrozen(ctx, escrow.ID, true); err != nil {
		t.Fatalf("update escrow frozen: %v", err)
	}
	got, err = db.GetEscrow(ctx, escrow.ID)
	if err != nil {
		t.Fatalf("get escrow after update: %v", err)
	}
	if !got.Frozen {
		t.Fatal("expected escrow to be frozen")
	}

	if err := db.UpdateEscrowFrozen(ctx, escrow.ID, false); err != nil {
		t.Fatalf("update escrow unfrozen: %v", err)
	}
	got, err = db.GetEscrow(ctx, escrow.ID)
	if err != nil {
		t.Fatalf("get escrow after unfreeze: %v", err)
	}
	if got.Frozen {
		t.Fatal("expected escrow to not be frozen after unfreeze")
	}
}

func TestGetEscrowByOnChainID(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	task, err := db.CreateTask(ctx, "Task", "", "0x123")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	escrow, err := db.CreateEscrow(ctx, &Escrow{
		TaskID:         task.ID,
		ChainID:        84532,
		FactoryAddress: "0xF",
		EscrowAddress:  "0xEscrowAddr",
		EscrowID:       42,
		Buyer:          "0xBuyer",
		Worker:         "0xWorker",
		Verifier:       "0xV",
		Arbitrator:     "0xA",
		Amount:         "100",
		Status:         "created",
	})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}

	got, err := db.GetEscrowByOnChainID(ctx, 84532, 42)
	if err != nil {
		t.Fatalf("get escrow by on-chain ID: %v", err)
	}
	if got.ID != escrow.ID {
		t.Fatalf("expected escrow ID %d, got %d", escrow.ID, got.ID)
	}
	if got.EscrowAddress != "0xEscrowAddr" {
		t.Fatalf("expected address 0xEscrowAddr, got %q", got.EscrowAddress)
	}
	if got.EscrowID != 42 {
		t.Fatalf("expected escrow_id 42, got %d", got.EscrowID)
	}

	// Different chain or escrow_id should not find
	_, err = db.GetEscrowByOnChainID(ctx, 1, 42)
	if err == nil {
		t.Fatal("expected error for wrong chain")
	}
	_, err = db.GetEscrowByOnChainID(ctx, 84532, 99)
	if err == nil {
		t.Fatal("expected error for non-existent escrow_id")
	}
}
