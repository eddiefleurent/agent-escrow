package decomposition

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/bidding"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
)

const zeroAddress = "0x0000000000000000000000000000000000000000"

var supportedVerificationTypes = map[string]struct{}{
	"":           {},
	"optimistic": {},
	"quorum":     {},
	"zk_proof":   {},
	"unit_test":  {},
}

// SubTaskInput is a node in the decomposition proposal from the calling agent.
type SubTaskInput struct {
	TempID                       string         `json:"temp_id"`        // client-assigned, for referencing parents
	ParentTempID                 string         `json:"parent_temp_id"` // "" for root nodes
	Title                        string         `json:"title"`
	Description                  string         `json:"description"`
	VerificationType             string         `json:"verification_type"` // optimistic|quorum|zk_proof|unit_test|""
	VerificationDetails          map[string]any `json:"verification_details"`
	RequiresFurtherDecomposition bool           `json:"requires_further_decomposition"`
}

// StructuralIssue is a hard validation failure - the node literally cannot be settled as-is.
// These are technical blockers, not market opinions.
type StructuralIssue struct {
	NodeID int64  `json:"node_id"`
	TempID string `json:"temp_id"`
	Title  string `json:"title"`
	Reason string `json:"reason"` // e.g. "verification_type required for leaf nodes"
	// e.g. "zk_proof requires circuit_id in verification_details"
	// e.g. "node flagged as requiring further decomposition"
}

// NodeMarketContext is informational market intelligence for one leaf node.
// Never gates validation - provided so the caller can make an informed choice.
type NodeMarketContext struct {
	NodeID           int64  `json:"node_id"`
	TempID           string `json:"temp_id"`
	VerificationType string `json:"verification_type"`
	MarketDepth      int    `json:"market_depth"`   // settled escrows using this type historically
	VerifierCount    int    `json:"verifier_count"` // addresses with relevant reputation
	Signal           string `json:"signal"`         // "proven" | "emerging" | "untested"
	Evidence         string `json:"evidence"`       // human-readable summary
}

// CreateDecompositionParams are inputs for creating/revising a decomposition.
type CreateDecompositionParams struct {
	Buyer       string
	Title       string
	Description string
	SpecHash    string
	SubTasks    []SubTaskInput
}

// CreateResult is returned from CreateDecomposition.
type CreateResult struct {
	Decomposition *storage.Decomposition
	Nodes         []*storage.DecompositionNode
	Issues        []StructuralIssue   // hard blockers - empty when valid=true
	MarketContext []NodeMarketContext // informational per-leaf, always returned
	Valid         bool                // true when Issues is empty
}

// FinalizeParams are inputs for creating RFQs from a validated decomposition.
type FinalizeParams struct {
	DecompositionID          int64
	Buyer                    string
	Token                    string
	Deadline                 int64
	ReviewPeriodSeconds      int64
	DisputePeriodSeconds     int64
	ArbitratorTimeoutSeconds int64
	Arbitrator               string
	VerifierPanel            []string // used for quorum/zk_proof nodes
	QuorumCount              int
	BudgetMin                string
	BudgetMax                string
	CommitDeadline           int64
	RevealDeadline           int64
	ExpiresAt                int64
}

// Service holds dependencies.
type Service struct {
	DB      *storage.DB
	Bidding *bidding.Service
}

func (s *Service) CreateDecomposition(ctx context.Context, p CreateDecompositionParams) (*CreateResult, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db is required")
	}
	if strings.TrimSpace(p.Buyer) == "" {
		return nil, errors.New("buyer is required")
	}
	if strings.TrimSpace(p.Title) == "" {
		return nil, errors.New("title is required")
	}
	if len(p.SubTasks) == 0 {
		return nil, errors.New("sub_tasks is required")
	}

	inputByTempID := make(map[string]SubTaskInput, len(p.SubTasks))
	order := make([]string, 0, len(p.SubTasks))
	for i, in := range p.SubTasks {
		tempID := strings.TrimSpace(in.TempID)
		if tempID == "" {
			return nil, fmt.Errorf("sub_tasks[%d].temp_id is required", i)
		}
		if _, exists := inputByTempID[tempID]; exists {
			return nil, fmt.Errorf("duplicate temp_id %q", tempID)
		}
		in.TempID = tempID
		in.ParentTempID = strings.TrimSpace(in.ParentTempID)
		in.VerificationType = strings.TrimSpace(in.VerificationType)
		if _, ok := supportedVerificationTypes[in.VerificationType]; !ok {
			return nil, fmt.Errorf("sub_tasks[%d] has unsupported verification_type %q", i, in.VerificationType)
		}
		inputByTempID[tempID] = in
		order = append(order, tempID)
	}
	for _, in := range inputByTempID {
		if in.ParentTempID == "" {
			continue
		}
		if _, ok := inputByTempID[in.ParentTempID]; !ok {
			return nil, fmt.Errorf("sub_task %q references unknown parent_temp_id %q", in.TempID, in.ParentTempID)
		}
	}

	tx, err := s.DB.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin decomposition tx: %w", err)
	}

	decomp, err := s.DB.CreateDecompositionTx(ctx, tx, &storage.Decomposition{
		Buyer:                p.Buyer,
		Title:                p.Title,
		Description:          p.Description,
		SpecHash:             p.SpecHash,
		Status:               "draft",
		ValidationErrorsJSON: "[]",
		RFQIDsJSON:           "[]",
	})
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}

	pending := make(map[string]SubTaskInput, len(inputByTempID))
	for tempID, in := range inputByTempID {
		pending[tempID] = in
	}
	nodeByTempID := make(map[string]*storage.DecompositionNode, len(inputByTempID))
	tempIDByNodeID := make(map[int64]string, len(inputByTempID))
	nodes := make([]*storage.DecompositionNode, 0, len(inputByTempID))
	for len(pending) > 0 {
		progress := false
		for _, tempID := range order {
			in, ok := pending[tempID]
			if !ok {
				continue
			}

			var parentNodeID *int64
			depth := 0
			if in.ParentTempID != "" {
				parentNode, hasParent := nodeByTempID[in.ParentTempID]
				if !hasParent {
					continue
				}
				parentNodeID = &parentNode.ID
				depth = parentNode.Depth + 1
			}

			verificationDetailsJSON := "{}"
			if in.VerificationDetails != nil {
				raw, marshalErr := json.Marshal(in.VerificationDetails)
				if marshalErr != nil {
					_ = tx.Rollback()
					return nil, fmt.Errorf("marshal verification_details for temp_id %q: %w", in.TempID, marshalErr)
				}
				verificationDetailsJSON = string(raw)
			}

			node, createNodeErr := s.DB.CreateDecompositionNodeTx(ctx, tx, &storage.DecompositionNode{
				DecompositionID:              decomp.ID,
				ParentNodeID:                 parentNodeID,
				Title:                        in.Title,
				Description:                  in.Description,
				VerificationType:             in.VerificationType,
				VerificationDetailsJSON:      verificationDetailsJSON,
				Depth:                        depth,
				RequiresFurtherDecomposition: in.RequiresFurtherDecomposition,
			})
			if createNodeErr != nil {
				_ = tx.Rollback()
				return nil, createNodeErr
			}
			nodeByTempID[tempID] = node
			tempIDByNodeID[node.ID] = tempID
			nodes = append(nodes, node)
			delete(pending, tempID)
			progress = true
		}
		if !progress {
			_ = tx.Rollback()
			return nil, errors.New("unable to resolve parent linkage (cycle detected)")
		}
	}

	issues := validateLeafNodes(nodes, tempIDByNodeID)
	status := "valid"
	if len(issues) > 0 {
		status = "draft"
	}
	issuesJSON, err := json.Marshal(issues)
	if err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("marshal structural issues: %w", err)
	}
	if err := s.DB.UpdateDecompositionStatusTx(ctx, tx, decomp.ID, status, string(issuesJSON), "[]"); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit decomposition tx: %w", err)
	}

	decompStored, nodesStored, err := s.GetDecomposition(ctx, decomp.ID)
	if err != nil {
		return nil, err
	}
	leafNodes := leafNodes(nodesStored)
	marketContext := make([]NodeMarketContext, 0, len(leafNodes))
	for _, leaf := range leafNodes {
		ctxItem, ctxErr := s.marketContextForLeaf(ctx, leaf)
		if ctxErr != nil {
			continue
		}
		ctxItem.TempID = tempIDByNodeID[leaf.ID]
		marketContext = append(marketContext, *ctxItem)
	}
	sort.Slice(marketContext, func(i, j int) bool {
		return marketContext[i].NodeID < marketContext[j].NodeID
	})

	return &CreateResult{
		Decomposition: decompStored,
		Nodes:         nodesStored,
		Issues:        issues,
		MarketContext: marketContext,
		Valid:         len(issues) == 0,
	}, nil
}

func (s *Service) GetDecomposition(ctx context.Context, id int64) (*storage.Decomposition, []*storage.DecompositionNode, error) {
	if s == nil || s.DB == nil {
		return nil, nil, errors.New("db is required")
	}
	decomp, err := s.DB.GetDecomposition(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	nodes, err := s.DB.ListDecompositionNodes(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return decomp, nodes, nil
}

func (s *Service) ListDecompositions(ctx context.Context, buyer, status string) ([]*storage.Decomposition, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db is required")
	}
	return s.DB.ListDecompositions(ctx, buyer, status)
}

func (s *Service) FinalizeDecomposition(ctx context.Context, p FinalizeParams) (*storage.Decomposition, []int64, error) {
	if s == nil || s.DB == nil {
		return nil, nil, errors.New("db is required")
	}
	if s.Bidding == nil {
		return nil, nil, errors.New("bidding service is required")
	}
	decomp, nodes, err := s.GetDecomposition(ctx, p.DecompositionID)
	if err != nil {
		return nil, nil, err
	}
	if decomp.Status != "valid" {
		return nil, nil, errors.New("decomposition must be valid before finalization")
	}
	if strings.TrimSpace(p.Buyer) == "" {
		return nil, nil, errors.New("buyer is required")
	}
	if !strings.EqualFold(decomp.Buyer, p.Buyer) {
		return nil, nil, errors.New("buyer does not match decomposition buyer")
	}

	leafNodes := leafNodes(nodes)
	if len(leafNodes) == 0 {
		return nil, nil, errors.New("decomposition has no leaf nodes")
	}
	sort.Slice(leafNodes, func(i, j int) bool {
		if leafNodes[i].Depth == leafNodes[j].Depth {
			return leafNodes[i].ID < leafNodes[j].ID
		}
		return leafNodes[i].Depth < leafNodes[j].Depth
	})

	// Validate all leaf params before opening the transaction to surface errors cheaply.
	leafParams := make([]bidding.CreateRFQParams, 0, len(leafNodes))
	for _, leaf := range leafNodes {
		params, paramsErr := buildRFQParamsForLeaf(leaf, p)
		if paramsErr != nil {
			return nil, nil, paramsErr
		}
		leafParams = append(leafParams, params)
	}

	// Create RFQs and update node/decomposition records atomically so a partial
	// failure cannot leave orphaned RFQs linked to no node, or nodes with no RFQ.
	tx, err := s.DB.BeginTx(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin finalize tx: %w", err)
	}

	rfqIDs := make([]int64, 0, len(leafNodes))
	for i, leaf := range leafNodes {
		rfq, createErr := s.Bidding.CreateRFQTx(ctx, tx, leafParams[i])
		if createErr != nil {
			_ = tx.Rollback()
			return nil, nil, createErr
		}
		if updateErr := s.DB.UpdateDecompositionNodeRFQTx(ctx, tx, leaf.ID, rfq.ID); updateErr != nil {
			_ = tx.Rollback()
			return nil, nil, updateErr
		}
		rfqIDs = append(rfqIDs, rfq.ID)
	}

	rfqIDsJSON, err := json.Marshal(rfqIDs)
	if err != nil {
		_ = tx.Rollback()
		return nil, nil, fmt.Errorf("marshal rfq ids: %w", err)
	}
	if err := s.DB.UpdateDecompositionStatusTx(ctx, tx, decomp.ID, "finalized", decomp.ValidationErrorsJSON, string(rfqIDsJSON)); err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit finalize tx: %w", err)
	}
	updated, err := s.DB.GetDecomposition(ctx, decomp.ID)
	if err != nil {
		return nil, nil, err
	}
	return updated, rfqIDs, nil
}

func (s *Service) marketContextForLeaf(ctx context.Context, node *storage.DecompositionNode) (*NodeMarketContext, error) {
	verificationType := strings.TrimSpace(node.VerificationType)
	marketDepth, err := s.marketDepthByVerificationType(ctx, verificationType)
	if err != nil {
		return nil, err
	}
	verifierCount, err := s.verifierPoolCount(ctx)
	if err != nil {
		return nil, err
	}
	signal := "untested"
	switch {
	case marketDepth > 5:
		signal = "proven"
	case marketDepth >= 1:
		signal = "emerging"
	}
	evidence := marketEvidence(verificationType, marketDepth, verifierCount)
	return &NodeMarketContext{
		NodeID:           node.ID,
		VerificationType: verificationType,
		MarketDepth:      marketDepth,
		VerifierCount:    verifierCount,
		Signal:           signal,
		Evidence:         evidence,
	}, nil
}

func validateLeafNodes(nodes []*storage.DecompositionNode, tempIDByNodeID map[int64]string) []StructuralIssue {
	leafNodes := leafNodes(nodes)
	issues := make([]StructuralIssue, 0)
	for _, node := range leafNodes {
		if node.VerificationType == "" {
			issues = append(issues, StructuralIssue{
				NodeID: node.ID,
				TempID: tempIDByNodeID[node.ID],
				Title:  node.Title,
				Reason: "verification_type required for leaf nodes",
			})
		}
		if node.RequiresFurtherDecomposition {
			issues = append(issues, StructuralIssue{
				NodeID: node.ID,
				TempID: tempIDByNodeID[node.ID],
				Title:  node.Title,
				Reason: "node flagged as requiring further decomposition",
			})
		}
		if node.VerificationType == "zk_proof" {
			details, err := parseVerificationDetails(node.VerificationDetailsJSON)
			if err != nil || strings.TrimSpace(details["circuit_id"]) == "" {
				issues = append(issues, StructuralIssue{
					NodeID: node.ID,
					TempID: tempIDByNodeID[node.ID],
					Title:  node.Title,
					Reason: "zk_proof requires circuit_id in verification_details",
				})
			}
		}
	}
	return issues
}

func leafNodes(nodes []*storage.DecompositionNode) []*storage.DecompositionNode {
	childrenByParentID := make(map[int64]int, len(nodes))
	for _, node := range nodes {
		if node.ParentNodeID != nil {
			childrenByParentID[*node.ParentNodeID]++
		}
	}
	leafNodes := make([]*storage.DecompositionNode, 0, len(nodes))
	for _, node := range nodes {
		if childrenByParentID[node.ID] == 0 {
			leafNodes = append(leafNodes, node)
		}
	}
	return leafNodes
}

func parseVerificationDetails(raw string) (map[string]string, error) {
	details := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return map[string]string{}, nil
	}
	if err := json.Unmarshal([]byte(raw), &details); err != nil {
		return nil, fmt.Errorf("invalid verification_details_json: %w", err)
	}
	out := make(map[string]string, len(details))
	for k, v := range details {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out, nil
}

func (s *Service) marketDepthByVerificationType(ctx context.Context, verificationType string) (int, error) {
	var query string
	switch verificationType {
	case "zk_proof":
		query = `SELECT COUNT(*) FROM escrows WHERE lower(status) = 'settled' AND lower(zk_verifier) != lower(?)`
		return queryCount(ctx, s.DB.SQLDB(), query, zeroAddress)
	case "quorum":
		query = `SELECT COUNT(*) FROM escrows WHERE lower(status) = 'settled' AND verifier_panel_json != '[]'`
		return queryCount(ctx, s.DB.SQLDB(), query)
	case "optimistic", "unit_test":
		query = `SELECT COUNT(*) FROM escrows WHERE lower(status) = 'settled' AND service_tier = 0`
		return queryCount(ctx, s.DB.SQLDB(), query)
	default:
		return 0, nil
	}
}

func (s *Service) verifierPoolCount(ctx context.Context) (int, error) {
	return queryCount(
		ctx,
		s.DB.SQLDB(),
		`SELECT COUNT(DISTINCT lower(address)) FROM reputation WHERE role = 'worker' AND completed > 0`,
	)
}

func queryCount(ctx context.Context, db *sql.DB, query string, args ...any) (int, error) {
	var count int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count query failed: %w", err)
	}
	return count, nil
}

func marketEvidence(verificationType string, marketDepth, verifierCount int) string {
	switch verificationType {
	case "zk_proof":
		if marketDepth == 0 {
			return "no settled escrows with ZK verification found - you may be the first"
		}
		return fmt.Sprintf("%d settled escrows with ZK verification found", marketDepth)
	case "quorum":
		if marketDepth == 0 && verifierCount == 0 {
			return "no settled quorum escrows found and no verifier history available"
		}
		if marketDepth == 0 {
			return "no settled escrows with quorum verification found"
		}
		if verifierCount == 0 {
			return fmt.Sprintf("%d settled quorum escrows found, but no verifier history available", marketDepth)
		}
		return fmt.Sprintf("%d settled quorum escrows found with %d active verifiers", marketDepth, verifierCount)
	case "unit_test":
		if marketDepth == 0 {
			return "no settled unit_test escrows observed yet"
		}
		return fmt.Sprintf("%d settled tier-0 escrows provide analogous unit_test market signal", marketDepth)
	case "optimistic":
		if marketDepth == 0 {
			return "no settled optimistic escrows observed yet"
		}
		return fmt.Sprintf("%d settled optimistic escrows found", marketDepth)
	default:
		return "market context unavailable for this verification type"
	}
}

func buildRFQParamsForLeaf(node *storage.DecompositionNode, p FinalizeParams) (bidding.CreateRFQParams, error) {
	verificationType := strings.TrimSpace(node.VerificationType)
	if verificationType == "" {
		return bidding.CreateRFQParams{}, fmt.Errorf("leaf node %d missing verification_type", node.ID)
	}

	verifier := ""
	if verificationType == "quorum" || verificationType == "zk_proof" {
		if len(p.VerifierPanel) == 0 {
			return bidding.CreateRFQParams{}, fmt.Errorf("verification_type %s requires verifier_panel", verificationType)
		}
		verifier = p.VerifierPanel[0]
	}

	var serviceTier int
	var biddingMode string
	requirements := map[string]any{
		"verification_type": verificationType,
	}
	switch verificationType {
	case "optimistic":
		serviceTier = 0
		biddingMode = "open"
	case "unit_test":
		serviceTier = 0
		biddingMode = "open"
		requirements["required_artifacts"] = []string{"unit_test_log"}
	case "quorum":
		serviceTier = 1
		biddingMode = "sealed"
		if p.QuorumCount <= 0 {
			return bidding.CreateRFQParams{}, errors.New("quorum_count must be > 0 for quorum verification_type")
		}
		requirements["verifier_panel"] = p.VerifierPanel
		requirements["quorum_count"] = p.QuorumCount
	case "zk_proof":
		serviceTier = 1
		biddingMode = "sealed"
		details, err := parseVerificationDetails(node.VerificationDetailsJSON)
		if err != nil {
			return bidding.CreateRFQParams{}, err
		}
		circuitID := strings.TrimSpace(details["circuit_id"])
		if circuitID == "" {
			return bidding.CreateRFQParams{}, fmt.Errorf("leaf node %d zk_proof requires circuit_id", node.ID)
		}
		if zkVerifier := strings.TrimSpace(details["zk_verifier"]); zkVerifier != "" {
			requirements["zk_verifier"] = zkVerifier
		}
		requirements["circuit_id"] = circuitID
	default:
		return bidding.CreateRFQParams{}, fmt.Errorf("unsupported verification_type %q", verificationType)
	}

	requirementsJSON, err := json.Marshal(requirements)
	if err != nil {
		return bidding.CreateRFQParams{}, fmt.Errorf("marshal requirements_json: %w", err)
	}
	// Decomposition-derived RFQs require no worker stake: the anti-Sybil bond
	// applies to individual negotiated contracts, not to planning-phase RFQs.
	return bidding.CreateRFQParams{
		Title:                    node.Title,
		Description:              node.Description,
		Buyer:                    p.Buyer,
		Token:                    normalizeToken(p.Token),
		BudgetMin:                p.BudgetMin,
		BudgetMax:                p.BudgetMax,
		Deadline:                 p.Deadline,
		ReviewPeriodSeconds:      p.ReviewPeriodSeconds,
		DisputePeriodSeconds:     p.DisputePeriodSeconds,
		ArbitratorTimeoutSeconds: p.ArbitratorTimeoutSeconds,
		Verifier:                 verifier,
		Arbitrator:               p.Arbitrator,
		WorkerStake:              "0",
		MilestonesJSON:           "[]",
		RequirementsJSON:         string(requirementsJSON),
		RequiredCredentialsJSON:  "[]",
		ServiceTier:              serviceTier,
		BiddingMode:              biddingMode,
		CommitDeadline:           p.CommitDeadline,
		RevealDeadline:           p.RevealDeadline,
		ExpiresAt:                p.ExpiresAt,
	}, nil
}

func normalizeToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" || strings.EqualFold(token, zeroAddress) {
		return ""
	}
	return token
}
