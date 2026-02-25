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
	task, err := db.CreateTask(ctx, "t", "d", "0x1")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	escrow, err := db.CreateEscrow(ctx, &storage.Escrow{TaskID: task.ID, ChainID: 1, FactoryAddress: "0xf", EscrowAddress: "0xe", EscrowID: 1, Buyer: "0xb", Worker: "0xw", Verifier: "0xv", Arbitrator: "0xa", Amount: "1", Status: "funded", SubmissionDeadline: 1, ReviewPeriodSeconds: 1, DisputePeriodSeconds: 1, ArbitratorTimeoutSeconds: 1})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}
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

	_, _, err = svc.Delegate(ctx, DelegateParams{ParentToken: parent, Subject: "agent-c", Operations: []string{"submit_work", "approve_work"}, Resources: []string{"escrow:1", "artifact:a"}, ExpiresAt: 2000})
	if err == nil || !strings.Contains(err.Error(), ErrInvalidAttenuation.Error()) {
		t.Fatalf("expected strict attenuation error, got %v", err)
	}
}

func TestDelegateRequiresSubject(t *testing.T) {
	svc, escrowID := testService(t)
	ctx := context.Background()
	_, parent, err := svc.Mint(ctx, MintParams{EscrowID: escrowID, Subject: "agent-a", Operations: []string{"submit_work"}, Resources: []string{"escrow:1"}, ExpiresAt: 2000})
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = svc.Delegate(ctx, DelegateParams{ParentToken: parent, Subject: " ", Operations: []string{"submit_work"}, Resources: []string{"escrow:1"}, ExpiresAt: 1500})
	if err == nil || !strings.Contains(err.Error(), "subject is required") {
		t.Fatalf("expected subject validation error, got %v", err)
	}
}

func TestIntrospectExpiry(t *testing.T) {
	svc, escrowID := testService(t)
	ctx := context.Background()
	_, token, err := svc.Mint(ctx, MintParams{EscrowID: escrowID, Subject: "agent-a", Operations: []string{"submit_work"}, Resources: []string{"escrow:1"}, ExpiresAt: 1200})
	if err != nil {
		t.Fatal(err)
	}

	svc.Now = func() time.Time { return time.Unix(1300, 0).UTC() }
	_, active, reasons, err := svc.Introspect(ctx, token)
	if err != nil || active || len(reasons) == 0 {
		t.Fatalf("expected expired token: err=%v active=%v reasons=%v", err, active, reasons)
	}
}

func TestIntrospectRevoke(t *testing.T) {
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

func TestIntrospectInvalidatesWhenEscrowFrozen(t *testing.T) {
	svc, escrowID := testService(t)
	ctx := context.Background()
	_, token, err := svc.Mint(ctx, MintParams{EscrowID: escrowID, Subject: "agent-a", Operations: []string{"submit_work"}, Resources: []string{"escrow:1"}, ExpiresAt: 2000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DB.SQLDB().ExecContext(ctx, `UPDATE escrows SET frozen = 1 WHERE id = ?`, escrowID); err != nil {
		t.Fatal(err)
	}
	_, active, reasons, err := svc.Introspect(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatalf("expected inactive token when escrow frozen")
	}
	if !strings.Contains(strings.Join(reasons, ","), "escrow_frozen") {
		t.Fatalf("expected escrow_frozen reason, got %v", reasons)
	}
}

func TestIntrospectInvalidatesAncestorRevocation(t *testing.T) {
	svc, escrowID := testService(t)
	ctx := context.Background()
	parentRec, parent, err := svc.Mint(ctx, MintParams{EscrowID: escrowID, Subject: "agent-a", Operations: []string{"submit_work", "approve_work"}, Resources: []string{"escrow:1", "artifact:a"}, ExpiresAt: 2000})
	if err != nil {
		t.Fatal(err)
	}
	_, child, err := svc.Delegate(ctx, DelegateParams{ParentToken: parent, Subject: "agent-b", Operations: []string{"submit_work"}, Resources: []string{"escrow:1"}, ExpiresAt: 1500})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Revoke(ctx, RevokeParams{TokenID: parentRec.TokenID, Reason: "manual"}); err != nil {
		t.Fatal(err)
	}
	_, active, reasons, err := svc.Introspect(ctx, child)
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatalf("expected child inactive when parent revoked")
	}
	if !strings.Contains(strings.Join(reasons, ","), "ancestor_revoked") {
		t.Fatalf("expected ancestor_revoked reason, got %v", reasons)
	}
}
