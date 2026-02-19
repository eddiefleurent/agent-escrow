package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/ethereum/go-ethereum/common"
)

type testEnv struct {
	db    *storage.DB
	mock  *chain.MockClient
	idx   *indexer.Indexer
	cfg   *config.Config
	mux   http.Handler
}

func setup(t *testing.T) *testEnv {
	t.Helper()

	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mock := chain.NewMockClient()
	cfg := &config.Config{
		ChainID:        84532,
		FactoryAddress: "0xFactoryAddr",
	}

	idx := indexer.New(db, mock, cfg.FactoryAddress)
	mux := NewRouter(db, mock, idx, cfg)

	return &testEnv{db: db, mock: mock, idx: idx, cfg: cfg, mux: mux}
}

func (e *testEnv) request(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rr := httptest.NewRecorder()
	e.mux.ServeHTTP(rr, req)
	return rr
}

func decodeJSON(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&m); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, rr.Body.String())
	}
	return m
}

func TestHealth_OK(t *testing.T) {
	env := setup(t)
	env.mock.BlockNum = 42

	rr := env.request(t, "GET", "/api/v1/health", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", resp["status"])
	}

	chainInfo, ok := resp["chain"].(map[string]any)
	if !ok {
		t.Fatal("expected chain info in response")
	}
	if chainInfo["block_number"].(float64) != 42 {
		t.Fatalf("expected block 42, got %v", chainInfo["block_number"])
	}
}

func TestHealth_ChainDown(t *testing.T) {
	env := setup(t)
	env.mock.BlockNumErr = http.ErrServerClosed // any error

	rr := env.request(t, "GET", "/api/v1/health", "")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["status"] != "degraded" {
		t.Fatalf("expected status degraded, got %v", resp["status"])
	}
}

func TestCreateEscrow_Success(t *testing.T) {
	env := setup(t)

	escrowAddr := common.HexToAddress("0xABCDEF1234567890ABCDEF1234567890ABCDEF12")
	buyer := common.HexToAddress("0x1000000000000000000000000000000000000001")
	env.mock.Receipt = chain.MakeEscrowCreatedReceipt(7, escrowAddr, buyer)

	body := `{
		"title": "Test Task",
		"description": "Do something",
		"buyer": "0x1000000000000000000000000000000000000001",
		"worker": "0x2000000000000000000000000000000000000002",
		"verifier": "0x3000000000000000000000000000000000000003",
		"arbitrator": "0x4000000000000000000000000000000000000004",
		"amount": "1000000000000000000",
		"submission_deadline": "1700000000",
		"review_period_seconds": "86400",
		"dispute_period_seconds": "172800",
		"arbitrator_timeout_seconds": "604800"
	}`

	rr := env.request(t, "POST", "/api/v1/escrows", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["escrow_address"] != escrowAddr.Hex() {
		t.Fatalf("expected escrow address %s, got %v", escrowAddr.Hex(), resp["escrow_address"])
	}
	if resp["chain_escrow_id"].(float64) != 7 {
		t.Fatalf("expected chain escrow id 7, got %v", resp["chain_escrow_id"])
	}
	if resp["tx_hash"] == nil || resp["tx_hash"] == "" {
		t.Fatal("expected non-empty tx_hash")
	}

	// Verify the escrow was persisted with on-chain fields
	escrowID := int64(resp["escrow_id"].(float64))
	escrow, err := env.db.GetEscrow(escrowID)
	if err != nil {
		t.Fatalf("get escrow from db: %v", err)
	}
	if escrow.EscrowAddress != escrowAddr.Hex() {
		t.Fatalf("db escrow address: expected %s, got %s", escrowAddr.Hex(), escrow.EscrowAddress)
	}
	if escrow.EscrowID != 7 {
		t.Fatalf("db escrow id: expected 7, got %d", escrow.EscrowID)
	}
}

func TestCreateEscrow_ChainError(t *testing.T) {
	env := setup(t)
	env.mock.CreateEscrowErr = http.ErrServerClosed

	body := `{
		"title": "Test", "description": "x",
		"buyer": "0x01", "worker": "0x02", "verifier": "0x03", "arbitrator": "0x04",
		"amount": "100", "submission_deadline": "1700000000",
		"review_period_seconds": "86400", "dispute_period_seconds": "172800",
		"arbitrator_timeout_seconds": "604800"
	}`

	rr := env.request(t, "POST", "/api/v1/escrows", body)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCreateEscrow_InvalidJSON(t *testing.T) {
	env := setup(t)

	rr := env.request(t, "POST", "/api/v1/escrows", "not json")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestGetEscrow_Success(t *testing.T) {
	env := setup(t)

	task, err := env.db.CreateTask("Task", "desc", "0xabc")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(&storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF", EscrowAddress: "0xE1",
		Buyer: "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "created",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	rr := env.request(t, "GET", "/api/v1/escrows/1", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["Buyer"] != "0xB" {
		t.Fatalf("expected buyer 0xB, got %v", resp["Buyer"])
	}
}

func TestGetEscrow_NotFound(t *testing.T) {
	env := setup(t)

	rr := env.request(t, "GET", "/api/v1/escrows/999", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestGetEscrow_InvalidID(t *testing.T) {
	env := setup(t)

	rr := env.request(t, "GET", "/api/v1/escrows/abc", "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestListEscrows_All(t *testing.T) {
	env := setup(t)

	task, err := env.db.CreateTask("Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(&storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF", EscrowAddress: "0xE1",
		Buyer: "0xB1", Worker: "0xW1", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "created",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}
	_, err = env.db.CreateEscrow(&storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF", EscrowAddress: "0xE2",
		Buyer: "0xB2", Worker: "0xW2", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "200", Status: "funded",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	rr := env.request(t, "GET", "/api/v1/escrows", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var escrows []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&escrows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(escrows) != 2 {
		t.Fatalf("expected 2 escrows, got %d", len(escrows))
	}
}

func TestListEscrows_FilterByStatus(t *testing.T) {
	env := setup(t)

	task, err := env.db.CreateTask("Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(&storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF", EscrowAddress: "0xE1",
		Buyer: "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "created",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}
	_, err = env.db.CreateEscrow(&storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF", EscrowAddress: "0xE2",
		Buyer: "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "200", Status: "funded",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	rr := env.request(t, "GET", "/api/v1/escrows?status=funded", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var escrows []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&escrows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(escrows) != 1 {
		t.Fatalf("expected 1 funded escrow, got %d", len(escrows))
	}
}

func TestFundEscrow_Success(t *testing.T) {
	env := setup(t)

	task, err := env.db.CreateTask("Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(&storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr",
		Buyer: "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "1000000000000000000", Status: "created",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	rr := env.request(t, "POST", "/api/v1/escrows/1/fund", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["tx_hash"] == nil || resp["tx_hash"] == "" {
		t.Fatal("expected tx_hash")
	}
}

func TestFundEscrow_NotFound(t *testing.T) {
	env := setup(t)

	rr := env.request(t, "POST", "/api/v1/escrows/999/fund", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestSubmitWork_Success(t *testing.T) {
	env := setup(t)

	task, err := env.db.CreateTask("Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(&storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr",
		Buyer: "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "funded",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	rr := env.request(t, "POST", "/api/v1/escrows/1/submit", `{"submission_uri": "ipfs://result"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestApproveWork_Buyer(t *testing.T) {
	env := setup(t)

	task, err := env.db.CreateTask("Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(&storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr",
		Buyer: "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "submitted",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	rr := env.request(t, "POST", "/api/v1/escrows/1/approve", `{"role": "buyer"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestApproveWork_Verifier(t *testing.T) {
	env := setup(t)

	task, err := env.db.CreateTask("Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(&storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr",
		Buyer: "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "submitted",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	rr := env.request(t, "POST", "/api/v1/escrows/1/approve", `{"role": "verifier"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestApproveWork_InvalidRole(t *testing.T) {
	env := setup(t)

	task, err := env.db.CreateTask("Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(&storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr",
		Buyer: "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "submitted",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	rr := env.request(t, "POST", "/api/v1/escrows/1/approve", `{"role": "worker"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestDisputeWork_Buyer(t *testing.T) {
	env := setup(t)

	task, err := env.db.CreateTask("Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	escrow, err := env.db.CreateEscrow(&storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr1",
		Buyer: "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "submitted",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	body := `{"role": "buyer", "reason_uri": "ipfs://reason"}`
	rr := env.request(t, "POST", escrowPath(escrow.ID, "dispute"), body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestDisputeWork_Verifier(t *testing.T) {
	env := setup(t)

	task, err := env.db.CreateTask("Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	escrow, err := env.db.CreateEscrow(&storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr2",
		Buyer: "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "submitted",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	body := `{"role": "verifier", "reason_uri": "ipfs://reason"}`
	rr := env.request(t, "POST", escrowPath(escrow.ID, "dispute"), body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestDisputeWork_Worker(t *testing.T) {
	env := setup(t)

	task, err := env.db.CreateTask("Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	escrow, err := env.db.CreateEscrow(&storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr3",
		Buyer: "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "submitted",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	body := `{"role": "worker", "reason_uri": "ipfs://reason"}`
	rr := env.request(t, "POST", escrowPath(escrow.ID, "dispute"), body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestDisputeWork_InvalidRole(t *testing.T) {
	env := setup(t)

	task, err := env.db.CreateTask("Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(&storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr",
		Buyer: "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "submitted",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	rr := env.request(t, "POST", "/api/v1/escrows/1/dispute", `{"role": "admin", "reason_uri": "x"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestResolveDispute_Success(t *testing.T) {
	env := setup(t)

	task, err := env.db.CreateTask("Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(&storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr",
		Buyer: "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "disputed",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	body := `{"worker_award_bps": "5000", "resolution_uri": "ipfs://resolution"}`
	rr := env.request(t, "POST", "/api/v1/escrows/1/resolve", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCORS_Preflight(t *testing.T) {
	env := setup(t)

	req := httptest.NewRequest("OPTIONS", "/api/v1/health", nil)
	rr := httptest.NewRecorder()
	env.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("expected CORS header")
	}
}

func escrowPath(id int64, action string) string {
	if action != "" {
		return fmt.Sprintf("/api/v1/escrows/%d/%s", id, action)
	}
	return fmt.Sprintf("/api/v1/escrows/%d", id)
}
