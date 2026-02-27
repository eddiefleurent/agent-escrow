package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/authz"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
)

// testCallerMiddleware injects an authenticated principal based on the
// X-Test-Caller header. Used in tests to simulate auth middleware.
func testCallerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if addr := strings.TrimSpace(r.Header.Get("X-Test-Caller")); addr != "" {
			ctx := authz.WithCaller(r.Context(), authz.Principal{
				Address:       strings.ToLower(addr),
				Authenticated: true,
			})
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

func TestDCTHTTPFlow(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	task, err := db.CreateTask(ctx, "t", "d", "0x1")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	escrow, err := db.CreateEscrow(ctx, &storage.Escrow{TaskID: task.ID, ChainID: 1, FactoryAddress: "0xf", EscrowAddress: "0xe", EscrowID: 1, Buyer: "0xb", Worker: "0xw", Verifier: "0xv", Arbitrator: "0xa", Amount: "1", Status: "funded", SubmissionDeadline: 1, ReviewPeriodSeconds: 1, DisputePeriodSeconds: 1, ArbitratorTimeoutSeconds: 1})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}

	mock := chain.NewMockClient()
	cfg := &config.Config{ChainID: 1, FactoryAddress: "0xf", RequestTimeout: 10 * time.Second, TxTimeout: 10 * time.Second}
	router := testCallerMiddleware(NewRouter(db, mock, indexer.New(db, mock, cfg.FactoryAddress), cfg, nil))
	ts := httptest.NewServer(router)
	defer ts.Close()

	mintReq := map[string]any{"escrow_id": escrow.ID, "subject": "agent-b", "operations": []string{"submit_work"}, "resources": []string{"escrow:1"}, "expires_at": time.Now().Add(time.Hour).Unix(), "caller": "0xb"}
	body, _ := json.Marshal(mintReq)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/v1/dcts/mint", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Caller", "0xb") // auth middleware injects verified buyer
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("mint failed: err=%v status=%d", err, resp.StatusCode)
	}
	defer resp.Body.Close()
	var mint map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&mint)
	if mint["token"] == nil {
		t.Fatalf("expected token in mint response: %v", mint)
	}

	// Regression: handler must trust the middleware-injected principal, not the
	// JSON body's "caller" field. Send a mismatched caller in the body but a
	// valid buyer in the header — the request should still be authorized.
	mintReq["caller"] = "0xc" // mismatched — different from authenticated header
	body, _ = json.Marshal(mintReq)
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/v1/dcts/mint", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request (mismatch): %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Caller", "0xb") // middleware still injects authorized buyer
	resp2, err := http.DefaultClient.Do(req)
	if err != nil || resp2.StatusCode != http.StatusCreated {
		t.Fatalf("mint with mismatched JSON caller failed: err=%v status=%d", err, resp2.StatusCode)
	}
	resp2.Body.Close()
}

func TestListDCTAuditAuth(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	mock := chain.NewMockClient()
	cfg := &config.Config{
		ChainID:        1,
		FactoryAddress: "0xf",
		OwnerAddress:   "0xowner",
		RequestTimeout: 10 * time.Second,
		TxTimeout:      10 * time.Second,
	}
	router := testCallerMiddleware(NewRouter(db, mock, indexer.New(db, mock, cfg.FactoryAddress), cfg, nil))
	ts := httptest.NewServer(router)
	defer ts.Close()

	ctx := context.Background()

	t.Run("forbidden without auth", func(t *testing.T) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/dcts/audit", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request error: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("expected 403, got %d", resp.StatusCode)
		}
	})

	t.Run("forbidden for non-owner", func(t *testing.T) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/dcts/audit", nil)
		req.Header.Set("X-Test-Caller", "0xnotowner")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request error: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("expected 403, got %d", resp.StatusCode)
		}
	})

	t.Run("allowed for owner", func(t *testing.T) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/dcts/audit", nil)
		req.Header.Set("X-Test-Caller", "0xowner")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request error: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
	})

	t.Run("limit exceeds max returns 400", func(t *testing.T) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/dcts/audit?limit=9999", nil)
		req.Header.Set("X-Test-Caller", "0xowner")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request error: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("invalid limit returns 400", func(t *testing.T) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/dcts/audit?limit=0", nil)
		req.Header.Set("X-Test-Caller", "0xowner")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request error: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", resp.StatusCode)
		}
	})
}
