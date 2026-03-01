package decomposition

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/bidding"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
)

const (
	testBuyerAddress      = "0x1000000000000000000000000000000000000001"
	testWorkerAddress     = "0x2000000000000000000000000000000000000002"
	testVerifierAddress   = "0x3000000000000000000000000000000000000003"
	testVerifierAddressB  = "0x4000000000000000000000000000000000000004"
	testArbitratorAddress = "0x5000000000000000000000000000000000000005"
	testFactoryAddress    = "0x6000000000000000000000000000000000000006"
)

func newDecompositionTestService(t *testing.T) (*Service, *storage.DB) {
	t.Helper()

	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	mock := chain.NewMockClient()
	cfg := &config.Config{
		FactoryAddress: testFactoryAddress,
		ChainID:        84532,
	}
	idx := indexer.New(db, mock, cfg.FactoryAddress, indexer.WithStartBlock(0))
	bidSvc := &bidding.Service{
		DB:    db,
		Chain: mock,
		Idx:   idx,
		Cfg:   cfg,
	}
	return &Service{
		DB:      db,
		Bidding: bidSvc,
	}, db
}

type settledEscrowSeed struct {
	serviceTier int
	quorum      bool
	zkProof     bool
}

func seedSettledEscrow(t *testing.T, db *storage.DB, idx int, seed settledEscrowSeed) {
	t.Helper()
	ctx := context.Background()

	task, err := db.CreateTask(ctx, fmt.Sprintf("task-%d", idx), "desc", fmt.Sprintf("0xspec%x", idx))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	zkVerifier := ""
	if seed.zkProof {
		zkVerifier = "0x7000000000000000000000000000000000000007"
	}
	verifierPanelJSON := "[]"
	if seed.quorum {
		verifierPanelJSON = `["` + strings.ToLower(testVerifierAddress) + `"]`
	}

	_, err = db.CreateEscrow(ctx, &storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  84532,
		FactoryAddress:           testFactoryAddress,
		EscrowAddress:            fmt.Sprintf("0x80000000000000000000000000000000000000%02x", idx),
		Buyer:                    testBuyerAddress,
		Worker:                   testWorkerAddress,
		Verifier:                 testVerifierAddress,
		VerifierPanelJSON:        verifierPanelJSON,
		QuorumThreshold:          1,
		QuorumVerifierCount:      1,
		VerifierStakePerVerifier: "0",
		Arbitrator:               testArbitratorAddress,
		Amount:                   "100",
		WorkerStake:              "0",
		Token:                    "",
		Status:                   "settled",
		SubmissionDeadline:       time.Now().Add(time.Hour).Unix(),
		ReviewPeriodSeconds:      60,
		DisputePeriodSeconds:     60,
		ArbitratorTimeoutSeconds: 60,
		ServiceTier:              seed.serviceTier,
		ZKVerifier:               zkVerifier,
		CircuitID:                "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	if err != nil {
		t.Fatalf("create settled escrow: %v", err)
	}
}

func seedCompletedWorkerReputation(t *testing.T, db *storage.DB, addr string) {
	t.Helper()
	if err := db.UpsertReputation(context.Background(), addr, "worker", "completed"); err != nil {
		t.Fatalf("seed worker reputation: %v", err)
	}
}

func findIssueByTempID(issues []StructuralIssue, tempID string) *StructuralIssue {
	for i := range issues {
		if issues[i].TempID == tempID {
			return &issues[i]
		}
	}
	return nil
}

func findContextByTempID(contexts []NodeMarketContext, tempID string) *NodeMarketContext {
	for i := range contexts {
		if contexts[i].TempID == tempID {
			return &contexts[i]
		}
	}
	return nil
}

func findNodeByTitle(nodes []*storage.DecompositionNode, title string) *storage.DecompositionNode {
	for _, node := range nodes {
		if node.Title == title {
			return node
		}
	}
	return nil
}

func defaultFinalizeParams(decompositionID int64) FinalizeParams {
	now := time.Now().Unix()
	return FinalizeParams{
		DecompositionID:          decompositionID,
		Buyer:                    testBuyerAddress,
		Token:                    "",
		Deadline:                 now + 7200,
		ReviewPeriodSeconds:      60,
		DisputePeriodSeconds:     60,
		ArbitratorTimeoutSeconds: 60,
		Arbitrator:               testArbitratorAddress,
		VerifierPanel:            []string{testVerifierAddress, testVerifierAddressB},
		QuorumCount:              1,
		BudgetMin:                "100",
		BudgetMax:                "200",
		CommitDeadline:           now + 600,
		RevealDeadline:           now + 1200,
		ExpiresAt:                now + 1800,
	}
}

func TestCreate_AllLeafValid(t *testing.T) {
	svc, db := newDecompositionTestService(t)
	seedSettledEscrow(t, db, 1, settledEscrowSeed{serviceTier: 0})
	seedSettledEscrow(t, db, 2, settledEscrowSeed{serviceTier: 1, quorum: true})
	seedSettledEscrow(t, db, 3, settledEscrowSeed{serviceTier: 1, zkProof: true})
	seedCompletedWorkerReputation(t, db, testWorkerAddress)

	result, err := svc.CreateDecomposition(context.Background(), CreateDecompositionParams{
		Buyer:       testBuyerAddress,
		Title:       "Complex Task",
		Description: "decompose this task",
		SubTasks: []SubTaskInput{
			{TempID: "root", Title: "Root", Description: "root node"},
			{TempID: "leaf-opt", ParentTempID: "root", Title: "Optimistic", Description: "opt", VerificationType: "optimistic"},
			{TempID: "leaf-quorum", ParentTempID: "root", Title: "Quorum", Description: "quorum", VerificationType: "quorum"},
			{
				TempID:           "leaf-zk",
				ParentTempID:     "root",
				Title:            "ZK",
				Description:      "zk",
				VerificationType: "zk_proof",
				VerificationDetails: map[string]any{
					"circuit_id": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				},
			},
			{TempID: "leaf-unit", ParentTempID: "root", Title: "Unit", Description: "unit", VerificationType: "unit_test"},
		},
	})
	if err != nil {
		t.Fatalf("create decomposition: %v", err)
	}
	if !result.Valid {
		t.Fatalf("expected valid decomposition, got invalid with issues: %+v", result.Issues)
	}
	if len(result.Issues) != 0 {
		t.Fatalf("expected zero structural issues, got %d", len(result.Issues))
	}
	if result.Decomposition.Status != "valid" {
		t.Fatalf("expected status valid, got %s", result.Decomposition.Status)
	}
	if len(result.MarketContext) != 4 {
		t.Fatalf("expected 4 market context entries, got %d", len(result.MarketContext))
	}
}

func TestCreate_MissingType(t *testing.T) {
	svc, _ := newDecompositionTestService(t)
	result, err := svc.CreateDecomposition(context.Background(), CreateDecompositionParams{
		Buyer:       testBuyerAddress,
		Title:       "Missing Type",
		Description: "leaf missing type",
		SubTasks: []SubTaskInput{
			{TempID: "leaf-1", Title: "Leaf 1", Description: "desc"},
		},
	})
	if err != nil {
		t.Fatalf("create decomposition: %v", err)
	}
	if result.Valid {
		t.Fatal("expected invalid decomposition")
	}
	issue := findIssueByTempID(result.Issues, "leaf-1")
	if issue == nil {
		t.Fatalf("expected issue for leaf-1, got %+v", result.Issues)
	}
	if issue.Reason != "verification_type required for leaf nodes" {
		t.Fatalf("unexpected issue reason: %s", issue.Reason)
	}
}

func TestCreate_ZKProofNoMarketHistory(t *testing.T) {
	svc, _ := newDecompositionTestService(t)
	result, err := svc.CreateDecomposition(context.Background(), CreateDecompositionParams{
		Buyer:       testBuyerAddress,
		Title:       "ZK First Mover",
		Description: "fresh zk market",
		SubTasks: []SubTaskInput{
			{
				TempID:           "zk-leaf",
				Title:            "ZK Leaf",
				Description:      "zk",
				VerificationType: "zk_proof",
				VerificationDetails: map[string]any{
					"circuit_id": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("create decomposition: %v", err)
	}
	if !result.Valid {
		t.Fatalf("expected valid=true for first mover zk market, issues=%+v", result.Issues)
	}
	ctx := findContextByTempID(result.MarketContext, "zk-leaf")
	if ctx == nil {
		t.Fatalf("missing market context for zk-leaf: %+v", result.MarketContext)
	}
	if ctx.Signal != "untested" {
		t.Fatalf("expected untested signal, got %s", ctx.Signal)
	}
	if !strings.Contains(strings.ToLower(ctx.Evidence), "no settled escrows with zk verification found") {
		t.Fatalf("unexpected evidence: %s", ctx.Evidence)
	}
}

func TestCreate_QuorumNoVerifiers(t *testing.T) {
	svc, _ := newDecompositionTestService(t)
	result, err := svc.CreateDecomposition(context.Background(), CreateDecompositionParams{
		Buyer:       testBuyerAddress,
		Title:       "Quorum First Mover",
		Description: "no verifier history",
		SubTasks: []SubTaskInput{
			{TempID: "quorum-leaf", Title: "Quorum Leaf", Description: "quorum", VerificationType: "quorum"},
		},
	})
	if err != nil {
		t.Fatalf("create decomposition: %v", err)
	}
	if !result.Valid {
		t.Fatalf("expected valid=true with no verifier history, issues=%+v", result.Issues)
	}
	ctx := findContextByTempID(result.MarketContext, "quorum-leaf")
	if ctx == nil {
		t.Fatalf("missing market context for quorum-leaf: %+v", result.MarketContext)
	}
	if ctx.Signal != "untested" {
		t.Fatalf("expected untested signal, got %s", ctx.Signal)
	}
	if !strings.Contains(strings.ToLower(ctx.Evidence), "no verifier history") {
		t.Fatalf("expected no verifier history evidence, got %s", ctx.Evidence)
	}
}

func TestCreate_ZKProofMissingCircuitID(t *testing.T) {
	svc, _ := newDecompositionTestService(t)
	result, err := svc.CreateDecomposition(context.Background(), CreateDecompositionParams{
		Buyer:       testBuyerAddress,
		Title:       "ZK Missing Circuit",
		Description: "missing circuit_id",
		SubTasks: []SubTaskInput{
			{
				TempID:                       "zk-no-circuit",
				Title:                        "ZK Leaf",
				Description:                  "zk",
				VerificationType:             "zk_proof",
				VerificationDetails:          map[string]any{},
				ParentTempID:                 "",
				RequiresFurtherDecomposition: false,
			},
		},
	})
	if err != nil {
		t.Fatalf("create decomposition: %v", err)
	}
	if result.Valid {
		t.Fatal("expected invalid decomposition when zk circuit_id missing")
	}
	issue := findIssueByTempID(result.Issues, "zk-no-circuit")
	if issue == nil {
		t.Fatalf("expected issue for zk-no-circuit, got %+v", result.Issues)
	}
	if issue.Reason != "zk_proof requires circuit_id in verification_details" {
		t.Fatalf("unexpected issue reason: %s", issue.Reason)
	}
}

func TestCreate_RequiresFurtherDecomposition(t *testing.T) {
	svc, _ := newDecompositionTestService(t)
	result, err := svc.CreateDecomposition(context.Background(), CreateDecompositionParams{
		Buyer:       testBuyerAddress,
		Title:       "Requires Further Decomposition",
		Description: "leaf should fail",
		SubTasks: []SubTaskInput{
			{
				TempID:                       "leaf-split",
				Title:                        "Leaf",
				Description:                  "leaf",
				VerificationType:             "optimistic",
				RequiresFurtherDecomposition: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("create decomposition: %v", err)
	}
	if result.Valid {
		t.Fatal("expected invalid decomposition")
	}
	issue := findIssueByTempID(result.Issues, "leaf-split")
	if issue == nil {
		t.Fatalf("expected issue for leaf-split, got %+v", result.Issues)
	}
	if issue.Reason != "node flagged as requiring further decomposition" {
		t.Fatalf("unexpected issue reason: %s", issue.Reason)
	}
}

func TestCreate_InternalNodeIgnored(t *testing.T) {
	svc, _ := newDecompositionTestService(t)
	result, err := svc.CreateDecomposition(context.Background(), CreateDecompositionParams{
		Buyer:       testBuyerAddress,
		Title:       "Internal Node",
		Description: "internal node can omit type",
		SubTasks: []SubTaskInput{
			{TempID: "root", Title: "Root", Description: "root", VerificationType: ""},
			{TempID: "leaf", ParentTempID: "root", Title: "Leaf", Description: "leaf", VerificationType: "optimistic"},
		},
	})
	if err != nil {
		t.Fatalf("create decomposition: %v", err)
	}
	if !result.Valid {
		t.Fatalf("expected valid decomposition, issues=%+v", result.Issues)
	}
	if len(result.Issues) != 0 {
		t.Fatalf("expected zero issues, got %d", len(result.Issues))
	}
}

func TestCreate_DelegatePreferencePersisted(t *testing.T) {
	svc, _ := newDecompositionTestService(t)
	result, err := svc.CreateDecomposition(context.Background(), CreateDecompositionParams{
		Buyer:       testBuyerAddress,
		Title:       "Human Preference",
		Description: "persist delegate preference",
		SubTasks: []SubTaskInput{
			{TempID: "root", Title: "Root", Description: "root"},
			{
				TempID:             "leaf-human",
				ParentTempID:       "root",
				Title:              "Human Review",
				Description:        "needs judgment",
				VerificationType:   "optimistic",
				DelegatePreference: "human",
			},
		},
	})
	if err != nil {
		t.Fatalf("create decomposition: %v", err)
	}
	if !result.Valid {
		t.Fatalf("expected valid decomposition, issues=%+v", result.Issues)
	}
	createdLeaf := findNodeByTitle(result.Nodes, "Human Review")
	if createdLeaf == nil {
		t.Fatalf("missing created leaf node: %+v", result.Nodes)
	}
	if createdLeaf.DelegatePreference != "human" {
		t.Fatalf("expected delegate_preference=human, got %q", createdLeaf.DelegatePreference)
	}

	_, storedNodes, err := svc.GetDecomposition(context.Background(), result.Decomposition.ID)
	if err != nil {
		t.Fatalf("get decomposition: %v", err)
	}
	storedLeaf := findNodeByTitle(storedNodes, "Human Review")
	if storedLeaf == nil {
		t.Fatalf("missing stored leaf node: %+v", storedNodes)
	}
	if storedLeaf.DelegatePreference != "human" {
		t.Fatalf("expected stored delegate_preference=human, got %q", storedLeaf.DelegatePreference)
	}
}

func TestCreate_UnsupportedDelegatePreference(t *testing.T) {
	svc, _ := newDecompositionTestService(t)
	_, err := svc.CreateDecomposition(context.Background(), CreateDecompositionParams{
		Buyer:       testBuyerAddress,
		Title:       "Bad Preference",
		Description: "invalid delegate preference",
		SubTasks: []SubTaskInput{
			{
				TempID:             "leaf-1",
				Title:              "Leaf 1",
				Description:        "desc",
				VerificationType:   "optimistic",
				DelegatePreference: "robot",
			},
		},
	})
	if err == nil {
		t.Fatal("expected error for unsupported delegate_preference")
	}
	if !strings.Contains(err.Error(), "unsupported delegate_preference") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreate_DelegatePreferenceOnInternalNodeRejected(t *testing.T) {
	svc, _ := newDecompositionTestService(t)
	_, err := svc.CreateDecomposition(context.Background(), CreateDecompositionParams{
		Buyer:       testBuyerAddress,
		Title:       "Internal Preference",
		Description: "internal node delegate preference should fail",
		SubTasks: []SubTaskInput{
			{TempID: "root", Title: "Root", Description: "root", DelegatePreference: "human"},
			{TempID: "leaf", ParentTempID: "root", Title: "Leaf", Description: "leaf", VerificationType: "optimistic"},
		},
	})
	if err == nil {
		t.Fatal("expected error for non-leaf delegate_preference")
	}
	if !strings.Contains(err.Error(), "non-empty delegate_preference only allowed on leaf nodes") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFinalize_CreatesRFQsWithCorrectTier(t *testing.T) {
	svc, db := newDecompositionTestService(t)
	createRes, err := svc.CreateDecomposition(context.Background(), CreateDecompositionParams{
		Buyer:       testBuyerAddress,
		Title:       "Finalize Me",
		Description: "finalize into rfqs",
		SubTasks: []SubTaskInput{
			{TempID: "root", Title: "Root", Description: "root"},
			{TempID: "leaf-opt", ParentTempID: "root", Title: "Leaf optimistic", Description: "opt", VerificationType: "optimistic"},
			{TempID: "leaf-quorum", ParentTempID: "root", Title: "Leaf quorum", Description: "quorum", VerificationType: "quorum"},
		},
	})
	if err != nil {
		t.Fatalf("create decomposition: %v", err)
	}
	if !createRes.Valid {
		t.Fatalf("expected valid decomposition, issues=%+v", createRes.Issues)
	}

	finalized, rfqIDs, err := svc.FinalizeDecomposition(context.Background(), defaultFinalizeParams(createRes.Decomposition.ID))
	if err != nil {
		t.Fatalf("finalize decomposition: %v", err)
	}
	if finalized.Status != "finalized" {
		t.Fatalf("expected finalized status, got %s", finalized.Status)
	}
	if len(rfqIDs) != 2 {
		t.Fatalf("expected 2 RFQs, got %d", len(rfqIDs))
	}

	nodes, err := db.ListDecompositionNodes(context.Background(), createRes.Decomposition.ID)
	if err != nil {
		t.Fatalf("list decomposition nodes: %v", err)
	}
	rfqByVerificationType := make(map[string]*storage.RFQ)
	for _, node := range nodes {
		if node.RFQID == nil {
			continue
		}
		rfq, getErr := db.GetRFQ(context.Background(), *node.RFQID)
		if getErr != nil {
			t.Fatalf("get rfq %d: %v", *node.RFQID, getErr)
		}
		rfqByVerificationType[node.VerificationType] = rfq
	}

	optRFQ := rfqByVerificationType["optimistic"]
	if optRFQ == nil {
		t.Fatal("missing optimistic RFQ")
	}
	if optRFQ.ServiceTier != 0 {
		t.Fatalf("expected optimistic service_tier=0, got %d", optRFQ.ServiceTier)
	}
	if optRFQ.BiddingMode != "open" {
		t.Fatalf("expected optimistic bidding_mode=open, got %s", optRFQ.BiddingMode)
	}

	quorumRFQ := rfqByVerificationType["quorum"]
	if quorumRFQ == nil {
		t.Fatal("missing quorum RFQ")
	}
	if quorumRFQ.ServiceTier != 1 {
		t.Fatalf("expected quorum service_tier=1, got %d", quorumRFQ.ServiceTier)
	}
	if quorumRFQ.BiddingMode != "sealed" {
		t.Fatalf("expected quorum bidding_mode=sealed, got %s", quorumRFQ.BiddingMode)
	}
}

func TestFinalize_RequiresValidStatus(t *testing.T) {
	svc, _ := newDecompositionTestService(t)
	createRes, err := svc.CreateDecomposition(context.Background(), CreateDecompositionParams{
		Buyer:       testBuyerAddress,
		Title:       "Draft Decomposition",
		Description: "missing verification_type keeps draft",
		SubTasks: []SubTaskInput{
			{TempID: "leaf", Title: "Leaf", Description: "leaf"},
		},
	})
	if err != nil {
		t.Fatalf("create decomposition: %v", err)
	}
	if createRes.Decomposition.Status != "draft" {
		t.Fatalf("expected draft status, got %s", createRes.Decomposition.Status)
	}

	_, _, err = svc.FinalizeDecomposition(context.Background(), defaultFinalizeParams(createRes.Decomposition.ID))
	if err == nil {
		t.Fatal("expected finalize to fail for draft decomposition")
	}
	if !strings.Contains(err.Error(), "must be valid") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFinalize_QuorumCountMustNotExceedVerifierPanelSize(t *testing.T) {
	svc, _ := newDecompositionTestService(t)
	createRes, err := svc.CreateDecomposition(context.Background(), CreateDecompositionParams{
		Buyer:       testBuyerAddress,
		Title:       "Quorum Limits",
		Description: "quorum count bounds",
		SubTasks: []SubTaskInput{
			{TempID: "root", Title: "Root", Description: "root"},
			{TempID: "leaf-quorum", ParentTempID: "root", Title: "Leaf quorum", Description: "quorum", VerificationType: "quorum"},
		},
	})
	if err != nil {
		t.Fatalf("create decomposition: %v", err)
	}
	if !createRes.Valid {
		t.Fatalf("expected valid decomposition, issues=%+v", createRes.Issues)
	}

	params := defaultFinalizeParams(createRes.Decomposition.ID)
	params.VerifierPanel = []string{testVerifierAddress}
	params.QuorumCount = 2

	_, _, err = svc.FinalizeDecomposition(context.Background(), params)
	if err == nil {
		t.Fatal("expected finalize to fail when quorum_count exceeds verifier_panel size")
	}
	if !strings.Contains(err.Error(), "quorum_count must be <= verifier_panel size") {
		t.Fatalf("unexpected error: %v", err)
	}
}
