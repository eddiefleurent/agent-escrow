package storage

import (
	"context"
	"strings"
	"testing"
)

func createEmergencyTestEscrowAndToken(t *testing.T, db *DB, tokenID string) *Escrow {
	t.Helper()

	ctx := context.Background()
	task, err := db.CreateTask(ctx, "Emergency Task", "desc", "0xspec")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	escrow, err := db.CreateEscrow(ctx, &Escrow{
		TaskID:                   task.ID,
		ChainID:                  84532,
		FactoryAddress:           "0x1234567890123456789012345678901234567890",
		EscrowAddress:            "0x1111111111111111111111111111111111111111",
		EscrowID:                 1,
		Buyer:                    "0x2222222222222222222222222222222222222222",
		Worker:                   "0x3333333333333333333333333333333333333333",
		Verifier:                 "0x4444444444444444444444444444444444444444",
		Arbitrator:               "0x5555555555555555555555555555555555555555",
		Amount:                   "1000",
		Status:                   "funded",
		SubmissionDeadline:       1700000000,
		ReviewPeriodSeconds:      60,
		DisputePeriodSeconds:     60,
		ArbitratorTimeoutSeconds: 60,
	})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}

	_, err = db.CreateDCTToken(ctx, &DCTToken{
		TokenID:        tokenID,
		TokenHash:      tokenID + "-hash",
		EscrowID:       escrow.ID,
		Subject:        "worker",
		Issuer:         "buyer",
		OperationsJSON: `["submit"]`,
		ResourcesJSON:  `["escrow:1"]`,
		Profile:        "delegation-v1",
		CaveatsJSON:    `{}`,
		Depth:          0,
		ExpiresAt:      1999999999,
	})
	if err != nil {
		t.Fatalf("create dct token: %v", err)
	}
	return escrow
}

func TestRecordFreezeEscrowAndRevokeDCT(t *testing.T) {
	t.Parallel()

	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	escrow := createEmergencyTestEscrowAndToken(t, db, "tok-freeze")
	txHash := "0xabc123"

	if err := db.RecordFreezeEscrowAndRevokeDCT(context.Background(), escrow.ID, escrow.EscrowAddress, txHash); err != nil {
		t.Fatalf("record freeze escrow: %v", err)
	}

	gotEscrow, err := db.GetEscrow(context.Background(), escrow.ID)
	if err != nil {
		t.Fatalf("get escrow: %v", err)
	}
	if !gotEscrow.Frozen {
		t.Fatal("expected escrow to be frozen")
	}

	token, err := db.GetDCTTokenByTokenID(context.Background(), "tok-freeze")
	if err != nil {
		t.Fatalf("get token: %v", err)
	}
	if token.RevokedAt == nil {
		t.Fatal("expected token to be revoked")
	}
	if token.RevocationReason != "escrow_frozen" {
		t.Fatalf("expected revocation reason escrow_frozen, got %q", token.RevocationReason)
	}

	actions, err := db.ListEmergencyActions(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("list emergency actions: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("expected exactly 1 emergency action, got %d", len(actions))
	}
	if actions[0].Action != "freeze_escrow" {
		t.Fatalf("expected freeze_escrow action, got %q", actions[0].Action)
	}
	if actions[0].TxHash != txHash {
		t.Fatalf("expected tx hash %q, got %q", txHash, actions[0].TxHash)
	}
}

func TestRecordEmergencyResolveAndRevokeDCT(t *testing.T) {
	t.Parallel()

	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	escrow := createEmergencyTestEscrowAndToken(t, db, "tok-resolve")
	txHash := "0xdef456"

	if err := db.RecordEmergencyResolveAndRevokeDCT(context.Background(), escrow.ID, escrow.EscrowAddress, 6500, txHash); err != nil {
		t.Fatalf("record emergency resolve: %v", err)
	}

	gotEscrow, err := db.GetEscrow(context.Background(), escrow.ID)
	if err != nil {
		t.Fatalf("get escrow: %v", err)
	}
	if gotEscrow.Status != "resolved" {
		t.Fatalf("expected escrow status resolved, got %q", gotEscrow.Status)
	}

	token, err := db.GetDCTTokenByTokenID(context.Background(), "tok-resolve")
	if err != nil {
		t.Fatalf("get token: %v", err)
	}
	if token.RevokedAt == nil {
		t.Fatal("expected token to be revoked")
	}
	if token.RevocationReason != "emergency_resolve" {
		t.Fatalf("expected revocation reason emergency_resolve, got %q", token.RevocationReason)
	}

	actions, err := db.ListEmergencyActions(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("list emergency actions: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("expected exactly 1 emergency action, got %d", len(actions))
	}
	if actions[0].Action != "emergency_resolve" {
		t.Fatalf("expected emergency_resolve action, got %q", actions[0].Action)
	}
	if actions[0].TxHash != txHash {
		t.Fatalf("expected tx hash %q, got %q", txHash, actions[0].TxHash)
	}
	if !strings.Contains(actions[0].Reason, "workerAwardBps=6500") {
		t.Fatalf("expected workerAwardBps reason, got %q", actions[0].Reason)
	}
}
