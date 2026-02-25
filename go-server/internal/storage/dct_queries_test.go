package storage

import (
	"context"
	"testing"
)

func TestDCTQueriesLifecycle(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	task, err := db.CreateTask(ctx, "t", "d", "0x1")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	escrow, err := db.CreateEscrow(ctx, &Escrow{TaskID: task.ID, ChainID: 1, FactoryAddress: "0xf", EscrowAddress: "0xe", EscrowID: 1, Buyer: "0xb", Worker: "0xw", Verifier: "0xv", Arbitrator: "0xa", Amount: "1", Status: "funded", SubmissionDeadline: 1, ReviewPeriodSeconds: 1, DisputePeriodSeconds: 1, ArbitratorTimeoutSeconds: 1})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}

	rec, err := db.CreateDCTToken(ctx, &DCTToken{TokenID: "dct_a", TokenHash: "hash", EscrowID: escrow.ID, Subject: "alice", Issuer: "bob", OperationsJSON: "[\"submit_work\"]", ResourcesJSON: "[\"escrow:1\"]", Profile: "dct-profile-v1", CaveatsJSON: "[\"op=submit_work\",\"res=escrow:1\",\"exp<=9999999999\"]", Depth: 0, ExpiresAt: 9999999999})
	if err != nil {
		t.Fatal(err)
	}
	if rec.TokenID != "dct_a" {
		t.Fatalf("unexpected token id: %s", rec.TokenID)
	}
	if _, err := db.GetDCTTokenByTokenHash(ctx, "hash"); err != nil {
		t.Fatal(err)
	}
	if err := db.RevokeDCTToken(ctx, "dct_a", "manual", "test"); err != nil {
		t.Fatal(err)
	}
	if err := db.RevokeDCTToken(ctx, "dct_a", "manual-again", "test"); err != nil {
		t.Fatalf("expected idempotent revoke, got %v", err)
	}
	list, err := db.ListDCTTokensByEscrow(ctx, escrow.ID)
	if err != nil || len(list) != 1 || list[0].RevokedAt == nil {
		t.Fatalf("expected revoked token in list: err=%v len=%d", err, len(list))
	}
}
