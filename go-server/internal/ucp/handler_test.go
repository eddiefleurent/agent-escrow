package ucp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestHandlerWellKnown(t *testing.T) {
	svc, _, _, cleanup := newUCPTestService(t)
	defer cleanup()
	h := NewHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/ucp", nil)
	rec := httptest.NewRecorder()
	h.WellKnown(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if _, ok := payload["operations"]; !ok {
		t.Fatalf("expected operations in profile: %v", payload)
	}
}

func TestHandlerCreateAndGetCheckout(t *testing.T) {
	svc, db, _, cleanup := newUCPTestService(t)
	defer cleanup()
	h := NewHandler(svc)
	ctx := context.Background()

	escrowID := createTestEscrow(ctx, t, db, "created")
	body := []byte(`{"escrow_id":` + strconv.FormatInt(escrowID, 10) + `}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/ucp/checkouts", bytes.NewReader(body))
	createRec := httptest.NewRecorder()
	h.CreateCheckout(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var createResp Checkout
	if err := json.Unmarshal(createRec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	if createResp.CheckoutID == "" {
		t.Fatalf("expected checkout_id")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/ucp/checkouts/"+createResp.CheckoutID, nil)
	getReq.SetPathValue("checkout_id", createResp.CheckoutID)
	getRec := httptest.NewRecorder()
	h.GetCheckout(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	var getResp Checkout
	if err := json.Unmarshal(getRec.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("unmarshal get response: %v", err)
	}
	if getResp.CheckoutID != createResp.CheckoutID {
		t.Fatalf("expected checkout_id=%s got=%s", createResp.CheckoutID, getResp.CheckoutID)
	}
}

func TestHandlerGetCheckoutNotFound(t *testing.T) {
	svc, _, _, cleanup := newUCPTestService(t)
	defer cleanup()
	h := NewHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ucp/checkouts/missing", nil)
	req.SetPathValue("checkout_id", "missing")
	rec := httptest.NewRecorder()
	h.GetCheckout(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
