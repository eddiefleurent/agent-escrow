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

func TestValidateMandateHTTP_RejectsDuplicateAuthorizationKey(t *testing.T) {
	t.Parallel()

	svc, _, _ := setupTestService(t)
	h := NewHandler(svc)
	body := []byte(`{"mandate_envelope":{"type":"payment","payload":{"signer":"0x1"},"signature":"0x11","signer_address":"0x1111111111111111111111111111111111111111","authorization":{"token":"0x036CbD53842c5426634e7929541eC2318f3dCF7e","from":"0x1111111111111111111111111111111111111111","From":"0x2222222222222222222222222222222222222222","to":"0x1","value":"1","valid_before":"2","nonce":"0x11","v":27,"r":"0x11","s":"0x11"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ap2/validate", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ValidateMandate(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp ValidateMandateResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	rejected := bytes.Contains(rr.Body.Bytes(), []byte("duplicate key")) || bytes.Contains(rr.Body.Bytes(), []byte("case-variant key"))
	if resp.Valid || !rejected {
		t.Fatalf("expected ambiguous authorization to be rejected, got %s", rr.Body.Bytes())
	}
}

func TestValidateMandateHTTP_SignatureEnforced(t *testing.T) {
	t.Parallel()

	svc, _, _ := setupTestService(t)
	h := NewHandler(svc)
	buyer := newTestSigner(t)
	attacker := newTestSigner(t)

	cases := map[string]struct {
		env   MandateEnvelope
		valid bool
	}{
		"signed by buyer":    {signedPayment(t, svc, buyer, testEscrowAddr, "1000000"), true},
		"signed by attacker": {sign(t, svc, attacker.key, paymentEnvelope(t, buyer.addr, testEscrowAddr, "1000000")), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			body, err := json.Marshal(ValidateMandateRequest{MandateEnvelope: tc.env})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/ap2/validate", bytes.NewReader(body))
			rr := httptest.NewRecorder()

			h.ValidateMandate(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rr.Code)
			}
			var resp ValidateMandateResponse
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Valid != tc.valid {
				t.Fatalf("expected valid=%v, got %+v", tc.valid, resp)
			}
		})
	}
}
