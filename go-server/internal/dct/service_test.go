package dct

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
)

func testService(t *testing.T) (*Service, int64) {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	task, _ := db.CreateTask(ctx, "t", "d", "0x1")
	escrow, _ := db.CreateEscrow(ctx, &storage.Escrow{TaskID: task.ID, ChainID: 1, FactoryAddress: "0xf", EscrowAddress: "0xe", EscrowID: 1, Buyer: "0xb", Worker: "0xw", Verifier: "0xv", Arbitrator: "0xa", Amount: "1", Status: "funded", SubmissionDeadline: 1, ReviewPeriodSeconds: 1, DisputePeriodSeconds: 1, ArbitratorTimeoutSeconds: 1})
	return &Service{DB: db, Now: func() time.Time { return time.Unix(1000, 0).UTC() }}, escrow.ID
}

func TestDelegateStrictAttenuation(t *testing.T) {
	svc, escrowID := testService(t)
	ctx := context.Background()
	_, parent, err := svc.Mint(ctx, MintParams{EscrowID: escrowID, Subject: "agent-a", Operations: []string{"submit_work", "approve_work"}, Resources: []string{"escrow:1", "artifact:a"}, ExpiresAt: 2000})
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = svc.Delegate(ctx, DelegateParams{ParentToken: parent, Subject: "agent-b", Operations: []string{"submit_work"}, Resources: []string{"escrow:1"}, ExpiresAt: 1500})
	if err != nil {
		t.Fatalf("expected valid attenuation, got %v", err)
	}

	_, _, err = svc.Delegate(ctx, DelegateParams{ParentToken: parent, Subject: "agent-c", Operations: []string{"resolve_dispute"}, Resources: []string{"escrow:1"}, ExpiresAt: 1500})
	if err == nil || !strings.Contains(err.Error(), ErrInvalidAttenuation.Error()) {
		t.Fatalf("expected attenuation error, got %v", err)
	}
}

func TestIntrospectExpiryAndRevoke(t *testing.T) {
	svc, escrowID := testService(t)
	ctx := context.Background()
	rec, token, err := svc.Mint(ctx, MintParams{EscrowID: escrowID, Subject: "agent-a", Operations: []string{"submit_work"}, Resources: []string{"escrow:1"}, ExpiresAt: 1200})
	if err != nil {
		t.Fatal(err)
	}

	_, active, _, err := svc.Introspect(ctx, token)
	if err != nil || !active {
		t.Fatalf("expected active token: err=%v active=%v", err, active)
	}

	if err := svc.Revoke(ctx, RevokeParams{TokenID: rec.TokenID, Reason: "manual"}); err != nil {
		t.Fatal(err)
	}
	_, active, reasons, err := svc.Introspect(ctx, token)
	if err != nil || active || len(reasons) == 0 {
		t.Fatalf("expected revoked token: err=%v active=%v reasons=%v", err, active, reasons)
	}
}
