package x402

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerify_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/verify" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}

		var req VerifyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Payment.From != "0xBuyer" {
			t.Fatalf("unexpected from: %s", req.Payment.From)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(VerifyResponse{Valid: true})
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	resp, err := c.Verify(context.Background(), PaymentPayload{From: "0xBuyer"})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !resp.Valid {
		t.Fatal("expected valid")
	}
}

func TestVerify_Invalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(VerifyResponse{Valid: false, Reason: "expired"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	resp, err := c.Verify(context.Background(), PaymentPayload{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if resp.Valid {
		t.Fatal("expected invalid")
	}
	if resp.Reason != "expired" {
		t.Fatalf("unexpected reason: %s", resp.Reason)
	}
}

func TestVerify_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	_, err := c.Verify(context.Background(), PaymentPayload{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSettle_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/settle" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(SettleResponse{TxHash: "0xabc123", Success: true})
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	resp, err := c.Settle(context.Background(), PaymentPayload{From: "0xBuyer"})
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success")
	}
	if resp.TxHash != "0xabc123" {
		t.Fatalf("unexpected tx hash: %s", resp.TxHash)
	}
}

func TestSettle_Failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	_, err := c.Settle(context.Background(), PaymentPayload{})
	if err == nil {
		t.Fatal("expected error")
	}
}
