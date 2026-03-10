package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/attestation"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	escrowservice "github.com/eddiefleurent/agent-escrow/go-server/internal/escrow"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
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
		ChainID:                 84532,
		FactoryAddress:          "0x0000000000000000000000000000000000000001",
		RequestTimeout:          10 * time.Second,
		TxTimeout:               90 * time.Second,
		ReputationDampingFactor: 0.9,
	}

	idx := indexer.New(db, mock, cfg.FactoryAddress)
	mux := NewRouter(db, mock, idx, cfg, nil)

	return &testEnv{db: db, mock: mock, idx: idx, cfg: cfg, mux: mux}
}

func setupWithEmergency(t *testing.T) *testEnv {
	t.Helper()

	env := setup(t)
	env.cfg.EmergencyEnabled = true
	env.mux = NewRouter(env.db, env.mock, env.idx, env.cfg, nil)
	return env
}

func setupWithUCP(t *testing.T) *testEnv {
	t.Helper()
	env := setup(t)
	env.cfg.UCPEnabled = true
	env.cfg.UCPBaseURL = "http://localhost:8080"
	env.cfg.UCPProviderName = "Test UCP Provider"
	env.mux = NewRouter(env.db, env.mock, env.idx, env.cfg, nil)
	return env
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

func makeValidCompletionAttestationChainJSON(t *testing.T) string {
	t.Helper()

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate attestation key: %v", err)
	}
	now := time.Now().Unix()
	att := attestation.CompletionAttestation{
		Profile:      attestation.CompletionAttestationV1,
		LinkID:       "link-1",
		FromAddress:  crypto.PubkeyToAddress(key.PublicKey).Hex(),
		ToAddress:    "0x0000000000000000000000000000000000000002",
		TaskSpecHash: "0xabc",
		OutcomeHash:  "0xdef",
		IssuedAt:     now - 60,
		ExpiresAt:    now + 3600,
		Nonce:        "n1",
	}
	msgHash := crypto.Keccak256Hash([]byte(attestation.CanonicalCompletionMessage(&att)))
	sig, err := crypto.Sign(msgHash.Bytes(), key)
	if err != nil {
		t.Fatalf("sign attestation: %v", err)
	}
	att.Signature = "0x" + hex.EncodeToString(sig)

	chainJSONBytes, err := json.Marshal([]attestation.CompletionAttestation{att})
	if err != nil {
		t.Fatalf("marshal attestation chain: %v", err)
	}
	return string(chainJSONBytes)
}

func createSealedRFQFixture(t *testing.T, env *testEnv, now, commitDeadline, revealDeadline int64) *storage.RFQ {
	t.Helper()

	rfq, err := env.db.CreateRFQ(context.Background(), &storage.RFQ{
		Title: "RFQ", Description: "desc", SpecHash: "0x1",
		Buyer:     "0x1000000000000000000000000000000000000001",
		BudgetMin: "100", BudgetMax: "500",
		Deadline: now + 86400, ReviewPeriodSeconds: 86400,
		DisputePeriodSeconds: 172800, ArbitratorTimeoutSeconds: 604800,
		Status: "open", ExpiresAt: now + 172800,
		BiddingMode: "sealed", CommitDeadline: commitDeadline, RevealDeadline: revealDeadline,
		MilestonesJSON: "[]", RequirementsJSON: "{}",
	})
	if err != nil {
		t.Fatalf("setup rfq: %v", err)
	}
	return rfq
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
		"verifier_panel": ["0x3000000000000000000000000000000000000003"],
		"quorum_threshold": 1,
		"quorum_verifier_count": 1,
		"verifier_stake_per_verifier": "0",
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
	if escrow.QuorumThreshold != 1 {
		t.Fatalf("db quorum_threshold: expected 1, got %d", escrow.QuorumThreshold)
	}
	if escrow.QuorumVerifierCount != 1 {
		t.Fatalf("db quorum_verifier_count: expected 1, got %d", escrow.QuorumVerifierCount)
	}
	if escrow.VerifierStakePerVerifier != "0" {
		t.Fatalf("db verifier_stake_per_verifier: expected 0, got %s", escrow.VerifierStakePerVerifier)
	}
	expectedPanel := strings.ToLower("0x3000000000000000000000000000000000000003")
	if !strings.Contains(strings.ToLower(escrow.VerifierPanelJSON), expectedPanel) {
		t.Fatalf("db verifier_panel_json: expected to contain %s, got %s", expectedPanel, escrow.VerifierPanelJSON)
	}
}

func TestCreateEscrow_ChainError(t *testing.T) {
	env := setup(t)
	env.mock.CreateEscrowErr = http.ErrServerClosed

	body := `{
		"title": "Test", "description": "x",
		"buyer": "0x1000000000000000000000000000000000000001",
		"worker": "0x2000000000000000000000000000000000000002",
		"verifier_panel": ["0x3000000000000000000000000000000000000003"],
		"quorum_threshold": 1,
		"quorum_verifier_count": 1,
		"verifier_stake_per_verifier": "0",
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

func TestCreateEscrow_DuplicateCoreRolesRejectedByService(t *testing.T) {
	env := setup(t)

	body := `{
		"title": "Test", "description": "x",
		"buyer": "0x1000000000000000000000000000000000000001",
		"worker": "0x1000000000000000000000000000000000000001",
		"verifier_panel": ["0x3000000000000000000000000000000000000003"],
		"quorum_threshold": 1,
		"quorum_verifier_count": 1,
		"verifier_stake_per_verifier": "0",
		"arbitrator": "0x4000000000000000000000000000000000000004",
		"amount": "100", "submission_deadline": "1700000000",
		"review_period_seconds": "86400", "dispute_period_seconds": "172800",
		"arbitrator_timeout_seconds": "604800"
	}`

	rr := env.request(t, "POST", "/api/v1/escrows", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decodeJSON(t, rr)
	msg, _ := resp["error"].(string)
	if !strings.Contains(msg, "buyer and worker must be distinct addresses") {
		t.Fatalf("expected duplicate core role validation message, got: %s", msg)
	}
	if len(env.mock.SentTxs) != 0 {
		t.Fatalf("expected no on-chain create submission, got %d tx records", len(env.mock.SentTxs))
	}
}

func TestCreateEscrow_RetryInFlightPendingDoesNotResubmit(t *testing.T) {
	env := setup(t)
	env.mock.CreateEscrowErr = errors.New("transient send failure")

	body := `{
		"title": "In-flight Retry", "description": "x",
		"buyer": "0x1000000000000000000000000000000000000001",
		"worker": "0x2000000000000000000000000000000000000002",
		"verifier_panel": ["0x3000000000000000000000000000000000000003"],
		"quorum_threshold": 1,
		"quorum_verifier_count": 1,
		"verifier_stake_per_verifier": "0",
		"arbitrator": "0x4000000000000000000000000000000000000004",
		"amount": "100", "submission_deadline": "1700000000",
		"review_period_seconds": "86400", "dispute_period_seconds": "172800",
		"arbitrator_timeout_seconds": "604800"
	}`

	first := env.request(t, "POST", "/api/v1/escrows", body)
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("expected first attempt 500, got %d: %s", first.Code, first.Body.String())
	}

	ctx := context.Background()
	escrows, err := env.db.ListEscrows(ctx, "", "", "")
	if err != nil {
		t.Fatalf("list escrows: %v", err)
	}
	if len(escrows) != 1 {
		t.Fatalf("expected one pending escrow record, got %d", len(escrows))
	}
	if err := env.db.UpdateEscrowStatus(ctx, escrows[0].ID, "submitting"); err != nil {
		t.Fatalf("set in-flight submitting status: %v", err)
	}

	env.mock.CreateEscrowErr = nil
	env.mock.Receipt = chain.MakeEscrowCreatedReceipt(
		1,
		common.HexToAddress("0xABCDEF1234567890ABCDEF1234567890ABCDEF12"),
		common.HexToAddress("0x1000000000000000000000000000000000000001"),
	)
	second := env.request(t, "POST", "/api/v1/escrows", body)
	if second.Code != http.StatusInternalServerError {
		t.Fatalf("expected second attempt 500 while in-flight, got %d: %s", second.Code, second.Body.String())
	}
	if len(env.mock.SentTxs) != 0 {
		t.Fatalf("expected no createEscrow re-submission, got %d tx records", len(env.mock.SentTxs))
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
		EscrowAddress: "0x000000000000000000000000000000000000E3E0",
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

func TestFundEscrow_ValidationErrorReturns400(t *testing.T) {
	env := setup(t)
	env.mock.FundErr = fmt.Errorf("%w: escrow is not fundable in current state", escrowservice.ErrValidation)
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0x000000000000000000000000000000000000E3E0",
		Buyer:         "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "1000000000000000000", Status: "created",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	rr := env.request(t, "POST", "/api/v1/escrows/1/fund", "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decodeJSON(t, rr)
	msg, _ := resp["error"].(string)
	if !strings.Contains(msg, "validation error") {
		t.Fatalf("expected validation error message, got: %s", msg)
	}
}

func TestWithdrawStake_ValidationErrorReturns400(t *testing.T) {
	env := setup(t)
	env.mock.WithdrawStakeErr = fmt.Errorf("%w: stake is not yet withdrawable", escrowservice.ErrValidation)
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0x000000000000000000000000000000000000E3E0",
		Buyer:         "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "1000000000000000000", Status: "funded",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	rr := env.request(t, "POST", "/api/v1/escrows/1/withdraw-stake", "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decodeJSON(t, rr)
	msg, _ := resp["error"].(string)
	if !strings.Contains(msg, "validation error") {
		t.Fatalf("expected validation error message, got: %s", msg)
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
		EscrowAddress: "0x000000000000000000000000000000000000E3E0",
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

func TestSubmitWork_InternalFailureReturns500(t *testing.T) {
	env := setup(t)
	env.mock.SubmitErr = errors.New("rpc submit failed")
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0x000000000000000000000000000000000000E3E0",
		Buyer:         "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "funded",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	rr := env.request(t, "POST", "/api/v1/escrows/1/submit", `{"submission_uri": "ipfs://result"}`)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decodeJSON(t, rr)
	msg, _ := resp["error"].(string)
	if msg != "internal server error" {
		t.Fatalf("expected generic internal error message, got: %s", msg)
	}
}

func TestSubmitWork_InvalidProofHashDoesNotPersistAttestationChain(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	escrow, err := env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0x000000000000000000000000000000000000E3E0",
		Buyer:         "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "funded",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	reqBodyBytes, err := json.Marshal(map[string]any{
		"submission_uri":         "ipfs://result",
		"proof_hash":             "0x1",
		"attestation_chain_json": makeValidCompletionAttestationChainJSON(t),
	})
	if err != nil {
		t.Fatalf("marshal submit request: %v", err)
	}

	rr := env.request(t, "POST", "/api/v1/escrows/1/submit", string(reqBodyBytes))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}

	chains, err := env.db.GetAttestationChainsByEscrow(ctx, escrow.ID)
	if err != nil {
		t.Fatalf("list attestation chains: %v", err)
	}
	if len(chains) != 0 {
		t.Fatalf("expected no persisted attestation chains on invalid proof_hash, got %d", len(chains))
	}
}

func TestSubmitWork_SubmitFailureDoesNotPersistAttestationChain(t *testing.T) {
	env := setup(t)
	env.mock.SubmitErr = errors.New("rpc submit failed")
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	escrow, err := env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress: "0x000000000000000000000000000000000000E3E0",
		Buyer:         "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "funded",
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	reqBodyBytes, err := json.Marshal(map[string]any{
		"submission_uri":         "ipfs://result",
		"attestation_chain_json": makeValidCompletionAttestationChainJSON(t),
	})
	if err != nil {
		t.Fatalf("marshal submit request: %v", err)
	}

	rr := env.request(t, "POST", "/api/v1/escrows/1/submit", string(reqBodyBytes))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}

	chains, err := env.db.GetAttestationChainsByEscrow(ctx, escrow.ID)
	if err != nil {
		t.Fatalf("list attestation chains: %v", err)
	}
	if len(chains) != 0 {
		t.Fatalf("expected no persisted attestation chains on submit failure, got %d", len(chains))
	}
}

func TestSubmitWork_SubmitMilestoneFailureDoesNotPersistAttestationChain(t *testing.T) {
	env := setup(t)
	env.mock.SubmitMilestoneErr = errors.New("rpc submit milestone failed")
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "", "0x1")
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}
	escrow, err := env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF",
		EscrowAddress:  "0x000000000000000000000000000000000000E3E0",
		Buyer:          "0xB",
		Worker:         "0xW",
		Verifier:       "0xV",
		Arbitrator:     "0xA",
		Amount:         "100",
		Status:         "funded",
		MilestoneCount: 2,
	})
	if err != nil {
		t.Fatalf("setup escrow: %v", err)
	}

	reqBodyBytes, err := json.Marshal(map[string]any{
		"submission_uri":         "ipfs://result",
		"milestone_index":        0,
		"attestation_chain_json": makeValidCompletionAttestationChainJSON(t),
	})
	if err != nil {
		t.Fatalf("marshal submit request: %v", err)
	}

	rr := env.request(t, "POST", "/api/v1/escrows/1/submit", string(reqBodyBytes))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}

	chains, err := env.db.GetAttestationChainsByEscrow(ctx, escrow.ID)
	if err != nil {
		t.Fatalf("list attestation chains: %v", err)
	}
	if len(chains) != 0 {
		t.Fatalf("expected no persisted attestation chains on submit milestone failure, got %d", len(chains))
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
		EscrowAddress: "0x000000000000000000000000000000000000E3E0",
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
		EscrowAddress: "0x000000000000000000000000000000000000E3E0",
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
		EscrowAddress: "0x000000000000000000000000000000000000E3E0",
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
		EscrowAddress: "0x000000000000000000000000000000000000E3E1",
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
		EscrowAddress: "0x000000000000000000000000000000000000E3E2",
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
		EscrowAddress: "0x000000000000000000000000000000000000E3E3",
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
		EscrowAddress: "0x000000000000000000000000000000000000E3E0",
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
		EscrowAddress: "0x000000000000000000000000000000000000E3E0",
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
		EscrowAddress: "0x000000000000000000000000000000000000E3E0",
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
		EscrowAddress: "0x000000000000000000000000000000000000E3E0",
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
		"verifier_panel": ["0x3000000000000000000000000000000000000003"],
		"quorum_threshold": 1,
		"quorum_verifier_count": 1,
		"verifier_stake_per_verifier": "0",
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
		"verifier_panel": ["0x3000000000000000000000000000000000000003"],
		"quorum_threshold": 1,
		"quorum_verifier_count": 1,
		"verifier_stake_per_verifier": "0",
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
		"verifier_panel": ["0x3000000000000000000000000000000000000003"],
		"quorum_threshold": 1,
		"quorum_verifier_count": 1,
		"verifier_stake_per_verifier": "0",
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

func sealedBidCommitment(
	rfqID int64,
	bidder, amount string,
	estimatedDuration int64,
	reputationBond, milestonesJSON, message string,
	expiresAt int64,
	stakeMandateID, nonce, salt string,
) string {
	milestonesHash := crypto.Keccak256Hash([]byte(milestonesJSON)).Hex()
	messageHash := crypto.Keccak256Hash([]byte(message)).Hex()
	stakeMandateHash := crypto.Keccak256Hash([]byte(stakeMandateID)).Hex()
	payload := strings.Join([]string{
		"agent-escrow:sealed-bid:v1",
		strconv.FormatInt(rfqID, 10),
		strings.ToLower(bidder),
		amount,
		strconv.FormatInt(estimatedDuration, 10),
		reputationBond,
		milestonesHash,
		messageHash,
		strconv.FormatInt(expiresAt, 10),
		stakeMandateHash,
		nonce,
		salt,
	}, "|")
	return crypto.Keccak256Hash([]byte(payload)).Hex()
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
		"commit_deadline": "%s",
		"reveal_deadline": "%s",
		"expires_at": "%s"
	}`, futureTimestamp(), futureTimestamp(), futureTimestamp(), farFutureTimestamp())

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

func TestCreateRFQ_ParentCooldownReturns429(t *testing.T) {
	env := setup(t)
	env.cfg.RebidCooldownSeconds = 300
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Parent", "", "0xparent")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	parent, err := env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  env.cfg.ChainID,
		FactoryAddress:           env.cfg.FactoryAddress,
		EscrowAddress:            "0x7000000000000000000000000000000000000007",
		EscrowID:                 10,
		Buyer:                    "0x1000000000000000000000000000000000000001",
		Worker:                   "0x2000000000000000000000000000000000000002",
		Verifier:                 "0x3000000000000000000000000000000000000003",
		Arbitrator:               "0x4000000000000000000000000000000000000004",
		Amount:                   "100",
		WorkerStake:              "0",
		Token:                    "",
		Status:                   "created",
		SubmissionDeadline:       time.Now().Add(24 * time.Hour).Unix(),
		ReviewPeriodSeconds:      60,
		DisputePeriodSeconds:     60,
		ArbitratorTimeoutSeconds: 60,
		ActiveWorker:             "0x1000000000000000000000000000000000000001",
	})
	if err != nil {
		t.Fatalf("create parent escrow: %v", err)
	}

	now := time.Now().Unix()
	_, err = env.db.CreateRFQ(ctx, &storage.RFQ{
		Title:                    "existing",
		Description:              "desc",
		SpecHash:                 "0x1",
		Buyer:                    "0x1000000000000000000000000000000000000001",
		BudgetMin:                "100",
		BudgetMax:                "500",
		Deadline:                 now + 3600,
		ReviewPeriodSeconds:      60,
		DisputePeriodSeconds:     60,
		ArbitratorTimeoutSeconds: 60,
		Verifier:                 "0x3000000000000000000000000000000000000003",
		Arbitrator:               "0x4000000000000000000000000000000000000004",
		WorkerStake:              "0",
		MilestonesJSON:           "[]",
		RequirementsJSON:         "{}",
		RequiredCredentialsJSON:  "[]",
		BiddingMode:              "sealed",
		CommitDeadline:           now + 600,
		RevealDeadline:           now + 1200,
		ServiceTier:              0,
		ParentEscrowID:           &parent.ID,
		Status:                   "open",
		ExpiresAt:                now + 1800,
	})
	if err != nil {
		t.Fatalf("create existing rfq: %v", err)
	}

	body := fmt.Sprintf(`{
		"title": "new-rfq",
		"description": "desc",
		"buyer": "0x1000000000000000000000000000000000000001",
		"budget_min": "100",
		"budget_max": "500",
		"deadline": "%d",
		"review_period_seconds": "60",
		"dispute_period_seconds": "60",
		"arbitrator_timeout_seconds": "60",
		"verifier": "0x3000000000000000000000000000000000000003",
		"arbitrator": "0x4000000000000000000000000000000000000004",
		"commit_deadline": "%d",
		"reveal_deadline": "%d",
		"expires_at": "%d",
		"parent_escrow_id": %d
	}`, now+3600, now+600, now+1200, now+1800, parent.ID)

	rr := env.request(t, "POST", "/api/v1/rfqs", body)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decodeJSON(t, rr)
	retryAfterRaw, ok := resp["retry_after_seconds"]
	if !ok || retryAfterRaw == nil {
		t.Fatalf("expected retry_after_seconds in cooldown response, got %v", resp)
	}
	retryAfterSecs, ok := retryAfterRaw.(float64)
	if !ok || retryAfterSecs <= 0 {
		t.Fatalf("expected retry_after_seconds to be a positive number, got %v", retryAfterRaw)
	}
	retryAfter := rr.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatalf("expected Retry-After header to be present, got empty")
	}
	parsed, parseErr := strconv.ParseFloat(retryAfter, 64)
	if parseErr != nil || parsed < retryAfterSecs {
		t.Errorf("Retry-After header %q should be >= retry_after_seconds %.2f", retryAfter, retryAfterSecs)
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
		"commit_deadline": "%s",
		"reveal_deadline": "%s",
		"expires_at": "%s"
	}`, futureTimestamp(), futureTimestamp(), futureTimestamp(), farFutureTimestamp())

	rr := env.request(t, "POST", "/api/v1/rfqs", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCreateRFQ_MissingSealedDeadlines(t *testing.T) {
	env := setup(t)

	body := fmt.Sprintf(`{
		"title": "Test",
		"description": "desc",
		"buyer": "0x1000000000000000000000000000000000000001",
		"budget_min": "100",
		"budget_max": "500",
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
	if !strings.Contains(rr.Body.String(), "commit_deadline is required") {
		t.Fatalf("expected commit_deadline error, got: %s", rr.Body.String())
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

func TestListRFQs_DerivesRevealOpenSealedBidStatus(t *testing.T) {
	env := setup(t)

	now := time.Now().Unix()
	createSealedRFQFixture(t, env, now, now-60, now+3600)

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
	if got := rfqs[0]["sealed_bid_status"]; got != "reveal_open" {
		t.Fatalf("expected reveal_open sealed bid status, got %v", got)
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

func TestGetRFQ_DerivesRevealOpenSealedBidStatus(t *testing.T) {
	env := setup(t)

	now := time.Now().Unix()
	rfq := createSealedRFQFixture(t, env, now, now-60, now+3600)

	rr := env.request(t, "GET", fmt.Sprintf("/api/v1/rfqs/%d", rfq.ID), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	rfqData, ok := resp["rfq"].(map[string]any)
	if !ok {
		t.Fatalf("expected rfq object in response, got %v", resp)
	}
	if got := rfqData["sealed_bid_status"]; got != "reveal_open" {
		t.Fatalf("expected reveal_open sealed bid status, got %v", got)
	}
}

func TestGetRFQ_NotFound(t *testing.T) {
	env := setup(t)

	rr := env.request(t, "GET", "/api/v1/rfqs/999", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestGetRFQ_DBErrorReturns500(t *testing.T) {
	env := setup(t)
	if err := env.db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	rr := env.request(t, "GET", "/api/v1/rfqs/1", "")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
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

func TestCommitBid_Success(t *testing.T) {
	env := setup(t)
	now := time.Now().Unix()
	rfq := createSealedRFQFixture(t, env, now, now+3600, now+7200)

	body := `{
		"bidder": "0x2000000000000000000000000000000000000002",
		"commitment": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"nonce": "n1"
	}`

	rr := env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/bids/commit", rfq.ID), body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["status"] != "committed" {
		t.Fatalf("expected status 'committed', got %v", resp["status"])
	}
}

func TestCommitBid_DuplicateNonceRejected(t *testing.T) {
	env := setup(t)
	now := time.Now().Unix()
	rfq := createSealedRFQFixture(t, env, now, now+3600, now+7200)

	first := `{
		"bidder": "0x2000000000000000000000000000000000000002",
		"commitment": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"nonce": "n-dupe"
	}`
	rr := env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/bids/commit", rfq.ID), first)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected first commit 201, got %d: %s", rr.Code, rr.Body.String())
	}

	second := `{
		"bidder": "0x2000000000000000000000000000000000000002",
		"commitment": "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"nonce": "n-dupe"
	}`
	rr = env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/bids/commit", rfq.ID), second)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected duplicate nonce 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "duplicate nonce") {
		t.Fatalf("expected duplicate nonce error, got: %s", rr.Body.String())
	}
}

func TestCommitBid_ReplacesPriorCommittedBid(t *testing.T) {
	env := setup(t)
	ctx := context.Background()
	now := time.Now().Unix()
	rfq := createSealedRFQFixture(t, env, now, now+3600, now+7200)

	first := `{
		"bidder": "0x2000000000000000000000000000000000000002",
		"commitment": "0x1111111111111111111111111111111111111111111111111111111111111111",
		"nonce": "n-replace-1"
	}`
	rr := env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/bids/commit", rfq.ID), first)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected first commit 201, got %d: %s", rr.Code, rr.Body.String())
	}

	second := `{
		"bidder": "0x2000000000000000000000000000000000000002",
		"commitment": "0x2222222222222222222222222222222222222222222222222222222222222222",
		"nonce": "n-replace-2"
	}`
	rr = env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/bids/commit", rfq.ID), second)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected replacement commit 201, got %d: %s", rr.Code, rr.Body.String())
	}

	firstCommit, err := env.db.GetBidCommitByRFQBidderNonce(ctx, rfq.ID, common.HexToAddress("0x2000000000000000000000000000000000000002").Hex(), "n-replace-1")
	if err != nil {
		t.Fatalf("get first commit: %v", err)
	}
	if firstCommit.Status != "superseded" {
		t.Fatalf("expected first commit superseded, got %q", firstCommit.Status)
	}
}

func TestCommitBid_CooldownRejectedAfterNonReveal(t *testing.T) {
	env := setup(t)
	ctx := context.Background()
	now := time.Now().Unix()

	expiredRFQ := createSealedRFQFixture(t, env, now, now-120, now-60)
	_, err := env.db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:      expiredRFQ.ID,
		Bidder:     common.HexToAddress("0x2000000000000000000000000000000000000002").Hex(),
		Commitment: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Nonce:      "n-cooldown-old",
		Status:     "committed",
	})
	if err != nil {
		t.Fatalf("setup expired commit: %v", err)
	}

	// Trigger sealed-bid finalization and strike accounting.
	rr := env.request(t, "GET", fmt.Sprintf("/api/v1/rfqs/%d", expiredRFQ.ID), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected finalization GET 200, got %d: %s", rr.Code, rr.Body.String())
	}

	openRFQ := createSealedRFQFixture(t, env, now, now+3600, now+7200)
	body := `{
		"bidder": "0x2000000000000000000000000000000000000002",
		"commitment": "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"nonce": "n-cooldown-new"
	}`
	rr = env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/bids/commit", openRFQ.ID), body)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decodeJSON(t, rr)
	if resp["retry_after_seconds"] == nil {
		t.Fatalf("expected retry_after_seconds in response, got %v", resp)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header")
	}
}

func TestRevealBid_OutOfBudgetRange(t *testing.T) {
	env := setup(t)
	ctx := context.Background()
	now := time.Now().Unix()

	rfq := createSealedRFQFixture(t, env, now, now-100, now+1000)
	nonce := "n2"
	salt := "s2"
	commitment := sealedBidCommitment(
		rfq.ID,
		"0x2000000000000000000000000000000000000002",
		"999",
		0,
		"0",
		"[]",
		"",
		now+120,
		"",
		nonce,
		salt,
	)
	_, err := env.db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:      rfq.ID,
		Bidder:     "0x2000000000000000000000000000000000000002",
		Commitment: commitment,
		Nonce:      nonce,
		Status:     "committed",
	})
	if err != nil {
		t.Fatalf("setup bid commit: %v", err)
	}

	body := fmt.Sprintf(`{
		"bidder": "0x2000000000000000000000000000000000000002",
		"amount": "999",
		"nonce": "%s",
		"salt": "%s",
		"expires_at": "%d"
	}`, nonce, salt, now+120)

	rr := env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/bids/reveal", rfq.ID), body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRevealBid_CommitmentMismatch(t *testing.T) {
	env := setup(t)
	ctx := context.Background()
	now := time.Now().Unix()

	rfq := createSealedRFQFixture(t, env, now, now-100, now+1000)

	nonce := "n-mismatch"
	salt := "s-expected"
	commitment := sealedBidCommitment(
		rfq.ID,
		"0x2000000000000000000000000000000000000002",
		"250",
		0,
		"0",
		"[]",
		"",
		now+120,
		"",
		nonce,
		salt,
	)
	_, err := env.db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:      rfq.ID,
		Bidder:     "0x2000000000000000000000000000000000000002",
		Commitment: commitment,
		Nonce:      nonce,
		Status:     "committed",
	})
	if err != nil {
		t.Fatalf("setup bid commit: %v", err)
	}

	body := fmt.Sprintf(`{
		"bidder": "0x2000000000000000000000000000000000000002",
		"amount": "250",
		"nonce": "%s",
		"salt": "s-actual",
		"expires_at": "%d"
	}`, nonce, now+120)

	rr := env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/bids/reveal", rfq.ID), body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "commitment mismatch") {
		t.Fatalf("expected commitment mismatch error, got: %s", rr.Body.String())
	}
}

func TestRevealBid_DefaultsNormalizedBeforeCommitmentCheck(t *testing.T) {
	env := setup(t)
	ctx := context.Background()
	now := time.Now().Unix()
	bidder := "0x2000000000000000000000000000000000000002"

	rfq := createSealedRFQFixture(t, env, now, now-100, now+1000)

	nonce := "n-defaults"
	salt := "s-defaults"
	commitment := sealedBidCommitment(
		rfq.ID,
		bidder,
		"250",
		0,
		"0",
		"[]",
		"",
		rfq.Deadline,
		"",
		nonce,
		salt,
	)
	_, err := env.db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:      rfq.ID,
		Bidder:     bidder,
		Commitment: commitment,
		Nonce:      nonce,
		Status:     "committed",
	})
	if err != nil {
		t.Fatalf("setup bid commit: %v", err)
	}

	// Omit reputation_bond, milestones_json, and expires_at: server defaults must match canonical commitment.
	body := fmt.Sprintf(`{
		"bidder": "%s",
		"amount": "250",
		"nonce": "%s",
		"salt": "%s"
	}`, bidder, nonce, salt)
	rr := env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/bids/reveal", rfq.ID), body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRevealBid_NumericCanonicalizationAllowsLeadingZeros(t *testing.T) {
	env := setup(t)
	ctx := context.Background()
	now := time.Now().Unix()
	bidder := "0x2000000000000000000000000000000000000002"

	rfq := createSealedRFQFixture(t, env, now, now-100, now+1000)

	nonce := "n-canon"
	salt := "s-canon"
	commitment := sealedBidCommitment(
		rfq.ID,
		bidder,
		"250",
		12,
		"0",
		"[]",
		"",
		rfq.Deadline,
		"",
		nonce,
		salt,
	)
	_, err := env.db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:      rfq.ID,
		Bidder:     bidder,
		Commitment: commitment,
		Nonce:      nonce,
		Status:     "committed",
	})
	if err != nil {
		t.Fatalf("setup bid commit: %v", err)
	}

	body := fmt.Sprintf(`{
		"bidder": "%s",
		"amount": "000250",
		"estimated_duration": 12,
		"reputation_bond": "000",
		"nonce": "%s",
		"salt": "%s",
		"expires_at": "%d"
	}`, bidder, nonce, salt, rfq.Deadline)
	rr := env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/bids/reveal", rfq.ID), body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRevealBid_BidderAddressCaseInsensitiveForCommitLookup(t *testing.T) {
	env := setup(t)
	ctx := context.Background()
	now := time.Now().Unix()
	checksummedBidder := common.HexToAddress("0x2000000000000000000000000000000000000002").Hex()

	rfq := createSealedRFQFixture(t, env, now, now-100, now+1000)

	nonce := "n-case"
	salt := "s-case"
	expiresAt := now + 120
	commitment := sealedBidCommitment(
		rfq.ID,
		checksummedBidder,
		"300",
		0,
		"0",
		"[]",
		"",
		expiresAt,
		"",
		nonce,
		salt,
	)
	_, err := env.db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:      rfq.ID,
		Bidder:     checksummedBidder,
		Commitment: commitment,
		Nonce:      nonce,
		Status:     "committed",
	})
	if err != nil {
		t.Fatalf("setup bid commit: %v", err)
	}

	// Use lower-case address at reveal time; lookup should still find the commit.
	body := fmt.Sprintf(`{
		"bidder": "%s",
		"amount": "300",
		"nonce": "%s",
		"salt": "%s",
		"expires_at": "%d"
	}`, strings.ToLower(checksummedBidder), nonce, salt, expiresAt)
	rr := env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/bids/reveal", rfq.ID), body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRevealBid_ExpiredCommitMarkedWhenRevealWindowEnded(t *testing.T) {
	env := setup(t)
	ctx := context.Background()
	now := time.Now().Unix()
	bidder := "0x2000000000000000000000000000000000000002"

	rfq := createSealedRFQFixture(t, env, now, now-200, now-100)

	_, err := env.db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:      rfq.ID,
		Bidder:     bidder,
		Commitment: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Nonce:      "n-expire",
		Status:     "committed",
	})
	if err != nil {
		t.Fatalf("setup bid commit: %v", err)
	}

	body := fmt.Sprintf(`{
		"bidder": "%s",
		"amount": "200",
		"nonce": "n-expire",
		"salt": "s-expire",
		"expires_at": "%d"
	}`, bidder, now+120)
	rr := env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/bids/reveal", rfq.ID), body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 after reveal window ended, got %d: %s", rr.Code, rr.Body.String())
	}

	updated, err := env.db.GetBidCommitByRFQBidderNonce(ctx, rfq.ID, bidder, "n-expire")
	if err != nil {
		t.Fatalf("get updated commit: %v", err)
	}
	if updated.Status != "expired" {
		t.Fatalf("expected committed bid to be marked expired, got %q", updated.Status)
	}
}

func TestGetRFQ_FinalizesSealedBiddingAfterRevealDeadline(t *testing.T) {
	env := setup(t)
	ctx := context.Background()
	now := time.Now().Unix()

	rfq := createSealedRFQFixture(t, env, now, now-200, now-100)

	bid1, err := env.db.CreateBid(ctx, &storage.Bid{
		RFQID:              rfq.ID,
		Bidder:             "0x2000000000000000000000000000000000000002",
		Amount:             "200",
		EstimatedDuration:  7200,
		ReputationBond:     "0",
		MilestonesJSON:     "[]",
		Message:            "",
		Status:             "pending",
		ExpiresAt:          now + 600,
		CredentialsJSON:    "[]",
		CredentialVerified: true,
	})
	if err != nil {
		t.Fatalf("create bid1: %v", err)
	}
	bid2, err := env.db.CreateBid(ctx, &storage.Bid{
		RFQID:              rfq.ID,
		Bidder:             "0x3000000000000000000000000000000000000003",
		Amount:             "200",
		EstimatedDuration:  3600,
		ReputationBond:     "0",
		MilestonesJSON:     "[]",
		Message:            "",
		Status:             "pending",
		ExpiresAt:          now + 600,
		CredentialsJSON:    "[]",
		CredentialVerified: true,
	})
	if err != nil {
		t.Fatalf("create bid2: %v", err)
	}

	_, err = env.db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:         rfq.ID,
		Bidder:        bid1.Bidder,
		Commitment:    "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Nonce:         "n-best-1",
		Status:        "revealed",
		RevealedBidID: &bid1.ID,
	})
	if err != nil {
		t.Fatalf("create bid1 commit: %v", err)
	}
	_, err = env.db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:         rfq.ID,
		Bidder:        bid2.Bidder,
		Commitment:    "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Nonce:         "n-best-2",
		Status:        "revealed",
		RevealedBidID: &bid2.ID,
	})
	if err != nil {
		t.Fatalf("create bid2 commit: %v", err)
	}
	_, err = env.db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:      rfq.ID,
		Bidder:     "0x8000000000000000000000000000000000000008",
		Commitment: "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Nonce:      "n-hidden",
		Status:     "committed",
	})
	if err != nil {
		t.Fatalf("create hidden commit: %v", err)
	}

	rr := env.request(t, "GET", fmt.Sprintf("/api/v1/rfqs/%d", rfq.ID), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	rfqResp, ok := resp["rfq"].(map[string]any)
	if !ok {
		t.Fatalf("expected rfq object, got %T", resp["rfq"])
	}
	if got := rfqResp["sealed_bid_status"]; got != "finalized" {
		t.Fatalf("expected finalized sealed bid status, got %v", got)
	}
	if got := rfqResp["best_bid_id"]; got != float64(bid2.ID) {
		t.Fatalf("expected best_bid_id %d, got %v", bid2.ID, got)
	}
	if rfqResp["sealed_bid_selection_rule"] == "" {
		t.Fatal("expected sealed_bid_selection_rule in rfq response")
	}
}

func TestCommitBid_BidderIsBuyer(t *testing.T) {
	env := setup(t)
	now := time.Now().Unix()
	rfq := createSealedRFQFixture(t, env, now, now+3600, now+7200)

	body := `{
		"bidder": "0x1000000000000000000000000000000000000001",
		"commitment": "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"nonce": "n3"
	}`

	rr := env.request(t, "POST", fmt.Sprintf("/api/v1/rfqs/%d/bids/commit", rfq.ID), body)
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

func TestListBids_MissingRFQSkipsExpiryAndReturnsBids(t *testing.T) {
	env := setup(t)

	rr := env.request(t, "GET", "/api/v1/rfqs/999/bids", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var bids []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&bids); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(bids) != 0 {
		t.Fatalf("expected 0 bids, got %d", len(bids))
	}
}

func TestListBids_GetRFQDBErrorReturns500(t *testing.T) {
	env := setup(t)
	if err := env.db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	rr := env.request(t, "GET", "/api/v1/rfqs/1/bids", "")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestGetRFQ_RedactsCommitmentAndNonce(t *testing.T) {
	env := setup(t)
	ctx := context.Background()
	now := time.Now().Unix()

	rfq := createSealedRFQFixture(t, env, now, now+100, now+200)

	_, err := env.db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:      rfq.ID,
		Bidder:     "0x2000000000000000000000000000000000000002",
		Commitment: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Nonce:      "n-redact",
		Status:     "committed",
	})
	if err != nil {
		t.Fatalf("setup commit: %v", err)
	}

	rr := env.request(t, "GET", fmt.Sprintf("/api/v1/rfqs/%d", rfq.ID), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decodeJSON(t, rr)
	commits, ok := resp["commits"].([]any)
	if !ok || len(commits) != 1 {
		t.Fatalf("expected one commit, got %v", resp["commits"])
	}
	first, ok := commits[0].(map[string]any)
	if !ok {
		t.Fatalf("expected commit object, got %T", commits[0])
	}
	if _, exists := first["commitment"]; exists {
		t.Fatalf("commitment should be redacted from GetRFQ response")
	}
	if _, exists := first["nonce"]; exists {
		t.Fatalf("nonce should be redacted from GetRFQ response")
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
		BiddingMode: "sealed", CommitDeadline: time.Now().Unix() - 120, RevealDeadline: time.Now().Unix() - 60,
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
	_, err = env.db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:         rfq.ID,
		Bidder:        bid.Bidder,
		Commitment:    "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Nonce:         "n4",
		Status:        "revealed",
		RevealedBidID: &bid.ID,
	})
	if err != nil {
		t.Fatalf("setup bid commit: %v", err)
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
		BiddingMode: "sealed", CommitDeadline: time.Now().Unix() - 120, RevealDeadline: time.Now().Unix() - 60,
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
	_, err = env.db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:         rfq.ID,
		Bidder:        bid1.Bidder,
		Commitment:    "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		Nonce:         "n5",
		Status:        "revealed",
		RevealedBidID: &bid1.ID,
	})
	if err != nil {
		t.Fatalf("setup bid1 commit: %v", err)
	}
	_, err = env.db.CreateBidCommit(ctx, &storage.BidCommit{
		RFQID:         rfq.ID,
		Bidder:        bid2.Bidder,
		Commitment:    "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		Nonce:         "n6",
		Status:        "revealed",
		RevealedBidID: &bid2.ID,
	})
	if err != nil {
		t.Fatalf("setup bid2 commit: %v", err)
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

// Checkpoint tests (paper §6.1)

func createCheckpointTestEscrow(t *testing.T, env *testEnv) *storage.Escrow {
	t.Helper()
	ctx := context.Background()
	task, err := env.db.CreateTask(ctx, "CP Task", "desc", "0xspec")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	escrow, err := env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  84532,
		FactoryAddress:           "0xFactory",
		EscrowAddress:            "0xEscrowCP",
		Buyer:                    "0xBuyer",
		Worker:                   "0xWorker",
		Verifier:                 "0xVerifier",
		Arbitrator:               "0xArbitrator",
		Amount:                   "1000",
		Status:                   "funded",
		SubmissionDeadline:       1700000000,
		ReviewPeriodSeconds:      86400,
		DisputePeriodSeconds:     172800,
		ArbitratorTimeoutSeconds: 604800,
		MilestoneCount:           2,
	})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}
	return escrow
}

func TestCheckpointCommit(t *testing.T) {
	env := setup(t)
	escrow := createCheckpointTestEscrow(t, env)
	id := strconv.FormatInt(escrow.ID, 10)

	body := `{"state_snapshot_uri":"ipfs://snap1","snapshot_hash":"0xabc","committed_by":"0xWorker","milestone_index":0,"completion_pct":25}`
	rr := env.request(t, "POST", "/api/v1/escrows/"+id+"/checkpoints", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["state_snapshot_uri"] != "ipfs://snap1" {
		t.Fatalf("expected state_snapshot_uri 'ipfs://snap1', got %v", resp["state_snapshot_uri"])
	}
	if resp["schema_version"] != "checkpoint-v1" {
		t.Fatalf("expected schema_version 'checkpoint-v1', got %v", resp["schema_version"])
	}
}

func TestCheckpointCommit_NonWorkerForbidden(t *testing.T) {
	env := setup(t)
	escrow := createCheckpointTestEscrow(t, env)
	id := strconv.FormatInt(escrow.ID, 10)

	body := `{"state_snapshot_uri":"ipfs://snap","committed_by":"0xNotWorker"}`
	rr := env.request(t, "POST", "/api/v1/escrows/"+id+"/checkpoints", body)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCheckpointCommit_MissingURI(t *testing.T) {
	env := setup(t)
	escrow := createCheckpointTestEscrow(t, env)
	id := strconv.FormatInt(escrow.ID, 10)

	body := `{"committed_by":"0xWorker"}`
	rr := env.request(t, "POST", "/api/v1/escrows/"+id+"/checkpoints", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCheckpointCommit_InvalidMilestoneIndex(t *testing.T) {
	env := setup(t)
	escrow := createCheckpointTestEscrow(t, env)
	id := strconv.FormatInt(escrow.ID, 10)

	body := `{"state_snapshot_uri":"ipfs://snap","committed_by":"0xWorker","milestone_index":99}`
	rr := env.request(t, "POST", "/api/v1/escrows/"+id+"/checkpoints", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCheckpointCommit_InvalidCompletionPct(t *testing.T) {
	env := setup(t)
	escrow := createCheckpointTestEscrow(t, env)
	id := strconv.FormatInt(escrow.ID, 10)

	body := `{"state_snapshot_uri":"ipfs://snap","committed_by":"0xWorker","completion_pct":150}`
	rr := env.request(t, "POST", "/api/v1/escrows/"+id+"/checkpoints", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCheckpointCommit_InvalidMetadataJSON(t *testing.T) {
	env := setup(t)
	escrow := createCheckpointTestEscrow(t, env)
	id := strconv.FormatInt(escrow.ID, 10)

	body := `{"state_snapshot_uri":"ipfs://snap","committed_by":"0xWorker","metadata_json":"not-json"}`
	rr := env.request(t, "POST", "/api/v1/escrows/"+id+"/checkpoints", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCheckpointList(t *testing.T) {
	env := setup(t)
	escrow := createCheckpointTestEscrow(t, env)
	id := strconv.FormatInt(escrow.ID, 10)

	rrSetup1 := env.request(t, "POST", "/api/v1/escrows/"+id+"/checkpoints",
		`{"state_snapshot_uri":"ipfs://snap1","committed_by":"0xWorker","milestone_index":0}`)
	if rrSetup1.Code != http.StatusCreated {
		t.Fatalf("expected setup checkpoint 1 to return 201, got %d: %s", rrSetup1.Code, rrSetup1.Body.String())
	}
	rrSetup2 := env.request(t, "POST", "/api/v1/escrows/"+id+"/checkpoints",
		`{"state_snapshot_uri":"ipfs://snap2","committed_by":"0xWorker","milestone_index":1}`)
	if rrSetup2.Code != http.StatusCreated {
		t.Fatalf("expected setup checkpoint 2 to return 201, got %d: %s", rrSetup2.Code, rrSetup2.Body.String())
	}

	rr := env.request(t, "GET", "/api/v1/escrows/"+id+"/checkpoints", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var checkpoints []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&checkpoints); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(checkpoints) != 2 {
		t.Fatalf("expected 2 checkpoints, got %d", len(checkpoints))
	}

	rr2 := env.request(t, "GET", "/api/v1/escrows/"+id+"/checkpoints?milestone_index=0", "")
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr2.Code, rr2.Body.String())
	}
	var filtered []map[string]any
	if err := json.NewDecoder(rr2.Body).Decode(&filtered); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("expected 1 checkpoint for milestone 0, got %d", len(filtered))
	}
}

func TestCheckpointList_InvalidMilestoneFilter(t *testing.T) {
	env := setup(t)
	escrow := createCheckpointTestEscrow(t, env)
	id := strconv.FormatInt(escrow.ID, 10)

	rr := env.request(t, "GET", "/api/v1/escrows/"+id+"/checkpoints?milestone_index=-1", "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCheckpointList_EscrowNotFound(t *testing.T) {
	env := setup(t)
	rr := env.request(t, "GET", "/api/v1/escrows/99999/checkpoints", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCheckpointLatest(t *testing.T) {
	env := setup(t)
	escrow := createCheckpointTestEscrow(t, env)
	id := strconv.FormatInt(escrow.ID, 10)

	rrSetup1 := env.request(t, "POST", "/api/v1/escrows/"+id+"/checkpoints",
		`{"state_snapshot_uri":"ipfs://old","committed_by":"0xWorker"}`)
	if rrSetup1.Code != http.StatusCreated {
		t.Fatalf("expected setup checkpoint old to return 201, got %d: %s", rrSetup1.Code, rrSetup1.Body.String())
	}
	rrSetup2 := env.request(t, "POST", "/api/v1/escrows/"+id+"/checkpoints",
		`{"state_snapshot_uri":"ipfs://latest","committed_by":"0xWorker"}`)
	if rrSetup2.Code != http.StatusCreated {
		t.Fatalf("expected setup checkpoint latest to return 201, got %d: %s", rrSetup2.Code, rrSetup2.Body.String())
	}

	rr := env.request(t, "GET", "/api/v1/escrows/"+id+"/checkpoints/latest", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["state_snapshot_uri"] != "ipfs://latest" {
		t.Fatalf("expected latest URI, got %v", resp["state_snapshot_uri"])
	}
}

func TestCheckpointLatest_InvalidMilestoneFilter(t *testing.T) {
	env := setup(t)
	escrow := createCheckpointTestEscrow(t, env)
	id := strconv.FormatInt(escrow.ID, 10)

	rr := env.request(t, "GET", "/api/v1/escrows/"+id+"/checkpoints/latest?milestone_index=999", "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCheckpointLatest_NoCheckpoints(t *testing.T) {
	env := setup(t)
	escrow := createCheckpointTestEscrow(t, env)
	id := strconv.FormatInt(escrow.ID, 10)

	rr := env.request(t, "GET", "/api/v1/escrows/"+id+"/checkpoints/latest", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCheckpointList_EmptyReturnsArray(t *testing.T) {
	env := setup(t)
	escrow := createCheckpointTestEscrow(t, env)
	id := strconv.FormatInt(escrow.ID, 10)

	rr := env.request(t, "GET", "/api/v1/escrows/"+id+"/checkpoints", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if body := strings.TrimSpace(rr.Body.String()); body != "[]" {
		t.Fatalf("expected empty array '[]', got %q", body)
	}
}

func TestUCPWellKnown(t *testing.T) {
	env := setupWithUCP(t)
	rr := env.request(t, "GET", "/.well-known/ucp", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decodeJSON(t, rr)
	version, ok := resp["version"].(string)
	if !ok || version == "" {
		t.Fatalf("expected non-empty version string in response, got: %v", resp)
	}
}

func TestUCPCreateAndGetCheckout(t *testing.T) {
	env := setupWithUCP(t)
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "UCP Task", "desc", "0xabc")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	escrow, err := env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  84532,
		FactoryAddress:           env.cfg.FactoryAddress,
		EscrowAddress:            "0x00000000000000000000000000000000000000e5",
		EscrowID:                 task.ID,
		Buyer:                    "0x1000000000000000000000000000000000000001",
		Worker:                   "0x2000000000000000000000000000000000000002",
		Verifier:                 "0x3000000000000000000000000000000000000003",
		Arbitrator:               "0x4000000000000000000000000000000000000004",
		Amount:                   "100",
		WorkerStake:              "0",
		VerifierStakePerVerifier: "0",
		Token:                    "",
		Status:                   "created",
		SubmissionDeadline:       1700000000,
		ReviewPeriodSeconds:      60,
		DisputePeriodSeconds:     60,
		ArbitratorTimeoutSeconds: 60,
	})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}

	createBody := fmt.Sprintf(`{"escrow_id":%d}`, escrow.ID)
	createRR := env.request(t, "POST", "/api/v1/ucp/checkouts", createBody)
	if createRR.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", createRR.Code, createRR.Body.String())
	}
	createResp := decodeJSON(t, createRR)
	checkoutID, _ := createResp["checkout_id"].(string)
	if strings.TrimSpace(checkoutID) == "" {
		t.Fatalf("expected checkout_id in response: %v", createResp)
	}

	getRR := env.request(t, "GET", "/api/v1/ucp/checkouts/"+checkoutID, "")
	if getRR.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", getRR.Code, getRR.Body.String())
	}
	getResp := decodeJSON(t, getRR)
	if got, _ := getResp["checkout_id"].(string); got != checkoutID {
		t.Fatalf("expected checkout_id=%s got=%v", checkoutID, getResp["checkout_id"])
	}
}
