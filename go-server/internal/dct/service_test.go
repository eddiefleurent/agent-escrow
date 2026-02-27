package dct

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/authz"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
)

// buyerCtx returns a context with the buyer principal set for authorization.
func buyerCtx() context.Context {
	return authz.WithCaller(context.Background(), authz.Principal{
		Address: "0xb", Authenticated: true,
	})
}

// callerCtx returns a context with the given address as an authenticated principal.
func callerCtx(addr string) context.Context {
	return authz.WithCaller(context.Background(), authz.Principal{
		Address: addr, Authenticated: true,
	})
}

func testService(t *testing.T) (*Service, int64) {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := buyerCtx()
	task, err := db.CreateTask(ctx, "t", "d", "0x1")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	escrow, err := db.CreateEscrow(ctx, &storage.Escrow{TaskID: task.ID, ChainID: 1, FactoryAddress: "0xf", EscrowAddress: "0xe", EscrowID: 1, Buyer: "0xb", Worker: "0xw", Verifier: "0xv", Arbitrator: "0xa", Amount: "1", Status: "funded", SubmissionDeadline: 1, ReviewPeriodSeconds: 1, DisputePeriodSeconds: 1, ArbitratorTimeoutSeconds: 1})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}
	audit := &authz.SQLiteAuditStore{DB: db.SQLDB()}
	return &Service{DB: db, Audit: audit, Now: func() time.Time { return time.Unix(1000, 0).UTC() }}, escrow.ID
}

func TestDelegateStrictAttenuation(t *testing.T) {
	svc, escrowID := testService(t)
	ctx := buyerCtx()
	_, parent, err := svc.Mint(ctx, MintParams{EscrowID: escrowID, Subject: "agent-a", Operations: []string{"submit_work", "approve_work"}, Resources: []string{"escrow:1", "artifact:a"}, ExpiresAt: 2000})
	if err != nil {
		t.Fatal(err)
	}

	// Delegate requires the caller to be the parent token's subject.
	delegateCtx := callerCtx("agent-a")
	_, _, err = svc.Delegate(delegateCtx, DelegateParams{ParentToken: parent, Subject: "agent-b", Operations: []string{"submit_work"}, Resources: []string{"escrow:1"}, ExpiresAt: 1500})
	if err != nil {
		t.Fatalf("expected valid attenuation, got %v", err)
	}

	_, _, err = svc.Delegate(delegateCtx, DelegateParams{ParentToken: parent, Subject: "agent-c", Operations: []string{"submit_work", "approve_work"}, Resources: []string{"escrow:1", "artifact:a"}, ExpiresAt: 2000})
	if err == nil || !strings.Contains(err.Error(), ErrInvalidAttenuation.Error()) {
		t.Fatalf("expected strict attenuation error, got %v", err)
	}
}

func TestDelegateRequiresSubject(t *testing.T) {
	svc, escrowID := testService(t)
	ctx := buyerCtx()
	_, parent, err := svc.Mint(ctx, MintParams{EscrowID: escrowID, Subject: "agent-a", Operations: []string{"submit_work"}, Resources: []string{"escrow:1"}, ExpiresAt: 2000})
	if err != nil {
		t.Fatal(err)
	}

	delegateCtx := callerCtx("agent-a")
	_, _, err = svc.Delegate(delegateCtx, DelegateParams{ParentToken: parent, Subject: " ", Operations: []string{"submit_work"}, Resources: []string{"escrow:1"}, ExpiresAt: 1500})
	if err == nil || !strings.Contains(err.Error(), "subject is required") {
		t.Fatalf("expected subject validation error, got %v", err)
	}
}

func TestIntrospectExpiry(t *testing.T) {
	svc, escrowID := testService(t)
	ctx := buyerCtx()
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

func TestIntrospectPublicUnauthenticated(t *testing.T) {
	svc, escrowID := testService(t)
	ctx := buyerCtx()
	_, token, err := svc.Mint(ctx, MintParams{EscrowID: escrowID, Subject: "agent-a", Operations: []string{"submit_work"}, Resources: []string{"escrow:1"}, ExpiresAt: 2000})
	if err != nil {
		t.Fatal(err)
	}

	_, active, reasons, err := svc.Introspect(context.Background(), token)
	if err != nil {
		t.Fatalf("introspect failed: %v", err)
	}
	if !active {
		t.Fatalf("expected active token for unauthenticated introspect, reasons=%v", reasons)
	}
	if strings.Contains(strings.Join(reasons, ","), string(authz.ReasonNotAuthenticated)) {
		t.Fatalf("unexpected auth-only reason in introspect result: %v", reasons)
	}
}

func TestIntrospectRevoke(t *testing.T) {
	svc, escrowID := testService(t)
	ctx := buyerCtx()
	rec, token, err := svc.Mint(ctx, MintParams{EscrowID: escrowID, Subject: "agent-a", Operations: []string{"submit_work"}, Resources: []string{"escrow:1"}, ExpiresAt: 1200})
	if err != nil {
		t.Fatal(err)
	}

	_, active, _, err := svc.Introspect(ctx, token)
	if err != nil || !active {
		t.Fatalf("expected active token: err=%v active=%v", err, active)
	}

	// Buyer can revoke any token in their escrow.
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
	ctx := buyerCtx()
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
	if !strings.Contains(strings.Join(reasons, ","), ReasonEscrowFrozen) {
		t.Fatalf("expected escrow_frozen reason, got %v", reasons)
	}
}

func TestIntrospectInvalidatesAncestorRevocation(t *testing.T) {
	svc, escrowID := testService(t)
	ctx := buyerCtx()
	parentRec, parent, err := svc.Mint(ctx, MintParams{EscrowID: escrowID, Subject: "agent-a", Operations: []string{"submit_work", "approve_work"}, Resources: []string{"escrow:1", "artifact:a"}, ExpiresAt: 2000})
	if err != nil {
		t.Fatal(err)
	}
	delegateCtx := callerCtx("agent-a")
	_, child, err := svc.Delegate(delegateCtx, DelegateParams{ParentToken: parent, Subject: "agent-b", Operations: []string{"submit_work"}, Resources: []string{"escrow:1"}, ExpiresAt: 1500})
	if err != nil {
		t.Fatal(err)
	}
	// Buyer revokes parent token (escrow-wide revocation authority).
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

// --- Authorization-specific tests ---

func TestMint_DeniedForWorker(t *testing.T) {
	svc, escrowID := testService(t)
	ctx := callerCtx("0xw")
	_, _, err := svc.Mint(ctx, MintParams{EscrowID: escrowID, Subject: "agent-a", Operations: []string{"submit_work"}, Resources: []string{"escrow:1"}, ExpiresAt: 2000})
	if err == nil {
		t.Fatal("expected authorization denied for worker minting")
	}
	if !IsUnauthorized(err) {
		t.Fatalf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestMint_DeniedForUnauthenticated(t *testing.T) {
	svc, escrowID := testService(t)
	ctx := context.Background()
	_, _, err := svc.Mint(ctx, MintParams{EscrowID: escrowID, Subject: "agent-a", Operations: []string{"submit_work"}, Resources: []string{"escrow:1"}, ExpiresAt: 2000})
	if err == nil {
		t.Fatal("expected authorization denied for unauthenticated caller")
	}
	if !IsUnauthorized(err) {
		t.Fatalf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestDelegate_DeniedForNonHolder(t *testing.T) {
	svc, escrowID := testService(t)
	ctx := buyerCtx()
	_, parent, err := svc.Mint(ctx, MintParams{EscrowID: escrowID, Subject: "agent-a", Operations: []string{"submit_work", "approve_work"}, Resources: []string{"escrow:1", "artifact:a"}, ExpiresAt: 2000})
	if err != nil {
		t.Fatal(err)
	}
	// Try to delegate as someone who is not the token subject.
	wrongCtx := callerCtx("0xrandom")
	_, _, err = svc.Delegate(wrongCtx, DelegateParams{ParentToken: parent, Subject: "agent-b", Operations: []string{"submit_work"}, Resources: []string{"escrow:1"}, ExpiresAt: 1500})
	if err == nil {
		t.Fatal("expected authorization denied for non-holder delegation")
	}
	if !IsUnauthorized(err) {
		t.Fatalf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestRevoke_DeniedForUnauthorized(t *testing.T) {
	svc, escrowID := testService(t)
	ctx := buyerCtx()
	rec, _, err := svc.Mint(ctx, MintParams{EscrowID: escrowID, Subject: "agent-a", Operations: []string{"submit_work"}, Resources: []string{"escrow:1"}, ExpiresAt: 2000})
	if err != nil {
		t.Fatal(err)
	}
	// Try to revoke as a random address that is neither issuer nor buyer.
	randomCtx := callerCtx("0xrandom")
	err = svc.Revoke(randomCtx, RevokeParams{TokenID: rec.TokenID, Reason: "test"})
	if err == nil {
		t.Fatal("expected authorization denied for unauthorized revoker")
	}
	if !IsUnauthorized(err) {
		t.Fatalf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestRevoke_AllowedForIssuer(t *testing.T) {
	svc, escrowID := testService(t)
	ctx := buyerCtx()
	rec, _, err := svc.Mint(ctx, MintParams{EscrowID: escrowID, Subject: "agent-a", Issuer: "0xissuer", Operations: []string{"submit_work"}, Resources: []string{"escrow:1"}, ExpiresAt: 2000})
	if err != nil {
		t.Fatal(err)
	}
	// Issuer can revoke their own token.
	issuerCtx := callerCtx("0xissuer")
	err = svc.Revoke(issuerCtx, RevokeParams{TokenID: rec.TokenID, Reason: "test"})
	if err != nil {
		t.Fatalf("expected issuer revoke to succeed, got: %v", err)
	}
}

func TestEmergencyOverride(t *testing.T) {
	svc, escrowID := testService(t)
	ctx := buyerCtx()
	_, _, err := svc.Mint(ctx, MintParams{EscrowID: escrowID, Subject: "agent-a", Operations: []string{"submit_work"}, Resources: []string{"escrow:1"}, ExpiresAt: 2000})
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := callerCtx("0xowner")
	err = svc.EmergencyOverride(ownerCtx, EmergencyOverrideParams{
		EscrowID:      escrowID,
		Operation:     "revoke_all",
		CallerAddress: "0xcompromised",
		Reason:        "key compromise",
		OwnerAddress:  "0xowner",
	})
	if err != nil {
		t.Fatalf("emergency override failed: %v", err)
	}
	// Verify all tokens for this escrow are revoked.
	tokens, err := svc.DB.ListDCTTokensByEscrow(ctx, escrowID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range tokens {
		if tok.RevokedAt == nil {
			t.Fatalf("expected token %s to be revoked after emergency override", tok.TokenID)
		}
	}

	err = svc.EmergencyOverride(callerCtx("0xnotowner"), EmergencyOverrideParams{
		EscrowID:      escrowID,
		Operation:     "revoke_all",
		CallerAddress: "0xcompromised",
		Reason:        "key compromise",
		OwnerAddress:  "0xowner",
	})
	if err == nil {
		t.Fatal("expected non-owner emergency override to fail")
	}
	if !IsUnauthorized(err) {
		t.Fatalf("expected ErrUnauthorized for non-owner override, got: %v", err)
	}
}
