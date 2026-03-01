package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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

const testWebhookSecret = "test-webhook-secret-key"

type webhookTestEnv struct {
	db   *storage.DB
	mock *chain.MockClient
	idx  *indexer.Indexer
	cfg  *config.Config
	mux  http.Handler
}

func setupWebhookTest(t *testing.T) *webhookTestEnv {
	t.Helper()

	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mock := chain.NewMockClient()
	cfg := &config.Config{
		ChainID:                 84532,
		FactoryAddress:          "0x0000000000000000000000000000000000000F01",
		RequestTimeout:          10 * time.Second,
		TxTimeout:               90 * time.Second,
		CDPWebhookSecret:        testWebhookSecret,
		ReputationDampingFactor: 0.9,
	}

	idx := indexer.New(db, mock, cfg.FactoryAddress)
	mux := NewRouter(db, mock, idx, cfg, nil)

	return &webhookTestEnv{db: db, mock: mock, idx: idx, cfg: cfg, mux: mux}
}

// signPayload computes the CDP webhook HMAC-SHA256 signature.
func signPayload(t *testing.T, body string, secret string, headers http.Header) string {
	t.Helper()

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	headerNames := "content-type x-hook0-id"

	nameList := strings.Split(headerNames, " ")
	var headerValues []string
	for _, name := range nameList {
		headerValues = append(headerValues, headers.Get(name))
	}

	signedPayload := fmt.Sprintf("%s.%s.%s.%s",
		timestamp,
		headerNames,
		strings.Join(headerValues, "."),
		body,
	)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	sig := hex.EncodeToString(mac.Sum(nil))

	return fmt.Sprintf("t=%s,h=%s,v1=%s", timestamp, headerNames, sig)
}

// makeCDPPayload builds a realistic CDP webhook payload with decoded parameters.
func makeCDPPayload(t *testing.T, eventName string, contractAddr string, txHash string, params map[string]string) string {
	t.Helper()

	// Build the data object: standard fields + decoded event params
	data := map[string]any{
		"subscriptionId":  "sub_test",
		"networkId":       "base-sepolia",
		"blockNumber":     100,
		"blockHash":       "0xblockhash",
		"transactionHash": txHash,
		"logIndex":        0,
		"contractAddress": contractAddr,
		"eventName":       eventName,
	}
	for k, v := range params {
		data[k] = v
	}

	payload := map[string]any{
		"id":        "evt_test123",
		"type":      "onchain.activity.detected",
		"createdAt": time.Now().UTC().Format(time.RFC3339),
		"data":      data,
	}

	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal webhook payload: %v", err)
	}
	return string(b)
}

func sendWebhook(t *testing.T, env *webhookTestEnv, body string, secret string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/webhooks/cdp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hook0-Id", "evt_test123")

	sigHeader := signPayload(t, body, secret, req.Header)
	req.Header.Set("X-Hook0-Signature", sigHeader)

	rr := httptest.NewRecorder()
	env.mux.ServeHTTP(rr, req)
	return rr
}

func TestWebhook_ValidSignature_EscrowCreated(t *testing.T) {
	env := setupWebhookTest(t)

	body := makeCDPPayload(t, "EscrowCreated", env.cfg.FactoryAddress,
		"0xabc123def456",
		map[string]string{
			"escrowId":     "7",
			"escrow":       "0xABCDEF1234567890ABCDEF1234567890ABCDEF12",
			"buyer":        "0x1000000000000000000000000000000000000001",
			"worker":       "0x2000000000000000000000000000000000000002",
			"verifier":     "0x3000000000000000000000000000000000000003",
			"arbitrator":   "0x4000000000000000000000000000000000000004",
			"taskSpecHash": "0x0100000000000000000000000000000000000000000000000000000000000000",
			"token":        "0x0000000000000000000000000000000000000000",
		},
	)

	rr := sendWebhook(t, env, body, testWebhookSecret)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", resp["status"])
	}

	// Verify escrow was created in DB (use checksummed form from common.HexToAddress)
	checksummed := common.HexToAddress("0xABCDEF1234567890ABCDEF1234567890ABCDEF12").Hex()
	escrow, err := env.db.GetEscrowByAddress(context.Background(), checksummed)
	if err != nil {
		t.Fatalf("get escrow by address: %v", err)
	}
	if escrow.EscrowID != 7 {
		t.Fatalf("expected escrow ID 7, got %d", escrow.EscrowID)
	}
	if escrow.Status != "created" {
		t.Fatalf("expected status 'created', got %q", escrow.Status)
	}
}

func TestWebhook_InvalidSignature_Rejected(t *testing.T) {
	env := setupWebhookTest(t)

	body := makeCDPPayload(t, "EscrowCreated", env.cfg.FactoryAddress,
		"0xabc123",
		map[string]string{"escrowId": "1", "escrow": "0x1111111111111111111111111111111111111111"},
	)

	rr := sendWebhook(t, env, body, "wrong-secret")

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["error"] != "invalid signature" {
		t.Fatalf("expected 'invalid signature' error, got %v", resp["error"])
	}
}

func TestWebhook_MissingSignature_Rejected(t *testing.T) {
	env := setupWebhookTest(t)

	body := makeCDPPayload(t, "EscrowCreated", env.cfg.FactoryAddress,
		"0xabc123",
		map[string]string{},
	)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/cdp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	env.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestWebhook_UnknownEventType_Ignored(t *testing.T) {
	env := setupWebhookTest(t)

	payload := map[string]any{
		"id":        "evt_unknown",
		"type":      "some.unknown.type",
		"createdAt": time.Now().UTC().Format(time.RFC3339),
		"data":      map[string]any{"subscriptionId": "sub_test"},
	}
	bodyBytes, _ := json.Marshal(payload)
	body := string(bodyBytes)

	rr := sendWebhook(t, env, body, testWebhookSecret)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["status"] != "ignored" {
		t.Fatalf("expected status 'ignored', got %v", resp["status"])
	}
}

func TestWebhook_UnknownFactoryEvent_Ignored(t *testing.T) {
	env := setupWebhookTest(t)

	body := makeCDPPayload(t, "SomeUnknownEvent", env.cfg.FactoryAddress,
		"0xunknown123",
		map[string]string{"foo": "bar"},
	)

	rr := sendWebhook(t, env, body, testWebhookSecret)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeJSON(t, rr)
	if resp["status"] != "ok" {
		t.Fatalf("expected status 'ok', got %v", resp["status"])
	}
}

func TestWebhook_OutcomeRecorded_Processed(t *testing.T) {
	env := setupWebhookTest(t)

	body := makeCDPPayload(t, "OutcomeRecorded", env.cfg.FactoryAddress,
		"0xoutcome456",
		map[string]string{
			"escrowId":    "1",
			"participant": "0x1000000000000000000000000000000000000001",
			"role":        "worker",
			"outcome":     "completed",
		},
	)

	rr := sendWebhook(t, env, body, testWebhookSecret)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify reputation was upserted
	rep, err := env.db.GetReputation(
		context.Background(),
		strings.ToLower("0x1000000000000000000000000000000000000001"),
		"worker",
	)
	if err != nil {
		t.Fatalf("get reputation: %v", err)
	}
	if rep.Completed != 1 {
		t.Fatalf("expected 1 completed, got %d", rep.Completed)
	}

	events, err := env.db.ListReputationEvents(
		context.Background(),
		strings.ToLower("0x1000000000000000000000000000000000000001"),
		"worker",
		100,
	)
	if err != nil {
		t.Fatalf("list reputation events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 reputation event, got %d", len(events))
	}
	if events[0].Role != "worker" {
		t.Errorf("unexpected role: got %v want worker", events[0].Role)
	}
	if events[0].Outcome != "completed" {
		t.Errorf("unexpected outcome: got %v want completed", events[0].Outcome)
	}
	if events[0].TxHash != "0xoutcome456" {
		t.Errorf("unexpected tx_hash: got %v want 0xoutcome456", events[0].TxHash)
	}
	if events[0].LogIndex != 0 {
		t.Errorf("unexpected log_index: got %v want 0", events[0].LogIndex)
	}
	if events[0].Address != strings.ToLower("0x1000000000000000000000000000000000000001") {
		t.Errorf("unexpected address: got %v want %v", events[0].Address, strings.ToLower("0x1000000000000000000000000000000000000001"))
	}
	if events[0].BlockNumber != 100 {
		t.Errorf("unexpected block_number: got %v want 100", events[0].BlockNumber)
	}
}

func TestWebhook_DuplicateEvent_Idempotent(t *testing.T) {
	env := setupWebhookTest(t)

	body := makeCDPPayload(t, "EscrowCreated", env.cfg.FactoryAddress,
		"0xduplicate789",
		map[string]string{
			"escrowId":   "99",
			"escrow":     "0xDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD1",
			"buyer":      "0x1000000000000000000000000000000000000001",
			"worker":     "0x2000000000000000000000000000000000000002",
			"verifier":   "0x3000000000000000000000000000000000000003",
			"arbitrator": "0x4000000000000000000000000000000000000004",
		},
	)

	rr1 := sendWebhook(t, env, body, testWebhookSecret)
	if rr1.Code != http.StatusOK {
		t.Fatalf("first send: expected 200, got %d: %s", rr1.Code, rr1.Body.String())
	}

	rr2 := sendWebhook(t, env, body, testWebhookSecret)
	if rr2.Code != http.StatusOK {
		t.Fatalf("second send: expected 200, got %d: %s", rr2.Code, rr2.Body.String())
	}
}

func TestWebhook_ExistingEscrow_Skipped(t *testing.T) {
	env := setupWebhookTest(t)
	ctx := context.Background()

	// Pre-create the escrow in the DB (simulates API/MCP creation)
	task, err := env.db.CreateTask(ctx, "Existing", "desc", "0xabc")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	escrowAddr := "0xEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE1"
	_, err = env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: env.cfg.FactoryAddress,
		EscrowAddress: escrowAddr, EscrowID: 42,
		Buyer: "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA",
		Amount: "100", Status: "created",
	})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}

	body := makeCDPPayload(t, "EscrowCreated", env.cfg.FactoryAddress,
		"0xexisting123",
		map[string]string{
			"escrowId":   "42",
			"escrow":     escrowAddr,
			"buyer":      "0x1000000000000000000000000000000000000001",
			"worker":     "0x2000000000000000000000000000000000000002",
			"verifier":   "0x3000000000000000000000000000000000000003",
			"arbitrator": "0x4000000000000000000000000000000000000004",
		},
	)

	rr := sendWebhook(t, env, body, testWebhookSecret)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestWebhook_ReplayedTimestamp_Rejected(t *testing.T) {
	env := setupWebhookTest(t)

	body := makeCDPPayload(t, "EscrowCreated", env.cfg.FactoryAddress,
		"0xreplay123",
		map[string]string{},
	)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/cdp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hook0-Id", "evt_test123")

	// Sign with a timestamp from 10 minutes ago
	oldTimestamp := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	headerNames := "content-type x-hook0-id"

	nameList := strings.Split(headerNames, " ")
	var headerValues []string
	for _, name := range nameList {
		headerValues = append(headerValues, req.Header.Get(name))
	}

	signedPayload := fmt.Sprintf("%s.%s.%s.%s",
		oldTimestamp, headerNames, strings.Join(headerValues, "."), body,
	)

	mac := hmac.New(sha256.New, []byte(testWebhookSecret))
	mac.Write([]byte(signedPayload))
	sig := hex.EncodeToString(mac.Sum(nil))

	req.Header.Set("X-Hook0-Signature", fmt.Sprintf("t=%s,h=%s,v1=%s", oldTimestamp, headerNames, sig))

	rr := httptest.NewRecorder()
	env.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for replayed timestamp, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestWebhook_NotRegistered_InPollingMode(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mock := chain.NewMockClient()
	cfg := &config.Config{
		ChainID:        84532,
		FactoryAddress: "0x0000000000000000000000000000000000000F01",
		RequestTimeout: 10 * time.Second,
		TxTimeout:      90 * time.Second,
		// CDPWebhookSecret is empty — polling-only mode
	}

	idx := indexer.New(db, mock, cfg.FactoryAddress)
	mux := NewRouter(db, mock, idx, cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/cdp", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code == http.StatusOK {
		t.Fatalf("expected webhook endpoint to not be registered in polling mode, got 200")
	}
}

func TestVerifyCDPSignature_Valid(t *testing.T) {
	secret := "test-secret"
	body := `{"test": "data"}`

	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	headers.Set("X-Hook0-Id", "evt_123")

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	headerNames := "content-type x-hook0-id"

	signedPayload := fmt.Sprintf("%s.%s.%s.%s.%s",
		timestamp, headerNames, "application/json", "evt_123", body,
	)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	sig := hex.EncodeToString(mac.Sum(nil))

	sigHeader := fmt.Sprintf("t=%s,h=%s,v1=%s", timestamp, headerNames, sig)

	if !verifyCDPSignature([]byte(body), sigHeader, secret, headers) {
		t.Fatal("expected valid signature to pass verification")
	}
}

func TestVerifyCDPSignature_Invalid(t *testing.T) {
	secret := "test-secret"
	body := `{"test": "data"}`

	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	headers.Set("X-Hook0-Id", "evt_123")

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	headerNames := "content-type x-hook0-id"

	sigHeader := fmt.Sprintf("t=%s,h=%s,v1=%s", timestamp, headerNames,
		"deadbeef00112233445566778899aabbccddeeff00112233445566778899aabbcc")

	if verifyCDPSignature([]byte(body), sigHeader, secret, headers) {
		t.Fatal("expected invalid signature to fail verification")
	}
}

func TestVerifyCDPSignature_ExpiredTimestamp(t *testing.T) {
	secret := "test-secret"
	body := `{"test": "data"}`

	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	headers.Set("X-Hook0-Id", "evt_123")

	timestamp := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	headerNames := "content-type x-hook0-id"

	signedPayload := fmt.Sprintf("%s.%s.%s.%s.%s",
		timestamp, headerNames, "application/json", "evt_123", body,
	)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	sig := hex.EncodeToString(mac.Sum(nil))

	sigHeader := fmt.Sprintf("t=%s,h=%s,v1=%s", timestamp, headerNames, sig)

	if verifyCDPSignature([]byte(body), sigHeader, secret, headers) {
		t.Fatal("expected expired timestamp to fail verification")
	}
}

func TestVerifyCDPSignature_MalformedHeader(t *testing.T) {
	tests := []struct {
		name      string
		sigHeader string
	}{
		{"empty", ""},
		{"no parts", "garbage"},
		{"missing v1", "t=123,h=content-type"},
		{"missing timestamp", "h=content-type,v1=abc"},
		{"invalid timestamp", "t=notanumber,h=content-type,v1=abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if verifyCDPSignature([]byte("body"), tt.sigHeader, "secret", http.Header{}) {
				t.Fatalf("expected malformed header %q to fail", tt.sigHeader)
			}
		})
	}
}
