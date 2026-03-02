package ucp

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	escrowservice "github.com/eddiefleurent/agent-escrow/go-server/internal/escrow"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
)

func TestProjectStatus(t *testing.T) {
	cases := map[string]CheckoutStatus{
		"created":   CheckoutStatusIncomplete,
		"funded":    CheckoutStatusIncomplete,
		"submitted": CheckoutStatusReadyForComplete,
		"approved":  CheckoutStatusCompleteInProgress,
		"resolved":  CheckoutStatusCompleteInProgress,
		"disputed":  CheckoutStatusRequiresEscalation,
		"settled":   CheckoutStatusCompleted,
		"cancelled": CheckoutStatusCanceled,
		"refunded":  CheckoutStatusCanceled,
	}
	for escrowStatus, want := range cases {
		if got := ProjectStatus(escrowStatus); got != want {
			t.Fatalf("ProjectStatus(%q)=%q want=%q", escrowStatus, got, want)
		}
	}
}

func TestCreateCheckoutExistingEscrow(t *testing.T) {
	svc, db, _, cleanup := newUCPTestService(t)
	defer cleanup()
	ctx := context.Background()

	escrowID := createTestEscrow(ctx, t, db, "created")
	out, err := svc.CreateCheckout(ctx, CreateCheckoutRequest{EscrowID: &escrowID})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	if out.CheckoutID == "" {
		t.Fatalf("expected checkout_id")
	}
	if out.EscrowID != escrowID {
		t.Fatalf("expected escrow_id=%d got=%d", escrowID, out.EscrowID)
	}
	if out.UCPStatus != CheckoutStatusIncomplete {
		t.Fatalf("expected incomplete status got %s", out.UCPStatus)
	}
}

func TestCreateCheckoutIdempotency(t *testing.T) {
	svc, db, _, cleanup := newUCPTestService(t)
	defer cleanup()
	ctx := context.Background()

	firstEscrowID := createTestEscrow(ctx, t, db, "created")
	secondEscrowID := createTestEscrow(ctx, t, db, "created")

	req := CreateCheckoutRequest{
		EscrowID:       &firstEscrowID,
		IdempotencyKey: "idem-1",
	}
	first, err := svc.CreateCheckout(ctx, req)
	if err != nil {
		t.Fatalf("first CreateCheckout: %v", err)
	}
	second, err := svc.CreateCheckout(ctx, req)
	if err != nil {
		t.Fatalf("second CreateCheckout: %v", err)
	}
	if first.CheckoutID != second.CheckoutID {
		t.Fatalf("expected same checkout_id for idempotent replay")
	}

	_, err = svc.CreateCheckout(ctx, CreateCheckoutRequest{
		EscrowID:       &secondEscrowID,
		IdempotencyKey: "idem-1",
	})
	if err == nil {
		t.Fatalf("expected idempotency conflict")
	}
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected ErrIdempotencyConflict, got: %v", err)
	}
}

func TestUpdateCheckoutFund(t *testing.T) {
	svc, db, mock, cleanup := newUCPTestService(t)
	defer cleanup()
	ctx := context.Background()

	escrowID := createTestEscrow(ctx, t, db, "created")
	checkout, err := svc.CreateCheckout(ctx, CreateCheckoutRequest{EscrowID: &escrowID})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	updated, err := svc.UpdateCheckout(ctx, checkout.CheckoutID, UpdateCheckoutRequest{Operation: "fund"})
	if err != nil {
		t.Fatalf("UpdateCheckout: %v", err)
	}
	if updated.LastOperation != "fund" {
		t.Fatalf("expected last_operation=fund got=%q", updated.LastOperation)
	}
	if updated.LastTxHash == "" {
		t.Fatalf("expected last_tx_hash")
	}
	if len(mock.SentTxs) == 0 || mock.SentTxs[0].Method != "fund" {
		t.Fatalf("expected mock fund tx, got: %+v", mock.SentTxs)
	}
}

func newUCPTestService(t *testing.T) (*Service, *storage.DB, *chain.MockClient, func()) {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mock := chain.NewMockClient()
	cfg := &config.Config{
		ChainID:         84532,
		FactoryAddress:  "0x00000000000000000000000000000000000000f0",
		ComplexityFloor: "0",
	}
	idx := indexer.New(db, mock, cfg.FactoryAddress)
	escrowSvc := escrowservice.NewService(db, mock, idx, cfg)
	ucpSvc := NewService(db, escrowSvc, "Test UCP Provider", "http://localhost:8080")
	return ucpSvc, db, mock, func() { _ = db.Close() }
}

func createTestEscrow(ctx context.Context, t *testing.T, db *storage.DB, status string) int64 {
	t.Helper()
	task, err := db.CreateTask(ctx, "Task "+status, "desc", "0xabc")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	rec, err := db.CreateEscrow(ctx, &storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  84532,
		FactoryAddress:           "0x00000000000000000000000000000000000000f0",
		EscrowAddress:            "0x0000000000000000000000000000000000000" + strconv.FormatInt(task.ID, 16),
		EscrowID:                 task.ID,
		Buyer:                    "0x00000000000000000000000000000000000000b0",
		Worker:                   "0x00000000000000000000000000000000000000c0",
		Verifier:                 "0x00000000000000000000000000000000000000d0",
		Arbitrator:               "0x00000000000000000000000000000000000000e0",
		Amount:                   "100",
		WorkerStake:              "0",
		VerifierStakePerVerifier: "0",
		Token:                    "",
		Status:                   status,
		SubmissionDeadline:       1700000000,
		ReviewPeriodSeconds:      60,
		DisputePeriodSeconds:     60,
		ArbitratorTimeoutSeconds: 60,
	})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}
	return rec.ID
}
