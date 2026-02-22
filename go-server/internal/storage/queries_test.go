package storage

import (
	"testing"
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

	task, err := db.CreateTask("Test Task", "A description", "0xabc123")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.ID == 0 {
		t.Fatal("expected non-zero task ID")
	}
	if task.Title != "Test Task" {
		t.Fatalf("expected title 'Test Task', got %q", task.Title)
	}

	got, err := db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.SpecHash != "0xabc123" {
		t.Fatalf("expected spec hash '0xabc123', got %q", got.SpecHash)
	}
}

func TestCreateAndGetEscrow(t *testing.T) {
	db := openTestDB(t)

	task, err := db.CreateTask("Task", "", "0x123")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	escrow, err := db.CreateEscrow(&Escrow{
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

	got, err := db.GetEscrow(escrow.ID)
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

	task, err := db.CreateTask("Task", "", "0x123")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = db.CreateEscrow(&Escrow{
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

	got, err := db.GetEscrowByAddress("0xUniqueAddr")
	if err != nil {
		t.Fatalf("get by address: %v", err)
	}
	if got.EscrowAddress != "0xUniqueAddr" {
		t.Fatalf("expected address '0xUniqueAddr', got %q", got.EscrowAddress)
	}
}

func TestUpdateEscrowStatus(t *testing.T) {
	db := openTestDB(t)

	task, err := db.CreateTask("Task", "", "0x123")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	escrow, err := db.CreateEscrow(&Escrow{
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

	if err := db.UpdateEscrowStatus(escrow.ID, "funded"); err != nil {
		t.Fatalf("update status: %v", err)
	}

	got, err := db.GetEscrow(escrow.ID)
	if err != nil {
		t.Fatalf("get escrow: %v", err)
	}
	if got.Status != "funded" {
		t.Fatalf("expected status 'funded', got %q", got.Status)
	}
}

func TestListEscrows(t *testing.T) {
	db := openTestDB(t)

	task, err := db.CreateTask("Task", "", "0x123")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = db.CreateEscrow(&Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF", EscrowAddress: "0xE1",
		Buyer: "0xBuyer", Worker: "0xWorker", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "created",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}
	_, err = db.CreateEscrow(&Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF", EscrowAddress: "0xE2",
		Buyer: "0xBuyer", Worker: "0xOtherWorker", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "200", Status: "funded",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	// List all
	all, err := db.ListEscrows("", "", "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 escrows, got %d", len(all))
	}

	// Filter by status
	funded, err := db.ListEscrows("", "", "funded")
	if err != nil {
		t.Fatalf("list funded: %v", err)
	}
	if len(funded) != 1 {
		t.Fatalf("expected 1 funded, got %d", len(funded))
	}

	// Filter by role
	byWorker, err := db.ListEscrows("worker", "0xWorker", "")
	if err != nil {
		t.Fatalf("list by worker: %v", err)
	}
	if len(byWorker) != 1 {
		t.Fatalf("expected 1 for worker, got %d", len(byWorker))
	}
}

func TestSubmissions(t *testing.T) {
	db := openTestDB(t)

	task, err := db.CreateTask("Task", "", "0x123")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	escrow, err := db.CreateEscrow(&Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF", EscrowAddress: "0xE",
		Buyer: "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "submitted",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	sub, err := db.CreateSubmission(escrow.ID, "0xhash123", "ipfs://result")
	if err != nil {
		t.Fatalf("create submission: %v", err)
	}
	if sub.SubmissionHash != "0xhash123" {
		t.Fatalf("expected hash '0xhash123', got %q", sub.SubmissionHash)
	}

	subs, err := db.GetSubmissionsByEscrow(escrow.ID)
	if err != nil {
		t.Fatalf("get submissions: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected 1 submission, got %d", len(subs))
	}
}

func TestDisputes(t *testing.T) {
	db := openTestDB(t)

	task, err := db.CreateTask("Task", "", "0x123")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	escrow, err := db.CreateEscrow(&Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF", EscrowAddress: "0xE",
		Buyer: "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "disputed",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	disp, err := db.CreateDispute(escrow.ID, "0xBuyer", "ipfs://reason")
	if err != nil {
		t.Fatalf("create dispute: %v", err)
	}
	if disp.Status != "open" {
		t.Fatalf("expected status 'open', got %q", disp.Status)
	}

	err = db.UpdateDispute(disp.ID, "ipfs://resolution", 5000)
	if err != nil {
		t.Fatalf("update dispute: %v", err)
	}

	updated, err := db.GetDispute(disp.ID)
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

	err := db.CreateChainLog("0xtxhash", 0, 12345, "EscrowFunded", "0xAddr", "{}")
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}

	// Duplicate should not error (INSERT OR IGNORE)
	err = db.CreateChainLog("0xtxhash", 0, 12345, "EscrowFunded", "0xAddr", "{}")
	if err != nil {
		t.Fatalf("duplicate insert: %v", err)
	}

	exists, err := db.ChainLogExists("0xtxhash", 0)
	if err != nil {
		t.Fatalf("check exists: %v", err)
	}
	if !exists {
		t.Fatal("expected log to exist")
	}

	exists, err = db.ChainLogExists("0xother", 0)
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

	rfq, err := db.CreateRFQ(&RFQ{
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

	got, err := db.GetRFQ(rfq.ID)
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

	_, err := db.CreateRFQ(&RFQ{
		Title: "RFQ 1", Description: "desc", SpecHash: "0x1", Buyer: "0xBuyer1",
		BudgetMin: "100", BudgetMax: "500", Deadline: 1800000000,
		ReviewPeriodSeconds: 86400, DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: 1900000000, MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("create rfq 1: %v", err)
	}
	_, err = db.CreateRFQ(&RFQ{
		Title: "RFQ 2", Description: "desc", SpecHash: "0x2", Buyer: "0xBuyer2",
		BudgetMin: "200", BudgetMax: "600", Deadline: 1800000000,
		ReviewPeriodSeconds: 86400, DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "closed", ExpiresAt: 1900000000, MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("create rfq 2: %v", err)
	}

	all, err := db.ListRFQs("", "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 rfqs, got %d", len(all))
	}

	open, err := db.ListRFQs("open", "")
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("expected 1 open rfq, got %d", len(open))
	}

	byBuyer, err := db.ListRFQs("", "0xBuyer1")
	if err != nil {
		t.Fatalf("list by buyer: %v", err)
	}
	if len(byBuyer) != 1 {
		t.Fatalf("expected 1 rfq for buyer, got %d", len(byBuyer))
	}
}

func TestUpdateRFQStatus(t *testing.T) {
	db := openTestDB(t)

	rfq, err := db.CreateRFQ(&RFQ{
		Title: "RFQ", Description: "desc", SpecHash: "0x1", Buyer: "0xBuyer",
		BudgetMin: "100", BudgetMax: "500", Deadline: 1800000000,
		ReviewPeriodSeconds: 86400, DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: 1900000000, MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	if err := db.UpdateRFQStatus(rfq.ID, "closed"); err != nil {
		t.Fatalf("update status: %v", err)
	}

	got, err := db.GetRFQ(rfq.ID)
	if err != nil {
		t.Fatalf("get rfq: %v", err)
	}
	if got.Status != "closed" {
		t.Fatalf("expected status 'closed', got %q", got.Status)
	}
}

func TestCreateAndGetBid(t *testing.T) {
	db := openTestDB(t)

	rfq, err := db.CreateRFQ(&RFQ{
		Title: "RFQ", Description: "desc", SpecHash: "0x1", Buyer: "0xBuyer",
		BudgetMin: "100", BudgetMax: "500", Deadline: 1800000000,
		ReviewPeriodSeconds: 86400, DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: 1900000000, MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	bid, err := db.CreateBid(&Bid{
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

	got, err := db.GetBid(bid.ID)
	if err != nil {
		t.Fatalf("get bid: %v", err)
	}
	if got.Amount != "300" {
		t.Fatalf("expected amount '300', got %q", got.Amount)
	}
}

func TestListBidsByRFQ(t *testing.T) {
	db := openTestDB(t)

	rfq, err := db.CreateRFQ(&RFQ{
		Title: "RFQ", Description: "desc", SpecHash: "0x1", Buyer: "0xBuyer",
		BudgetMin: "100", BudgetMax: "500", Deadline: 1800000000,
		ReviewPeriodSeconds: 86400, DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: 1900000000, MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	for i := 0; i < 3; i++ {
		_, err := db.CreateBid(&Bid{
			RFQID: rfq.ID, Bidder: "0xWorker", Amount: "200",
			Status: "pending", ExpiresAt: 1850000000, MilestonesJSON: "[]",
		})
		if err != nil {
			t.Fatalf("create bid %d: %v", i, err)
		}
	}

	bids, err := db.ListBidsByRFQ(rfq.ID)
	if err != nil {
		t.Fatalf("list bids: %v", err)
	}
	if len(bids) != 3 {
		t.Fatalf("expected 3 bids, got %d", len(bids))
	}
}

func TestListBidsByBidder(t *testing.T) {
	db := openTestDB(t)

	rfq, err := db.CreateRFQ(&RFQ{
		Title: "RFQ", Description: "desc", SpecHash: "0x1", Buyer: "0xBuyer",
		BudgetMin: "100", BudgetMax: "500", Deadline: 1800000000,
		ReviewPeriodSeconds: 86400, DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: 1900000000, MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	_, err = db.CreateBid(&Bid{
		RFQID: rfq.ID, Bidder: "0xWorkerA", Amount: "200",
		Status: "pending", ExpiresAt: 1850000000, MilestonesJSON: "[]",
	})
	if err != nil {
		t.Fatalf("create bid: %v", err)
	}
	_, err = db.CreateBid(&Bid{
		RFQID: rfq.ID, Bidder: "0xWorkerB", Amount: "300",
		Status: "pending", ExpiresAt: 1850000000, MilestonesJSON: "[]",
	})
	if err != nil {
		t.Fatalf("create bid: %v", err)
	}

	bidsA, err := db.ListBidsByBidder("0xWorkerA")
	if err != nil {
		t.Fatalf("list bids: %v", err)
	}
	if len(bidsA) != 1 {
		t.Fatalf("expected 1 bid for WorkerA, got %d", len(bidsA))
	}
}

func TestAcceptBidAndRejectPending(t *testing.T) {
	db := openTestDB(t)

	rfq, err := db.CreateRFQ(&RFQ{
		Title: "RFQ", Description: "desc", SpecHash: "0x1", Buyer: "0xBuyer",
		BudgetMin: "100", BudgetMax: "500", Deadline: 1800000000,
		ReviewPeriodSeconds: 86400, DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: 1900000000, MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("create rfq: %v", err)
	}

	task, err := db.CreateTask("Task", "", "0x123")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	escrow, err := db.CreateEscrow(&Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF", EscrowAddress: "0xE",
		Buyer: "0xBuyer", Worker: "0xWorkerA", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "200", Status: "created",
	})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}

	bid1, err := db.CreateBid(&Bid{
		RFQID: rfq.ID, Bidder: "0xWorkerA", Amount: "200",
		Status: "pending", ExpiresAt: 1850000000, MilestonesJSON: "[]",
	})
	if err != nil {
		t.Fatalf("create bid 1: %v", err)
	}
	bid2, err := db.CreateBid(&Bid{
		RFQID: rfq.ID, Bidder: "0xWorkerB", Amount: "300",
		Status: "pending", ExpiresAt: 1850000000, MilestonesJSON: "[]",
	})
	if err != nil {
		t.Fatalf("create bid 2: %v", err)
	}

	if err := db.AcceptBid(bid1.ID, escrow.ID); err != nil {
		t.Fatalf("accept bid: %v", err)
	}
	if err := db.RejectPendingBids(rfq.ID, bid1.ID); err != nil {
		t.Fatalf("reject pending: %v", err)
	}

	accepted, err := db.GetBid(bid1.ID)
	if err != nil {
		t.Fatalf("get accepted bid: %v", err)
	}
	if accepted.Status != "accepted" {
		t.Fatalf("expected status 'accepted', got %q", accepted.Status)
	}
	if accepted.EscrowID == nil || *accepted.EscrowID != escrow.ID {
		t.Fatalf("expected escrow_id %d, got %v", escrow.ID, accepted.EscrowID)
	}

	rejected, err := db.GetBid(bid2.ID)
	if err != nil {
		t.Fatalf("get rejected bid: %v", err)
	}
	if rejected.Status != "rejected" {
		t.Fatalf("expected status 'rejected', got %q", rejected.Status)
	}
}

func TestCursor(t *testing.T) {
	db := openTestDB(t)

	block, err := db.GetCursor(84532, "indexer")
	if err != nil {
		t.Fatalf("get cursor: %v", err)
	}
	if block != 0 {
		t.Fatalf("expected 0, got %d", block)
	}

	if err := db.SetCursor(84532, "indexer", 100); err != nil {
		t.Fatalf("set cursor: %v", err)
	}

	block, err = db.GetCursor(84532, "indexer")
	if err != nil {
		t.Fatalf("get cursor after set: %v", err)
	}
	if block != 100 {
		t.Fatalf("expected 100, got %d", block)
	}

	// Update existing
	if err := db.SetCursor(84532, "indexer", 200); err != nil {
		t.Fatalf("update cursor: %v", err)
	}
	block, err = db.GetCursor(84532, "indexer")
	if err != nil {
		t.Fatalf("get cursor: %v", err)
	}
	if block != 200 {
		t.Fatalf("expected 200, got %d", block)
	}
}
