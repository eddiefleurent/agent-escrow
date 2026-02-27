package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
)

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
	router := NewRouter(db, mock, indexer.New(db, mock, cfg.FactoryAddress), cfg, nil)
	ts := httptest.NewServer(router)
	defer ts.Close()

	mintReq := map[string]any{"escrow_id": escrow.ID, "subject": "agent-b", "operations": []string{"submit_work"}, "resources": []string{"escrow:1"}, "expires_at": time.Now().Add(time.Hour).Unix(), "caller": "0xb"}
	body, _ := json.Marshal(mintReq)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/v1/dcts/mint", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
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
}
