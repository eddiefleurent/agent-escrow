package a2a

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
)

func setupHandler(t *testing.T) (*Handler, *Service) {
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
		Port:           8080,
		A2AEnabled:     true,
		A2AAgentName:   "Test Agent",
		A2AAgentURL:    "http://localhost:8080",
		RequestTimeout: 10 * time.Second,
		TxTimeout:      90 * time.Second,
	}
	idx := indexer.New(db, mock, cfg.FactoryAddress)

	svc := &Service{DB: db, Chain: mock, Idx: idx, Cfg: cfg}
	return NewHandler(svc), svc
}

func TestServeAgentCard(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("GET", "/.well-known/agent.json", nil)
	rr := httptest.NewRecorder()
	h.ServeAgentCard(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}

	var card AgentCard
	if err := json.NewDecoder(rr.Body).Decode(&card); err != nil {
		t.Fatalf("decode agent card: %v", err)
	}

	if card.Name != "Test Agent" {
		t.Fatalf("expected name 'Test Agent', got %q", card.Name)
	}
	if len(card.Skills) == 0 {
		t.Fatal("expected non-empty skills")
	}
}

func TestHandleJSONRPC_TasksSend(t *testing.T) {
	h, _ := setupHandler(t)

	body := `{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "tasks/send",
		"params": {
			"message": {
				"role": "user",
				"parts": [{"type": "text", "text": "Create a task"}]
			},
			"metadata": {
				"delegator_agent": "agent-a"
			}
		}
	}`

	req := httptest.NewRequest("POST", "/a2a", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.HandleJSONRPC(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp JSONRPCResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
	if resp.ID != float64(1) {
		t.Fatalf("expected id 1, got %v", resp.ID)
	}
}

func TestHandleJSONRPC_TasksGet(t *testing.T) {
	h, svc := setupHandler(t)

	_, err := svc.HandleTaskSend(TaskSendParams{
		ID: "get-test-task",
		Message: Message{
			Role:  "user",
			Parts: []Part{{Type: "text", Text: "test"}},
		},
	})
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}

	body := `{
		"jsonrpc": "2.0",
		"id": 2,
		"method": "tasks/get",
		"params": {"id": "get-test-task"}
	}`

	req := httptest.NewRequest("POST", "/a2a", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.HandleJSONRPC(rr, req)

	var resp JSONRPCResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
}

func TestHandleJSONRPC_TasksCancel(t *testing.T) {
	h, svc := setupHandler(t)

	_, err := svc.HandleTaskSend(TaskSendParams{
		ID: "cancel-test-task",
		Message: Message{
			Role:  "user",
			Parts: []Part{{Type: "text", Text: "test"}},
		},
	})
	if err != nil {
		t.Fatalf("setup task: %v", err)
	}

	body := `{
		"jsonrpc": "2.0",
		"id": 3,
		"method": "tasks/cancel",
		"params": {"id": "cancel-test-task"}
	}`

	req := httptest.NewRequest("POST", "/a2a", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.HandleJSONRPC(rr, req)

	var resp JSONRPCResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
}

func TestHandleJSONRPC_MethodNotFound(t *testing.T) {
	h, _ := setupHandler(t)

	body := `{
		"jsonrpc": "2.0",
		"id": 4,
		"method": "tasks/unknown",
		"params": {}
	}`

	req := httptest.NewRequest("POST", "/a2a", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.HandleJSONRPC(rr, req)

	var resp JSONRPCResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != ErrCodeMethodNotFound {
		t.Fatalf("expected error code %d, got %d", ErrCodeMethodNotFound, resp.Error.Code)
	}
}

func TestHandleJSONRPC_InvalidContentType(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("POST", "/a2a", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()
	h.HandleJSONRPC(rr, req)

	var resp JSONRPCResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error for invalid content type")
	}
	if resp.Error.Code != ErrCodeInvalidRequest {
		t.Fatalf("expected error code %d, got %d", ErrCodeInvalidRequest, resp.Error.Code)
	}
}

func TestHandleJSONRPC_InvalidJSON(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("POST", "/a2a", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.HandleJSONRPC(rr, req)

	var resp JSONRPCResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if resp.Error.Code != ErrCodeParse {
		t.Fatalf("expected error code %d, got %d", ErrCodeParse, resp.Error.Code)
	}
}

func TestHandleJSONRPC_InvalidVersion(t *testing.T) {
	h, _ := setupHandler(t)

	body := `{
		"jsonrpc": "1.0",
		"id": 5,
		"method": "tasks/send",
		"params": {}
	}`

	req := httptest.NewRequest("POST", "/a2a", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.HandleJSONRPC(rr, req)

	var resp JSONRPCResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error for invalid jsonrpc version")
	}
	if resp.Error.Code != ErrCodeInvalidRequest {
		t.Fatalf("expected error code %d, got %d", ErrCodeInvalidRequest, resp.Error.Code)
	}
}

func TestHandleJSONRPC_TasksSendEmptyMessage(t *testing.T) {
	h, _ := setupHandler(t)

	body := `{
		"jsonrpc": "2.0",
		"id": 6,
		"method": "tasks/send",
		"params": {
			"message": {
				"role": "user",
				"parts": []
			}
		}
	}`

	req := httptest.NewRequest("POST", "/a2a", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.HandleJSONRPC(rr, req)

	var resp JSONRPCResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error for empty message parts")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected error code %d, got %d", ErrCodeInvalidParams, resp.Error.Code)
	}
}

func TestHandleJSONRPC_TasksGetNotFound(t *testing.T) {
	h, _ := setupHandler(t)

	body := `{
		"jsonrpc": "2.0",
		"id": 7,
		"method": "tasks/get",
		"params": {"id": "nonexistent"}
	}`

	req := httptest.NewRequest("POST", "/a2a", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.HandleJSONRPC(rr, req)

	var resp JSONRPCResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error for nonexistent task")
	}
	if resp.Error.Code != ErrCodeTaskNotFound {
		t.Fatalf("expected error code %d, got %d", ErrCodeTaskNotFound, resp.Error.Code)
	}
}

func TestHandleJSONRPC_TasksGetMissingID(t *testing.T) {
	h, _ := setupHandler(t)

	body := `{
		"jsonrpc": "2.0",
		"id": 8,
		"method": "tasks/get",
		"params": {}
	}`

	req := httptest.NewRequest("POST", "/a2a", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.HandleJSONRPC(rr, req)

	var resp JSONRPCResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error for missing id")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected error code %d, got %d", ErrCodeInvalidParams, resp.Error.Code)
	}
}

func TestHandleJSONRPC_WithVerificationPolicy(t *testing.T) {
	h, _ := setupHandler(t)

	body := `{
		"jsonrpc": "2.0",
		"id": 9,
		"method": "tasks/send",
		"params": {
			"message": {
				"role": "user",
				"parts": [{"type": "text", "text": "Escrowed task"}]
			},
			"metadata": {
				"delegator_agent": "agent-a",
				"verification_policy": "{\"mode\": \"strict\", \"artifacts\": [{\"type\": \"unit_test_log\"}], \"escrow_trigger\": true}"
			}
		}
	}`

	req := httptest.NewRequest("POST", "/a2a", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.HandleJSONRPC(rr, req)

	var resp JSONRPCResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
}
