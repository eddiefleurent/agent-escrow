package ap2

import (
	"context"
	"testing"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/x402"
)

func setupTestService(t *testing.T) (*Service, *storage.DB, *chain.MockClient) {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mc := chain.NewMockClient()
	factoryAddr := "0x1234567890123456789012345678901234567890"
	cfg := &config.Config{
		ChainID:        84532,
		FactoryAddress: factoryAddr,
		Port:           8080,
		X402Enabled:    true,
	}
	idx := indexer.New(db, mc, factoryAddr)

	svc := &Service{
		DB:    db,
		Chain: mc,
		Idx:   idx,
		Cfg:   cfg,
		X402:  x402.NewClient("http://localhost:9999"),
	}
	return svc, db, mc
}

func TestValidateMandate_Valid(t *testing.T) {
	svc, _, _ := setupTestService(t)

	env := MandateEnvelope{
		Type:          MandateTypeIntent,
		Payload:       map[string]any{"budget_amount": "1000000", "budget_currency": "USDC"},
		Signature:     "0xdeadbeef",
		SignerAddress: "0x1111111111111111111111111111111111111111",
		Authorization: EIP3009Authorization{
			From:        "0x1111111111111111111111111111111111111111",
			To:          "0x2222222222222222222222222222222222222222",
			Value:       "1000000",
			ValidAfter:  "0",
			ValidBefore: "9999999999",
			Nonce:       "0x0000000000000000000000000000000000000000000000000000000000000001",
			V:           27,
			R:           "0x0000000000000000000000000000000000000000000000000000000000000002",
			S:           "0x0000000000000000000000000000000000000000000000000000000000000003",
		},
	}

	err := svc.ValidateMandate(context.Background(), env)
	if err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

func TestValidateMandate_MissingType(t *testing.T) {
	svc, _, _ := setupTestService(t)

	env := MandateEnvelope{
		SignerAddress: "0x1111111111111111111111111111111111111111",
		Signature:     "0xdeadbeef",
		Authorization: EIP3009Authorization{From: "0x1111111111111111111111111111111111111111", Value: "100"},
	}

	err := svc.ValidateMandate(context.Background(), env)
	if err == nil {
		t.Fatal("expected error for missing type")
	}
}

func TestValidateMandate_SignerMismatch(t *testing.T) {
	svc, _, _ := setupTestService(t)

	env := MandateEnvelope{
		Type:          MandateTypePayment,
		Payload:       map[string]any{},
		Signature:     "0xdeadbeef",
		SignerAddress: "0x1111111111111111111111111111111111111111",
		Authorization: EIP3009Authorization{
			From:  "0x2222222222222222222222222222222222222222",
			Value: "100",
		},
	}

	err := svc.ValidateMandate(context.Background(), env)
	if err == nil {
		t.Fatal("expected error for signer mismatch")
	}
}

func TestFundViaMandate_HappyPath(t *testing.T) {
	svc, db, _ := setupTestService(t)

	task, err := db.CreateTask("Test Task", "Description", "0xhash")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	escrow, err := db.CreateEscrow(&storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  84532,
		FactoryAddress:           "0x1234567890123456789012345678901234567890",
		EscrowAddress:            "0xABCDABCDABCDABCDABCDABCDABCDABCDABCDABCD",
		EscrowID:                 1,
		Buyer:                    "0x1111111111111111111111111111111111111111",
		Worker:                   "0x2222222222222222222222222222222222222222",
		Verifier:                 "0x3333333333333333333333333333333333333333",
		Arbitrator:               "0x4444444444444444444444444444444444444444",
		Amount:                   "1000000",
		Token:                    "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		Status:                   "created",
		SubmissionDeadline:       9999999999,
		ReviewPeriodSeconds:      86400,
		DisputePeriodSeconds:     172800,
		ArbitratorTimeoutSeconds: 604800,
		MilestoneCount:           1,
		ActiveWorker:             "0x2222222222222222222222222222222222222222",
	})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}

	env := MandateEnvelope{
		Type:          MandateTypePayment,
		Payload:       map[string]any{"amount": "1000000"},
		Signature:     "0xdeadbeef",
		SignerAddress: "0x1111111111111111111111111111111111111111",
		Authorization: EIP3009Authorization{
			From:        "0x1111111111111111111111111111111111111111",
			To:          "0xABCDABCDABCDABCDABCDABCDABCDABCDABCDABCD",
			Value:       "1000000",
			ValidAfter:  "0",
			ValidBefore: "9999999999",
			Nonce:       "0x0000000000000000000000000000000000000000000000000000000000000001",
			V:           27,
			R:           "0x0000000000000000000000000000000000000000000000000000000000000002",
			S:           "0x0000000000000000000000000000000000000000000000000000000000000003",
		},
	}

	resp, err := svc.FundViaMandate(context.Background(), escrow.ID, env)
	if err != nil {
		t.Fatalf("fund via mandate: %v", err)
	}

	if resp.EscrowID != escrow.ID {
		t.Fatalf("expected escrow_id %d, got %d", escrow.ID, resp.EscrowID)
	}
	if resp.TxHash == "" {
		t.Fatal("expected non-empty tx_hash")
	}
	if resp.Status != "funded" {
		t.Fatalf("expected status 'funded', got %s", resp.Status)
	}
	if resp.MandateID == "" {
		t.Fatal("expected non-empty mandate_id")
	}
}

func TestFundViaMandate_BuyerMismatch(t *testing.T) {
	svc, db, _ := setupTestService(t)

	task, err := db.CreateTask("Test Task", "Description", "0xhash")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	escrow, err := db.CreateEscrow(&storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  84532,
		FactoryAddress:           "0x1234567890123456789012345678901234567890",
		EscrowAddress:            "0xABCDABCDABCDABCDABCDABCDABCDABCDABCDABCD",
		EscrowID:                 1,
		Buyer:                    "0x1111111111111111111111111111111111111111",
		Worker:                   "0x2222222222222222222222222222222222222222",
		Verifier:                 "0x3333333333333333333333333333333333333333",
		Arbitrator:               "0x4444444444444444444444444444444444444444",
		Amount:                   "1000000",
		Token:                    "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		Status:                   "created",
		SubmissionDeadline:       9999999999,
		ReviewPeriodSeconds:      86400,
		DisputePeriodSeconds:     172800,
		ArbitratorTimeoutSeconds: 604800,
		MilestoneCount:           1,
		ActiveWorker:             "0x2222222222222222222222222222222222222222",
	})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}

	env := MandateEnvelope{
		Type:          MandateTypePayment,
		Payload:       map[string]any{},
		Signature:     "0xdeadbeef",
		SignerAddress: "0x9999999999999999999999999999999999999999",
		Authorization: EIP3009Authorization{
			From:        "0x9999999999999999999999999999999999999999",
			To:          "0xABCDABCDABCDABCDABCDABCDABCDABCDABCDABCD",
			Value:       "1000000",
			ValidAfter:  "0",
			ValidBefore: "9999999999",
			Nonce:       "0x0000000000000000000000000000000000000000000000000000000000000001",
			V:           27,
			R:           "0x0000000000000000000000000000000000000000000000000000000000000002",
			S:           "0x0000000000000000000000000000000000000000000000000000000000000003",
		},
	}

	_, err = svc.FundViaMandate(context.Background(), escrow.ID, env)
	if err == nil {
		t.Fatal("expected error for buyer mismatch")
	}
}

func TestFundViaMandate_InsufficientValue(t *testing.T) {
	svc, db, _ := setupTestService(t)

	task, err := db.CreateTask("Test Task", "Description", "0xhash")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	escrow, err := db.CreateEscrow(&storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  84532,
		FactoryAddress:           "0x1234567890123456789012345678901234567890",
		EscrowAddress:            "0xABCDABCDABCDABCDABCDABCDABCDABCDABCDABCD",
		EscrowID:                 1,
		Buyer:                    "0x1111111111111111111111111111111111111111",
		Worker:                   "0x2222222222222222222222222222222222222222",
		Verifier:                 "0x3333333333333333333333333333333333333333",
		Arbitrator:               "0x4444444444444444444444444444444444444444",
		Amount:                   "1000000",
		Token:                    "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		Status:                   "created",
		SubmissionDeadline:       9999999999,
		ReviewPeriodSeconds:      86400,
		DisputePeriodSeconds:     172800,
		ArbitratorTimeoutSeconds: 604800,
		MilestoneCount:           1,
		ActiveWorker:             "0x2222222222222222222222222222222222222222",
	})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}

	env := MandateEnvelope{
		Type:          MandateTypePayment,
		Payload:       map[string]any{},
		Signature:     "0xdeadbeef",
		SignerAddress: "0x1111111111111111111111111111111111111111",
		Authorization: EIP3009Authorization{
			From:        "0x1111111111111111111111111111111111111111",
			To:          "0xABCDABCDABCDABCDABCDABCDABCDABCDABCDABCD",
			Value:       "500000",
			ValidAfter:  "0",
			ValidBefore: "9999999999",
			Nonce:       "0x0000000000000000000000000000000000000000000000000000000000000001",
			V:           27,
			R:           "0x0000000000000000000000000000000000000000000000000000000000000002",
			S:           "0x0000000000000000000000000000000000000000000000000000000000000003",
		},
	}

	_, err = svc.FundViaMandate(context.Background(), escrow.ID, env)
	if err == nil {
		t.Fatal("expected error for insufficient value")
	}
}

func TestFundViaMandate_AuthToMismatch(t *testing.T) {
	svc, db, _ := setupTestService(t)

	task, err := db.CreateTask("Test Task", "Description", "0xhash")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	escrow, err := db.CreateEscrow(&storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  84532,
		FactoryAddress:           "0x1234567890123456789012345678901234567890",
		EscrowAddress:            "0xABCDABCDABCDABCDABCDABCDABCDABCDABCDABCD",
		EscrowID:                 1,
		Buyer:                    "0x1111111111111111111111111111111111111111",
		Worker:                   "0x2222222222222222222222222222222222222222",
		Verifier:                 "0x3333333333333333333333333333333333333333",
		Arbitrator:               "0x4444444444444444444444444444444444444444",
		Amount:                   "1000000",
		Token:                    "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		Status:                   "created",
		SubmissionDeadline:       9999999999,
		ReviewPeriodSeconds:      86400,
		DisputePeriodSeconds:     172800,
		ArbitratorTimeoutSeconds: 604800,
		MilestoneCount:           1,
		ActiveWorker:             "0x2222222222222222222222222222222222222222",
	})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}

	env := MandateEnvelope{
		Type:          MandateTypePayment,
		Payload:       map[string]any{},
		Signature:     "0xdeadbeef",
		SignerAddress: "0x1111111111111111111111111111111111111111",
		Authorization: EIP3009Authorization{
			From:        "0x1111111111111111111111111111111111111111",
			To:          "0x9999999999999999999999999999999999999999",
			Value:       "1000000",
			ValidAfter:  "0",
			ValidBefore: "9999999999",
			Nonce:       "0x0000000000000000000000000000000000000000000000000000000000000001",
			V:           27,
			R:           "0x0000000000000000000000000000000000000000000000000000000000000002",
			S:           "0x0000000000000000000000000000000000000000000000000000000000000003",
		},
	}

	_, err = svc.FundViaMandate(context.Background(), escrow.ID, env)
	if err == nil {
		t.Fatal("expected error for authorization.to mismatch")
	}
}
