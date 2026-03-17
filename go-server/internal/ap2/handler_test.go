package ap2

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFundViaMandateInvalidJSON(t *testing.T) {
	t.Parallel()

	h := NewHandler(&Service{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ap2/fund", bytes.NewBufferString("{"))
	rr := httptest.NewRecorder()

	h.FundViaMandate(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["error"] != "invalid request body" {
		t.Fatalf("expected invalid request body error, got %q", body["error"])
	}
}

func TestValidateMandateInvalidBody(t *testing.T) {
	t.Parallel()

	h := NewHandler(&Service{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ap2/validate", bytes.NewBufferString("{"))
	rr := httptest.NewRecorder()

	h.ValidateMandate(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["error"] != "invalid request body" {
		t.Fatalf("expected invalid request body error, got %q", body["error"])
	}
}

func TestValidateMandateInvalidEnvelope(t *testing.T) {
	t.Parallel()

	h := NewHandler(&Service{})
	reqBody := `{"mandate_envelope":{"type":"intent"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ap2/validate", bytes.NewBufferString(reqBody))
	rr := httptest.NewRecorder()

	h.ValidateMandate(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp ValidateMandateResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Valid {
		t.Fatal("expected invalid mandate result")
	}
	if resp.Reason == "" {
		t.Fatal("expected non-empty reason for invalid mandate")
	}
}

func TestGetMandateMissingID(t *testing.T) {
	t.Parallel()

	h := NewHandler(&Service{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ap2/mandates/", nil)
	rr := httptest.NewRecorder()

	h.GetMandate(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["error"] != "mandate id is required" {
		t.Fatalf("expected missing id error, got %q", body["error"])
	}
}

func TestGetMandateNotFound(t *testing.T) {
	t.Parallel()

	svc, _, _ := setupTestService(t)
	h := NewHandler(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ap2/mandates/missing", nil)
	req.SetPathValue("id", "missing")
	rr := httptest.NewRecorder()

	h.GetMandate(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["error"] != "mandate not found" {
		t.Fatalf("expected mandate not found error, got %q", body["error"])
	}
}
