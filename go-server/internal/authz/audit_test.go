package authz

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func testAuditDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.ExecContext(context.Background(), `CREATE TABLE dct_authorization_audit (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp       TEXT NOT NULL DEFAULT (datetime('now')),
		operation       TEXT NOT NULL,
		allowed         INTEGER NOT NULL,
		caller_address  TEXT NOT NULL,
		escrow_id       INTEGER,
		token_id        TEXT,
		parent_token_id TEXT,
		reason          TEXT NOT NULL,
		request_id      TEXT,
		metadata        TEXT
	)`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestAuditLog_Allowed(t *testing.T) {
	db := testAuditDB(t)
	store := &SQLiteAuditStore{DB: db}
	ctx := context.Background()

	err := store.LogAuthzDecision(ctx, AuditEntry{
		Operation:     OpMint,
		Allowed:       true,
		CallerAddress: "0xbuyer",
		EscrowID:      42,
		Reason:        ReasonAllowed,
		RequestID:     "req-1",
		Metadata:      map[string]any{"subject": "agent-a"},
	})
	if err != nil {
		t.Fatal(err)
	}

	records, err := store.ListAuthzAudit(ctx, 42, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	r := records[0]
	if r.Operation != "mint" || !r.Allowed || r.CallerAddress != "0xbuyer" || r.EscrowID != 42 {
		t.Fatalf("unexpected record: %+v", r)
	}
	if r.Reason != "allowed" {
		t.Fatalf("expected reason 'allowed', got %q", r.Reason)
	}
}

func TestAuditLog_Denied(t *testing.T) {
	db := testAuditDB(t)
	store := &SQLiteAuditStore{DB: db}
	ctx := context.Background()

	err := store.LogAuthzDecision(ctx, AuditEntry{
		Operation:     OpMint,
		Allowed:       false,
		CallerAddress: "0xworker",
		EscrowID:      42,
		Reason:        ReasonCallerNotBuyer,
	})
	if err != nil {
		t.Fatal(err)
	}

	records, err := store.ListAuthzAudit(ctx, 0, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Allowed {
		t.Fatal("expected denied record")
	}
	if records[0].Reason != "caller_not_buyer" {
		t.Fatalf("expected caller_not_buyer, got %q", records[0].Reason)
	}
}

func TestAuditLog_EmergencyOverride(t *testing.T) {
	db := testAuditDB(t)
	store := &SQLiteAuditStore{DB: db}
	ctx := context.Background()

	err := store.LogAuthzDecision(ctx, AuditEntry{
		Operation:     Operation("emergency_override"),
		Allowed:       true,
		CallerAddress: "0xowner",
		EscrowID:      42,
		Reason:        ReasonEmergencyOverride,
		Metadata:      map[string]any{"target_operation": "revoke_all", "override_reason": "compromised key"},
	})
	if err != nil {
		t.Fatal(err)
	}

	records, err := store.ListAuthzAudit(ctx, 42, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Reason != "emergency_override" {
		t.Fatalf("expected emergency_override reason, got %q", records[0].Reason)
	}
}

func TestAuditLog_ListAll(t *testing.T) {
	db := testAuditDB(t)
	store := &SQLiteAuditStore{DB: db}
	ctx := context.Background()

	for i := range 5 {
		_ = store.LogAuthzDecision(ctx, AuditEntry{
			Operation:     OpMint,
			Allowed:       true,
			CallerAddress: "0xbuyer",
			EscrowID:      int64(i + 1),
			Reason:        ReasonAllowed,
		})
	}

	// List all (escrowID=0)
	records, err := store.ListAuthzAudit(ctx, 0, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 5 {
		t.Fatalf("expected 5 records, got %d", len(records))
	}

	// List by escrow
	records, err = store.ListAuthzAudit(ctx, 3, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record for escrow 3, got %d", len(records))
	}
}
