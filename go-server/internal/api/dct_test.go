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
	db, _ := storage.Open(":memory:")
	defer db.Close()
	ctx := context.Background()
	task, _ := db.CreateTask(ctx, "t", "d", "0x1")
	escrow, _ := db.CreateEscrow(ctx, &storage.Escrow{TaskID: task.ID, ChainID: 1, FactoryAddress: "0xf", EscrowAddress: "0xe", EscrowID: 1, Buyer: "0xb", Worker: "0xw", Verifier: "0xv", Arbitrator: "0xa", Amount: "1", Status: "funded", SubmissionDeadline: 1, ReviewPeriodSeconds: 1, DisputePeriodSeconds: 1, ArbitratorTimeoutSeconds: 1})

	mock := chain.NewMockClient()
	cfg := &config.Config{ChainID: 1, FactoryAddress: "0xf", RequestTimeout: 10 * time.Second, TxTimeout: 10 * time.Second}
	router := NewRouter(db, mock, indexer.New(db, mock, cfg.FactoryAddress), cfg, nil)
	ts := httptest.NewServer(router)
	defer ts.Close()

	mintReq := map[string]any{"escrow_id": escrow.ID, "subject": "agent-b", "operations": []string{"submit_work"}, "resources": []string{"escrow:1"}, "expires_at": time.Now().Add(time.Hour).Unix()}
	body, _ := json.Marshal(mintReq)
	resp, err := http.Post(ts.URL+"/api/v1/dcts/mint", "application/json", bytes.NewReader(body))
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
