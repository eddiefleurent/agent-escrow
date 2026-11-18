package storage

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestAP2LegacyNonceBackfill(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()

	task, err := db.CreateTask(ctx, "legacy", "desc", "0xhash")
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	escrow, err := db.CreateEscrow(ctx, &Escrow{
		TaskID: task.ID, ChainID: 84532, FactoryAddress: "0x1234567890123456789012345678901234567890",
		EscrowAddress: "0xabcdabcdabcdabcdabcdabcdabcdabcdabcdabcd", EscrowID: 1,
		Buyer: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Worker: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Verifier: "0xcccccccccccccccccccccccccccccccccccccccc", Arbitrator: "0xdddddddddddddddddddddddddddddddddddddddd",
		Amount: "1000000", Token: "0x036CbD53842c5426634e7929541eC2318f3dCF7e", Status: "created",
		SubmissionDeadline: 9999999999, ReviewPeriodSeconds: 1, DisputePeriodSeconds: 1, ArbitratorTimeoutSeconds: 1,
		MilestoneCount: 1, ActiveWorker: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	})
	if err != nil {
		t.Fatalf("escrow: %v", err)
	}

	nonce := "0x" + strings.Repeat("ab", 32)
	// sha256-id rows: id is the first 16 hex chars of the sha256 mandate hash.
	hash := "aabbccddeeff0011" + strings.Repeat("11", 24)
	_, err = db.SQLDB().ExecContext(ctx,
		`INSERT INTO ap2_mandates (id, mandate_type, mandate_hash, signer_address, escrow_id, status, raw_payload)
		 VALUES (?, 'payment', ?, '0xAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAa', ?, 'bound', ?)`,
		hash[:16], hash, escrow.ID, `{"nonce":"`+nonce+`"}`)
	if err != nil {
		t.Fatalf("insert payment legacy: %v", err)
	}
	noNonceHash := "bbccddeeff001122" + strings.Repeat("22", 24)
	_, err = db.SQLDB().ExecContext(ctx,
		`INSERT INTO ap2_mandates (id, mandate_type, mandate_hash, signer_address, escrow_id, status, raw_payload)
		 VALUES (?, 'intent', ?, '0xAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAa', ?, 'bound', ?)`,
		noNonceHash[:16], noNonceHash, escrow.ID, `{"budget_amount":"1","budget_currency":"USDC"}`)
	if err != nil {
		t.Fatalf("insert intent legacy: %v", err)
	}

	sqlBytes, err := os.ReadFile("migrations/028_ap2_mandate_funding_state.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	for _, stmt := range splitStatements(string(sqlBytes)) {
		if !strings.HasPrefix(stmt, "UPDATE") && !strings.HasPrefix(stmt, "CREATE UNIQUE") {
			continue
		}
		if _, err := db.SQLDB().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("re-run %s: %v", stmt[:20], err)
		}
	}

	got, err := db.GetAP2Mandate(ctx, hash[:16])
	if err != nil {
		t.Fatalf("get backfilled: %v", err)
	}
	if got.AuthorizationNonce != strings.ToLower(nonce) {
		t.Fatalf("nonce %q", got.AuthorizationNonce)
	}
	if got.ChainID != 84532 || got.Token == "" || got.Authorizer == "" {
		t.Fatalf("backfill incomplete: %+v", got)
	}
	if got.Status != "failed" {
		t.Fatalf("bound row without a tx hash should be failed, got %s", got.Status)
	}

	skipped, err := db.GetAP2Mandate(ctx, noNonceHash[:16])
	if err != nil {
		t.Fatalf("get skipped: %v", err)
	}
	if skipped.AuthorizationNonce != "" {
		t.Fatalf("intent row without a nonce should stay unset, got %q", skipped.AuthorizationNonce)
	}
	if skipped.Status != "failed" {
		t.Fatalf("bound intent row should be failed, got %s", skipped.Status)
	}

	_, err = db.SQLDB().ExecContext(ctx,
		`INSERT INTO ap2_mandates (id, mandate_type, mandate_hash, signer_address, status, raw_payload, chain_id, token, authorizer, authorization_nonce)
		 VALUES ('cccccccccccccccc', 'payment', 'cc', '0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'pending', '{}', 84532, ?, ?, ?)`,
		got.Token, got.Authorizer, got.AuthorizationNonce)
	if err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("expected unique nonce rejection, got %v", err)
	}
}
