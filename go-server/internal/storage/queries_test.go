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
		SubmissionDeadline:       "1700000000",
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
