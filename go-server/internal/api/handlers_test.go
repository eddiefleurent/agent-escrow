package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/ethereum/go-ethereum/common"
)

type testEnv struct {
	db   *storage.DB
	mock *chain.MockClient
	idx  *indexer.Indexer
	cfg  *config.Config
	mux  http.Handler
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
		RequestTimeout: 10 * time.Second,
		TxTimeout:      90 * time.Second,
	}

	idx := indexer.New(db, mock, cfg.FactoryAddress)
	mux := NewRouter(db, mock, idx, cfg, nil)

	return &testEnv{db: db, mock: mock, idx: idx, cfg: cfg, mux: mux}
}

func setupWithEmergency(t *testing.T) *testEnv {
	t.Helper()

	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mock := chain.NewMockClient()
	cfg := &config.Config{
		ChainID:          84532,
		FactoryAddress:   "0xFactoryAddr",
		RequestTimeout:   10 * time.Second,
		TxTimeout:        90 * time.Second,
		EmergencyEnabled: true,
	}

	idx := indexer.New(db, mock, cfg.FactoryAddress)
	mux := NewRouter(db, mock, idx, cfg, nil)

	return &testEnv{db: db, mock: mock, idx: idx, cfg: cfg, mux: mux}
}

func (e *testEnv) request(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, http.NoBody)
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
	escrow, err := env.db.GetEscrow(context.Background(), escrowID)
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
		"buyer": "0x1000000000000000000000000000000000000001",
		"worker": "0x2000000000000000000000000000000000000002",
		"verifier": "0x3000000000000000000000000000000000000003",
		"arbitrator": "0x4000000000000000000000000000000000000004",
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
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "desc", "0xabc")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
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
	escrowData, ok := resp["escrow"].(map[string]any)
	if !ok {
		t.Fatalf("expected escrow object in response, got %v", resp)
	}
	if escrowData["Buyer"] != "0xB" {
		t.Fatalf("expected buyer 0xB, got %v", escrowData["Buyer"])
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
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF", EscrowAddress: "0xE1",
		Buyer: "0xB1", Worker: "0xW1", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "created",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
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
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF", EscrowAddress: "0xE1",
		Buyer: "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "created",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
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
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr",
		Buyer:         "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
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
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr",
		Buyer:         "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
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
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr",
		Buyer:         "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
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
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr",
		Buyer:         "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
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
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr",
		Buyer:         "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
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
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	escrow, err := env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr1",
		Buyer:         "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
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
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	escrow, err := env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr2",
		Buyer:         "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
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
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	escrow, err := env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr3",
		Buyer:         "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
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
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr",
		Buyer:         "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
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
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr",
		Buyer:         "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
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

func TestCORS_Preflight_WildcardDefault(t *testing.T) {
	env := setup(t)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/health", http.NoBody)
	rr := httptest.NewRecorder()
	env.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("expected wildcard CORS header, got %q", rr.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORS_ExplicitWildcard(t *testing.T) {
	env := setup(t)
	env.cfg.CORSOrigins = []string{"*"}
	env.mux = NewRouter(env.db, env.mock, env.idx, env.cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", http.NoBody)
	req.Header.Set("Origin", "https://anything.example.com")
	rr := httptest.NewRecorder()
	env.mux.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected wildcard CORS header when origins=[\"*\"], got %q", got)
	}
}

func TestCORS_RestrictedOrigins_Allowed(t *testing.T) {
	env := setup(t)
	env.cfg.CORSOrigins = []string{"https://example.com", "https://app.example.com"}
	env.mux = NewRouter(env.db, env.mock, env.idx, env.cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", http.NoBody)
	req.Header.Set("Origin", "https://example.com")
	rr := httptest.NewRecorder()
	env.mux.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Fatalf("expected origin echo, got %q", got)
	}
	if rr.Header().Get("Vary") != "Origin" {
		t.Fatal("expected Vary: Origin header for restricted CORS")
	}
}

func TestCORS_RestrictedOrigins_Rejected(t *testing.T) {
	env := setup(t)
	env.cfg.CORSOrigins = []string{"https://example.com"}
	env.mux = NewRouter(env.db, env.mock, env.idx, env.cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", http.NoBody)
	req.Header.Set("Origin", "https://evil.com")
	rr := httptest.NewRecorder()
	env.mux.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no CORS header for rejected origin, got %q", got)
	}
}

func TestCORS_RestrictedOrigins_Preflight(t *testing.T) {
	env := setup(t)
	env.cfg.CORSOrigins = []string{"https://app.example.com"}
	env.mux = NewRouter(env.db, env.mock, env.idx, env.cfg, nil)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/escrows", http.NoBody)
	req.Header.Set("Origin", "https://app.example.com")
	rr := httptest.NewRecorder()
	env.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("expected origin echo on preflight, got %q", got)
	}
}

func TestTimeout_GET_Exceeded(t *testing.T) {
	env := setup(t)
	env.mock.BlockNum = 1

	// Reconfigure with a very short read timeout
	env.cfg.RequestTimeout = 50 * time.Millisecond
	env.cfg.TxTimeout = 90 * time.Second
	env.mux = NewRouter(env.db, env.mock, env.idx, env.cfg, nil)

	// Delay longer than the read timeout
	env.mock.Delay = 200 * time.Millisecond

	rr := env.request(t, "GET", "/api/v1/health", "")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on timeout, got %d: %s", rr.Code, rr.Body.String())
	}

	if !strings.Contains(rr.Body.String(), "request timeout") {
		t.Fatalf("expected timeout error body, got: %s", rr.Body.String())
	}
}

func TestTimeout_GET_WithinLimit(t *testing.T) {
	env := setup(t)
	env.mock.BlockNum = 99

	env.cfg.RequestTimeout = 2 * time.Second
	env.cfg.TxTimeout = 90 * time.Second
	env.mux = NewRouter(env.db, env.mock, env.idx, env.cfg, nil)

	rr := env.request(t, "GET", "/api/v1/health", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestTimeout_POST_UsesLongerTxTimeout(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr",
		Buyer:         "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "1000000000000000000", Status: "created",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	// Short read timeout but long tx timeout; delay is between them
	env.cfg.RequestTimeout = 50 * time.Millisecond
	env.cfg.TxTimeout = 2 * time.Second
	env.mux = NewRouter(env.db, env.mock, env.idx, env.cfg, nil)

	env.mock.Delay = 100 * time.Millisecond

	rr := env.request(t, "POST", "/api/v1/escrows/1/fund", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (within tx timeout), got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestTimeout_POST_TxTimeoutExceeded(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0xEscrowAddr",
		Buyer:         "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "1000000000000000000", Status: "created",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	env.cfg.RequestTimeout = 50 * time.Millisecond
	env.cfg.TxTimeout = 50 * time.Millisecond
	env.mux = NewRouter(env.db, env.mock, env.idx, env.cfg, nil)

	env.mock.Delay = 200 * time.Millisecond

	rr := env.request(t, "POST", "/api/v1/escrows/1/fund", "")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on tx timeout, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCreateEscrow_BelowComplexityFloor(t *testing.T) {
	env := setup(t)
	env.cfg.ComplexityFloor = "1000000000000000000" // 1 ETH
	env.mux = NewRouter(env.db, env.mock, env.idx, env.cfg, nil)

	body := `{
		"title": "Test", "description": "x",
		"buyer": "0x1000000000000000000000000000000000000001",
		"worker": "0x2000000000000000000000000000000000000002",
		"verifier": "0x3000000000000000000000000000000000000003",
		"arbitrator": "0x4000000000000000000000000000000000000004",
		"amount": "999999999999999999",
		"submission_deadline": "1700000000",
		"review_period_seconds": "86400", "dispute_period_seconds": "172800",
		"arbitrator_timeout_seconds": "604800"
	}`

	rr := env.request(t, "POST", "/api/v1/escrows", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decodeJSON(t, rr)
	errMsg, _ := resp["error"].(string)
	if !strings.Contains(errMsg, "complexity floor") {
		t.Fatalf("expected complexity floor error, got: %s", errMsg)
	}
}

func TestCreateEscrow_AtComplexityFloor(t *testing.T) {
	env := setup(t)
	env.cfg.ComplexityFloor = "100"
	env.mux = NewRouter(env.db, env.mock, env.idx, env.cfg, nil)

	escrowAddr := common.HexToAddress("0xABCDEF1234567890ABCDEF1234567890ABCDEF12")
	buyerAddr := common.HexToAddress("0x1000000000000000000000000000000000000001")
	env.mock.Receipt = chain.MakeEscrowCreatedReceipt(0, escrowAddr, buyerAddr)

	body := `{
		"title": "Test", "description": "x",
		"buyer": "0x1000000000000000000000000000000000000001",
		"worker": "0x2000000000000000000000000000000000000002",
		"verifier": "0x3000000000000000000000000000000000000003",
		"arbitrator": "0x4000000000000000000000000000000000000004",
		"amount": "100",
		"submission_deadline": "1700000000",
		"review_period_seconds": "86400", "dispute_period_seconds": "172800",
		"arbitrator_timeout_seconds": "604800"
	}`

	rr := env.request(t, "POST", "/api/v1/escrows", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCreateEscrow_EmptyComplexityFloorAllowsAny(t *testing.T) {
	env := setup(t)
	env.cfg.ComplexityFloor = ""
	env.mux = NewRouter(env.db, env.mock, env.idx, env.cfg, nil)

	escrowAddr := common.HexToAddress("0xABCDEF1234567890ABCDEF1234567890ABCDEF12")
	buyerAddr := common.HexToAddress("0x1000000000000000000000000000000000000001")
	env.mock.Receipt = chain.MakeEscrowCreatedReceipt(0, escrowAddr, buyerAddr)

	body := `{
		"title": "Test", "description": "x",
		"buyer": "0x1000000000000000000000000000000000000001",
		"worker": "0x2000000000000000000000000000000000000002",
		"verifier": "0x3000000000000000000000000000000000000003",
		"arbitrator": "0x4000000000000000000000000000000000000004",
		"amount": "1",
		"submission_deadline": "1700000000",
		"review_period_seconds": "86400", "dispute_period_seconds": "172800",
		"arbitrator_timeout_seconds": "604800"
	}`

	rr := env.request(t, "POST", "/api/v1/escrows", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
}

// RFQ Bidding Protocol Tests

func futureTimestamp() string {
	return strconv.FormatInt(time.Now().Unix()+86400, 10)
}

func farFutureTimestamp() string {
	return strconv.FormatInt(time.Now().Unix()+172800, 10)
}

func TestCreateRFQ_Success(t *testing.T) {
	env := setup(t)

	body := fmt.Sprintf(`{
		"title": "Build a widget",
		"description": "Build a high-quality widget",
		"buyer": "0x1000000000000000000000000000000000000001",
		"budget_min": "100",
		"budget_max": "500",
		"deadline": "%s",
		"review_period_seconds": "86400",
		"dispute_period_seconds": "172800",
		"arbitrator_timeout_seconds": "604800",
		"verifier": "0x3000000000000000000000000000000000000003",
		"arbitrator": "0x4000000000000000000000000000000000000004",
		"expires_at": "%s"
	}`, futureTimestamp(), farFutureTimestamp())

	rr := env.request(t, "POST", "/api/v1/rfqs", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["status"] != "open" {
		t.Fatalf("expected status 'open', got %v", resp["status"])
	}
	if resp["title"] != "Build a widget" {
		t.Fatalf("expected title 'Build a widget', got %v", resp["title"])
	}
}

func TestCreateRFQ_InvalidBudget(t *testing.T) {
	env := setup(t)

	body := fmt.Sprintf(`{
		"title": "Test",
		"description": "desc",
		"buyer": "0x1000000000000000000000000000000000000001",
		"budget_min": "500",
		"budget_max": "100",
		"deadline": "%s",
		"review_period_seconds": "86400",
		"dispute_period_seconds": "172800",
		"arbitrator_timeout_seconds": "604800",
		"expires_at": "%s"
	}`, futureTimestamp(), farFutureTimestamp())

	rr := env.request(t, "POST", "/api/v1/rfqs", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestListRFQs_Success(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	_, err := env.db.CreateRFQ(ctx, &storage.RFQ{
		Title: "RFQ 1", Description: "desc", SpecHash: "0x1",
		Buyer: "0xBuyer", BudgetMin: "100", BudgetMax: "500",
		Deadline: 1800000000, ReviewPeriodSeconds: 86400,
		DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: 1900000000,
		MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("setup rfq: %v", err)
	}

	rr := env.request(t, "GET", "/api/v1/rfqs", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var rfqs []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&rfqs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rfqs) != 1 {
		t.Fatalf("expected 1 rfq, got %d", len(rfqs))
	}
}

func TestListRFQs_FilterByStatus(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	_, err := env.db.CreateRFQ(ctx, &storage.RFQ{
		Title: "RFQ 1", Description: "desc", SpecHash: "0x1",
		Buyer: "0xBuyer", BudgetMin: "100", BudgetMax: "500",
		Deadline: 1800000000, ReviewPeriodSeconds: 86400,
		DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: 1900000000,
		MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("setup rfq: %v", err)
	}
	_, err = env.db.CreateRFQ(ctx, &storage.RFQ{
		Title: "RFQ 2", Description: "desc", SpecHash: "0x2",
		Buyer: "0xBuyer", BudgetMin: "200", BudgetMax: "600",
		Deadline: 1800000000, ReviewPeriodSeconds: 86400,
		DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "closed", ExpiresAt: 1900000000,
		MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("setup rfq: %v", err)
	}

	rr := env.request(t, "GET", "/api/v1/rfqs?status=open", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var rfqs []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&rfqs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rfqs) != 1 {
		t.Fatalf("expected 1 open rfq, got %d", len(rfqs))
	}
}

func TestGetRFQ_Success(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	rfq, err := env.db.CreateRFQ(ctx, &storage.RFQ{
		Title: "RFQ 1", Description: "desc", SpecHash: "0x1",
		Buyer: "0xBuyer", BudgetMin: "100", BudgetMax: "500",
		Deadline: 1800000000, ReviewPeriodSeconds: 86400,
		DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: 1900000000,
		MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("setup rfq: %v", err)
	}

	rr := env.request(t, "GET", fmt.Sprintf("/api/v1/rfqs/%d", rfq.ID), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	rfqData, ok := resp["rfq"].(map[string]any)
	if !ok {
		t.Fatalf("expected rfq object in response, got %v", resp)
	}
	if rfqData["title"] != "RFQ 1" {
		t.Fatalf("expected title 'RFQ 1', got %v", rfqData["title"])
	}
}

func TestGetRFQ_NotFound(t *testing.T) {
	env := setup(t)

	rr := env.request(t, "GET", "/api/v1/rfqs/999", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestCancelRFQ_Success(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	rfq, err := env.db.CreateRFQ(ctx, &storage.RFQ{
		Title: "RFQ 1", Description: "desc", SpecHash: "0x1",
		Buyer: "0xBuyer", BudgetMin: "100", BudgetMax: "500",
		Deadline: 1800000000, ReviewPeriodSeconds: 86400,
		DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: 1900000000,
		MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("setup rfq: %v", err)
	}

	rr := env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/cancel", rfq.ID), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["status"] != "cancelled" {
		t.Fatalf("expected status 'cancelled', got %v", resp["status"])
	}
}

func TestCancelRFQ_AlreadyClosed(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	rfq, err := env.db.CreateRFQ(ctx, &storage.RFQ{
		Title: "RFQ", Description: "desc", SpecHash: "0x1",
		Buyer: "0xBuyer", BudgetMin: "100", BudgetMax: "500",
		Deadline: 1800000000, ReviewPeriodSeconds: 86400,
		DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "closed", ExpiresAt: 1900000000,
		MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("setup rfq: %v", err)
	}

	rr := env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/cancel", rfq.ID), "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestPlaceBid_Success(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	rfq, err := env.db.CreateRFQ(ctx, &storage.RFQ{
		Title: "RFQ", Description: "desc", SpecHash: "0x1",
		Buyer:     "0x1000000000000000000000000000000000000001",
		BudgetMin: "100", BudgetMax: "500",
		Deadline: time.Now().Unix() + 86400, ReviewPeriodSeconds: 86400,
		DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: time.Now().Unix() + 172800,
		MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("setup rfq: %v", err)
	}

	body := fmt.Sprintf(`{
		"bidder": "0x2000000000000000000000000000000000000002",
		"amount": "300",
		"estimated_duration": 3600,
		"message": "I can do this",
		"expires_at": "%s"
	}`, futureTimestamp())

	rr := env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/bids", rfq.ID), body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["status"] != "pending" {
		t.Fatalf("expected status 'pending', got %v", resp["status"])
	}
}

func TestPlaceBid_OutOfBudgetRange(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	rfq, err := env.db.CreateRFQ(ctx, &storage.RFQ{
		Title: "RFQ", Description: "desc", SpecHash: "0x1",
		Buyer:     "0x1000000000000000000000000000000000000001",
		BudgetMin: "100", BudgetMax: "500",
		Deadline: time.Now().Unix() + 86400, ReviewPeriodSeconds: 86400,
		DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: time.Now().Unix() + 172800,
		MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("setup rfq: %v", err)
	}

	body := fmt.Sprintf(`{
		"bidder": "0x2000000000000000000000000000000000000002",
		"amount": "999",
		"expires_at": "%s"
	}`, futureTimestamp())

	rr := env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/bids", rfq.ID), body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestPlaceBid_BidderIsBuyer(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	rfq, err := env.db.CreateRFQ(ctx, &storage.RFQ{
		Title: "RFQ", Description: "desc", SpecHash: "0x1",
		Buyer:     "0x1000000000000000000000000000000000000001",
		BudgetMin: "100", BudgetMax: "500",
		Deadline: time.Now().Unix() + 86400, ReviewPeriodSeconds: 86400,
		DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: time.Now().Unix() + 172800,
		MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("setup rfq: %v", err)
	}

	body := fmt.Sprintf(`{
		"bidder": "0x1000000000000000000000000000000000000001",
		"amount": "300",
		"expires_at": "%s"
	}`, futureTimestamp())

	rr := env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/bids", rfq.ID), body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestListBids_Success(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	rfq, err := env.db.CreateRFQ(ctx, &storage.RFQ{
		Title: "RFQ", Description: "desc", SpecHash: "0x1",
		Buyer: "0xBuyer", BudgetMin: "100", BudgetMax: "500",
		Deadline: 1800000000, ReviewPeriodSeconds: 86400,
		DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: 1900000000,
		MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("setup rfq: %v", err)
	}

	_, err = env.db.CreateBid(ctx, &storage.Bid{
		RFQID: rfq.ID, Bidder: "0xWorker", Amount: "200",
		Status: "pending", ExpiresAt: 1850000000, MilestonesJSON: "[]",
	})
	if err != nil {
		t.Fatalf("setup bid: %v", err)
	}

	rr := env.request(t, "GET", fmt.Sprintf("/api/v1/rfqs/%d/bids", rfq.ID), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var bids []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&bids); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(bids) != 1 {
		t.Fatalf("expected 1 bid, got %d", len(bids))
	}
}

func TestAcceptBid_Success(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	escrowAddr := common.HexToAddress("0xABCDEF1234567890ABCDEF1234567890ABCDEF12")
	buyer := common.HexToAddress("0x1000000000000000000000000000000000000001")
	env.mock.Receipt = chain.MakeEscrowCreatedReceipt(7, escrowAddr, buyer)

	rfq, err := env.db.CreateRFQ(ctx, &storage.RFQ{
		Title: "Build widget", Description: "Build a high-quality widget", SpecHash: "0x1",
		Buyer:     "0x1000000000000000000000000000000000000001",
		BudgetMin: "100", BudgetMax: "500",
		Verifier:   "0x3000000000000000000000000000000000000003",
		Arbitrator: "0x4000000000000000000000000000000000000004",
		Deadline:   time.Now().Unix() + 86400, ReviewPeriodSeconds: 86400,
		DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: time.Now().Unix() + 172800,
		MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("setup rfq: %v", err)
	}

	bid, err := env.db.CreateBid(ctx, &storage.Bid{
		RFQID: rfq.ID, Bidder: "0x2000000000000000000000000000000000000002",
		Amount: "300", Status: "pending", ExpiresAt: time.Now().Unix() + 86400,
		MilestonesJSON: "[]",
	})
	if err != nil {
		t.Fatalf("setup bid: %v", err)
	}

	body := fmt.Sprintf(`{"bid_id": %d, "caller": "0x1000000000000000000000000000000000000001"}`, bid.ID)
	rr := env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/accept", rfq.ID), body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["escrow_address"] != escrowAddr.Hex() {
		t.Fatalf("expected escrow address %s, got %v", escrowAddr.Hex(), resp["escrow_address"])
	}
	if resp["bid_status"] != "accepted" {
		t.Fatalf("expected bid_status 'accepted', got %v", resp["bid_status"])
	}

	// Verify RFQ is now closed
	updatedRFQ, err := env.db.GetRFQ(ctx, rfq.ID)
	if err != nil {
		t.Fatalf("get rfq: %v", err)
	}
	if updatedRFQ.Status != "closed" {
		t.Fatalf("expected rfq status 'closed', got %q", updatedRFQ.Status)
	}
}

func TestAcceptBid_RFQNotOpen(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	rfq, err := env.db.CreateRFQ(ctx, &storage.RFQ{
		Title: "RFQ", Description: "desc", SpecHash: "0x1",
		Buyer: "0xBuyer", BudgetMin: "100", BudgetMax: "500",
		Deadline: 1800000000, ReviewPeriodSeconds: 86400,
		DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "closed", ExpiresAt: 1900000000,
		MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("setup rfq: %v", err)
	}

	rr := env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/accept", rfq.ID), `{"bid_id": 1}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAcceptBid_RejectsOtherBids(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	escrowAddr := common.HexToAddress("0xABCDEF1234567890ABCDEF1234567890ABCDEF12")
	buyer := common.HexToAddress("0x1000000000000000000000000000000000000001")
	env.mock.Receipt = chain.MakeEscrowCreatedReceipt(7, escrowAddr, buyer)

	rfq, err := env.db.CreateRFQ(ctx, &storage.RFQ{
		Title: "Build widget", Description: "desc", SpecHash: "0x1",
		Buyer:     "0x1000000000000000000000000000000000000001",
		BudgetMin: "100", BudgetMax: "500",
		Verifier:   "0x3000000000000000000000000000000000000003",
		Arbitrator: "0x4000000000000000000000000000000000000004",
		Deadline:   time.Now().Unix() + 86400, ReviewPeriodSeconds: 86400,
		DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: time.Now().Unix() + 172800,
		MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("setup rfq: %v", err)
	}

	bid1, err := env.db.CreateBid(ctx, &storage.Bid{
		RFQID: rfq.ID, Bidder: "0x2000000000000000000000000000000000000002",
		Amount: "200", Status: "pending", ExpiresAt: time.Now().Unix() + 86400,
		MilestonesJSON: "[]",
	})
	if err != nil {
		t.Fatalf("setup bid 1: %v", err)
	}
	bid2, err := env.db.CreateBid(ctx, &storage.Bid{
		RFQID: rfq.ID, Bidder: "0x5000000000000000000000000000000000000005",
		Amount: "300", Status: "pending", ExpiresAt: time.Now().Unix() + 86400,
		MilestonesJSON: "[]",
	})
	if err != nil {
		t.Fatalf("setup bid 2: %v", err)
	}

	body := fmt.Sprintf(`{"bid_id": %d, "caller": "0x1000000000000000000000000000000000000001"}`, bid1.ID)
	rr := env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/accept", rfq.ID), body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify bid2 was rejected
	rejectedBid, err := env.db.GetBid(ctx, bid2.ID)
	if err != nil {
		t.Fatalf("get bid 2: %v", err)
	}
	if rejectedBid.Status != "rejected" {
		t.Fatalf("expected bid 2 status 'rejected', got %q", rejectedBid.Status)
	}
}

func escrowPath(id int64, action string) string {
	if action != "" {
		return fmt.Sprintf("/api/v1/escrows/%d/%s", id, action)
	}
	return fmt.Sprintf("/api/v1/escrows/%d", id)
}

// Emergency response protocol tests (paper §4.9)

func TestFreezeAddress_OK(t *testing.T) {
	env := setupWithEmergency(t)

	body := `{"address":"0x1111111111111111111111111111111111111111"}`
	rr := env.request(t, "POST", "/api/v1/emergency/freeze-address", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["tx_hash"] == nil || resp["tx_hash"] == "" {
		t.Fatal("expected non-empty tx_hash")
	}
	if resp["address"] != "0x1111111111111111111111111111111111111111" {
		t.Fatalf("expected address in response, got %v", resp["address"])
	}
}

func TestUnfreezeAddress_OK(t *testing.T) {
	env := setupWithEmergency(t)

	body := `{"address":"0x1111111111111111111111111111111111111111"}`
	rr := env.request(t, "POST", "/api/v1/emergency/unfreeze-address", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["tx_hash"] == nil || resp["tx_hash"] == "" {
		t.Fatal("expected non-empty tx_hash")
	}
}

func TestFreezeAddress_InvalidAddress(t *testing.T) {
	env := setupWithEmergency(t)

	body := `{"address":"not-a-valid-address"}`
	rr := env.request(t, "POST", "/api/v1/emergency/freeze-address", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestFreezeEscrow_OK(t *testing.T) {
	env := setupWithEmergency(t)
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Test", "", "0x123")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xFactory",
		EscrowAddress: "0xEscrow1", EscrowID: 1, Buyer: "0xBuyer",
		Worker: "0xWorker", Verifier: "0xVerifier", Arbitrator: "0xArbitrator",
		Amount: "1000000000000000000", Status: "funded",
		SubmissionDeadline: 1700000000, ReviewPeriodSeconds: 86400,
		DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	body := `{"escrow_id":1}`
	rr := env.request(t, "POST", "/api/v1/emergency/freeze-escrow", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["tx_hash"] == nil || resp["tx_hash"] == "" {
		t.Fatal("expected non-empty tx_hash")
	}
	if resp["escrow_id"].(float64) != 1 {
		t.Fatalf("expected escrow_id 1, got %v", resp["escrow_id"])
	}
}

func TestUnfreezeEscrow_OK(t *testing.T) {
	env := setupWithEmergency(t)
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Test", "", "0x123")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xFactory",
		EscrowAddress: "0xEscrow1", EscrowID: 1, Buyer: "0xBuyer",
		Worker: "0xWorker", Verifier: "0xVerifier", Arbitrator: "0xArbitrator",
		Amount: "1000000000000000000", Status: "funded", Frozen: true,
		SubmissionDeadline: 1700000000, ReviewPeriodSeconds: 86400,
		DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	body := `{"escrow_id":1}`
	rr := env.request(t, "POST", "/api/v1/emergency/unfreeze-escrow", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["tx_hash"] == nil || resp["tx_hash"] == "" {
		t.Fatal("expected non-empty tx_hash")
	}
	if resp["escrow_id"].(float64) != 1 {
		t.Fatalf("expected escrow_id 1, got %v", resp["escrow_id"])
	}
}

func TestEmergencyResolve_OK(t *testing.T) {
	env := setupWithEmergency(t)
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Test", "", "0x123")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xFactory",
		EscrowAddress: "0xEscrow1", EscrowID: 1, Buyer: "0xBuyer",
		Worker: "0xWorker", Verifier: "0xVerifier", Arbitrator: "0xArbitrator",
		Amount: "1000000000000000000", Status: "funded",
		SubmissionDeadline: 1700000000, ReviewPeriodSeconds: 86400,
		DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	body := `{"escrow_id":1,"worker_award_bps":5000}`
	rr := env.request(t, "POST", "/api/v1/emergency/resolve", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["tx_hash"] == nil || resp["tx_hash"] == "" {
		t.Fatal("expected non-empty tx_hash")
	}
	if resp["worker_award_bps"].(float64) != 5000 {
		t.Fatalf("expected worker_award_bps 5000, got %v", resp["worker_award_bps"])
	}
}

func TestEmergencyResolve_InvalidBps(t *testing.T) {
	env := setupWithEmergency(t)
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Test", "", "0x123")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xFactory",
		EscrowAddress: "0xEscrow1", EscrowID: 1, Buyer: "0xBuyer",
		Worker: "0xWorker", Verifier: "0xVerifier", Arbitrator: "0xArbitrator",
		Amount: "1000000000000000000", Status: "funded",
		SubmissionDeadline: 1700000000, ReviewPeriodSeconds: 86400,
		DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	body := `{"escrow_id":1,"worker_award_bps":10001}`
	rr := env.request(t, "POST", "/api/v1/emergency/resolve", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestListFrozenAddresses_OK(t *testing.T) {
	env := setupWithEmergency(t)

	rr := env.request(t, "GET", "/api/v1/emergency/frozen-addresses", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	// Empty list may be encoded as [] or null
	if addrs, ok := resp["frozen_addresses"].([]any); ok && len(addrs) != 0 {
		t.Fatalf("expected empty list initially, got %d addresses", len(addrs))
	}
	if resp["count"].(float64) != 0 {
		t.Fatalf("expected count 0, got %v", resp["count"])
	}
}

func TestListEmergencyActions_OK(t *testing.T) {
	env := setupWithEmergency(t)

	rr := env.request(t, "GET", "/api/v1/emergency/actions", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["count"] == nil {
		t.Fatal("expected count in response")
	}
}

func TestEmergencyEndpoints_FreezeAddress_Disabled(t *testing.T) {
	env := setup(t)
	env.cfg.EmergencyEnabled = false
	env.mux = NewRouter(env.db, env.mock, env.idx, env.cfg, nil)

	body := `{"address":"0x1111111111111111111111111111111111111111"}`
	rr := env.request(t, "POST", "/api/v1/emergency/freeze-address", body)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when emergency disabled, got %d: %s", rr.Code, rr.Body.String())
	}
}
