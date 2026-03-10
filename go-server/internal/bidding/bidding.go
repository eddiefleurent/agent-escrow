package bidding

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/numconv"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// milestoneJSON is the JSON representation stored in rfqs.milestones_json and bids.milestones_json.
type milestoneJSON struct {
	Amount             string `json:"amount"`
	SubmissionDeadline string `json:"submission_deadline"`
}

const (
	maxCommitRequestsPerMinute   = 10
	commitRateLimitWindowSeconds = 60
	sealedBidSelectionRule       = "lowest_amount_then_duration_then_commit_time_then_bid_id"
)

type RebidCooldownError struct {
	ParentEscrowID int64
	RetryAt        time.Time
	RetryAfter     time.Duration
}

type SealedBidCooldownError struct {
	Bidder      string
	RetryAt     time.Time
	RetryAfter  time.Duration
	StrikeCount int
}

func (e *SealedBidCooldownError) RetryAfterSeconds() int64 {
	seconds := int64(e.RetryAfter / time.Second)
	if e.RetryAfter%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return seconds
}

func (e *SealedBidCooldownError) Error() string {
	return fmt.Sprintf(
		"sealed-bid cooldown active for %s: retry in %d seconds (at %s, strikes=%d)",
		e.Bidder,
		e.RetryAfterSeconds(),
		e.RetryAt.UTC().Format(time.RFC3339),
		e.StrikeCount,
	)
}

func (e *RebidCooldownError) RetryAfterSeconds() int64 {
	seconds := int64(e.RetryAfter / time.Second)
	if e.RetryAfter%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return seconds
}

func (e *RebidCooldownError) Error() string {
	return fmt.Sprintf(
		"re-bid cooldown active for parent_escrow_id %d: retry in %d seconds (at %s)",
		e.ParentEscrowID,
		e.RetryAfterSeconds(),
		e.RetryAt.UTC().Format(time.RFC3339),
	)
}

func sealedBidCooldownDuration(strikeCount int) time.Duration {
	switch {
	case strikeCount <= 1:
		return 15 * time.Minute
	case strikeCount == 2:
		return 6 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func sealedBidPhase(now time.Time, rfq *storage.RFQ) string {
	nowUnix := now.Unix()
	switch {
	case rfq.BiddingMode != "sealed":
		return ""
	case nowUnix < rfq.CommitDeadline:
		return "commit_open"
	case nowUnix <= rfq.RevealDeadline:
		return "reveal_open"
	default:
		return rfq.SealedBidStatus
	}
}

// parseMilestonesJSON parses the milestones_json string from an RFQ or bid into chain params.
func parseMilestonesJSON(raw string) ([]chain.MilestoneParam, error) {
	if raw == "" || raw == "[]" {
		return nil, nil
	}
	var items []milestoneJSON
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, fmt.Errorf("parse milestones_json: %w", err)
	}
	params := make([]chain.MilestoneParam, 0, len(items))
	for i, m := range items {
		amt, ok := new(big.Int).SetString(m.Amount, 10)
		if !ok || amt.Sign() <= 0 {
			return nil, fmt.Errorf("invalid milestone[%d] amount: %q", i, m.Amount)
		}
		deadline, err := strconv.ParseUint(m.SubmissionDeadline, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid milestone[%d] submission_deadline: %w", i, err)
		}
		params = append(params, chain.MilestoneParam{
			Amount:             amt,
			SubmissionDeadline: deadline,
		})
	}
	return params, nil
}

// Service encapsulates the bidding protocol business logic shared by MCP and HTTP handlers.
type Service struct {
	DB    *storage.DB
	Chain chain.ChainClient
	Idx   *indexer.Indexer
	Cfg   *config.Config
}

func (s *Service) NormalizeRFQ(rfq *storage.RFQ, now time.Time) *storage.RFQ {
	if rfq == nil {
		return nil
	}
	normalized := *rfq
	if normalized.BiddingMode == "sealed" {
		normalized.SealedBidStatus = sealedBidPhase(now.UTC(), &normalized)
	}
	return &normalized
}

func (s *Service) NormalizeRFQs(rfqs []*storage.RFQ, now time.Time) []*storage.RFQ {
	normalized := make([]*storage.RFQ, 0, len(rfqs))
	for _, rfq := range rfqs {
		normalized = append(normalized, s.NormalizeRFQ(rfq, now))
	}
	return normalized
}

type SealedBidSummary struct {
	RFQID                  int64   `json:"rfq_id"`
	Finalized              bool    `json:"finalized"`
	SealedBidStatus        string  `json:"sealed_bid_status"`
	SealedBidSelectionRule string  `json:"sealed_bid_selection_rule"`
	BestBidID              *int64  `json:"best_bid_id,omitempty"`
	EligibleBidIDs         []int64 `json:"eligible_bid_ids"`
	UnrevealedCommitCount  int     `json:"unrevealed_commit_count"`
	FinalizedAt            int64   `json:"sealed_bid_finalized_at"`
}

type sealedBidCandidate struct {
	bid        *storage.Bid
	commit     *storage.BidCommit
	amount     *big.Int
	commitTime time.Time
}

func containsInt64(items []int64, target int64) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func sortSealedBidCandidates(candidates []sealedBidCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if cmp := candidates[i].amount.Cmp(candidates[j].amount); cmp != 0 {
			return cmp < 0
		}
		if candidates[i].bid.EstimatedDuration != candidates[j].bid.EstimatedDuration {
			return candidates[i].bid.EstimatedDuration < candidates[j].bid.EstimatedDuration
		}
		if !candidates[i].commitTime.Equal(candidates[j].commitTime) {
			return candidates[i].commitTime.Before(candidates[j].commitTime)
		}
		return candidates[i].bid.ID < candidates[j].bid.ID
	})
}

func (s *Service) activeSealedBidCooldown(
	ctx context.Context,
	bidder string,
	now time.Time,
) (*SealedBidCooldownError, error) {
	discipline, err := s.DB.GetSealedBidderDiscipline(ctx, bidder)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if discipline.CooldownUntil <= now.Unix() {
		return nil, nil
	}
	retryAt := time.Unix(discipline.CooldownUntil, 0).UTC()
	return &SealedBidCooldownError{
		Bidder:      bidder,
		RetryAt:     retryAt,
		RetryAfter:  time.Until(retryAt),
		StrikeCount: discipline.NonRevealCount,
	}, nil
}

func buildSealedBidSummary(
	rfq *storage.RFQ,
	eligibleBidIDs []int64,
	unrevealedCommitCount int,
) *SealedBidSummary {
	return &SealedBidSummary{
		RFQID:                  rfq.ID,
		Finalized:              rfq.SealedBidStatus == "finalized" || rfq.SealedBidStatus == "no_valid_reveals",
		SealedBidStatus:        rfq.SealedBidStatus,
		SealedBidSelectionRule: rfq.SealedBidSelectionRule,
		BestBidID:              rfq.BestBidID,
		EligibleBidIDs:         eligibleBidIDs,
		UnrevealedCommitCount:  unrevealedCommitCount,
		FinalizedAt:            rfq.SealedBidFinalizedAt,
	}
}

func (s *Service) eligibleSealedBidCandidates(
	ctx context.Context,
	rfq *storage.RFQ,
	now time.Time,
) ([]sealedBidCandidate, error) {
	bids, err := s.DB.ListBidsByRFQ(ctx, rfq.ID)
	if err != nil {
		return nil, fmt.Errorf("list bids by rfq: %w", err)
	}

	requirements, err := ParseCredentialRequirements(rfq.RequiredCredentialsJSON)
	if err != nil {
		return nil, fmt.Errorf("parse rfq credential requirements: %w", err)
	}
	requireVerifiedCredentials := len(requirements) > 0

	candidates := make([]sealedBidCandidate, 0, len(bids))
	for _, bid := range bids {
		if bid.Status != "pending" {
			continue
		}
		if bid.ExpiresAt <= now.Unix() {
			continue
		}
		if requireVerifiedCredentials && !bid.CredentialVerified {
			continue
		}

		commit, err := s.DB.GetBidCommitByRevealedBidID(ctx, bid.ID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, fmt.Errorf("get bid commit by revealed bid id: %w", err)
		}
		if commit.Status != "revealed" {
			continue
		}

		amount, ok := new(big.Int).SetString(bid.Amount, 10)
		if !ok || amount.Sign() <= 0 {
			continue
		}
		candidates = append(candidates, sealedBidCandidate{
			bid:        bid,
			commit:     commit,
			amount:     amount,
			commitTime: commit.CreatedAt,
		})
	}

	sortSealedBidCandidates(candidates)
	return candidates, nil
}

func (s *Service) FinalizeSealedBidding(ctx context.Context, rfqID int64) (*SealedBidSummary, error) {
	rfq, err := s.DB.GetRFQ(ctx, rfqID)
	if err != nil {
		return nil, fmt.Errorf("rfq not found: %w", err)
	}
	if rfq.BiddingMode != "sealed" {
		return buildSealedBidSummary(rfq, nil, 0), nil
	}
	if rfq.SealedBidStatus == "finalized" || rfq.SealedBidStatus == "no_valid_reveals" {
		return buildSealedBidSummary(rfq, nil, 0), nil
	}

	now := time.Now().UTC()
	if now.Unix() <= rfq.RevealDeadline {
		return nil, errors.New("cannot finalize sealed bidding before reveal phase ends")
	}

	candidates, err := s.eligibleSealedBidCandidates(ctx, rfq, now)
	if err != nil {
		return nil, err
	}

	eligibleBidIDs := make([]int64, 0, len(candidates))
	for _, candidate := range candidates {
		eligibleBidIDs = append(eligibleBidIDs, candidate.bid.ID)
	}

	finalizedStatus := "no_valid_reveals"
	var bestBidID *int64
	if len(candidates) > 0 {
		finalizedStatus = "finalized"
		bestBidID = &candidates[0].bid.ID
	}

	dbTx, err := s.DB.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin db tx: %w", err)
	}
	defer dbTx.Rollback()

	currentRFQ, err := s.DB.GetRFQTx(ctx, dbTx, rfqID)
	if err != nil {
		return nil, fmt.Errorf("get rfq in tx: %w", err)
	}
	if currentRFQ.SealedBidStatus == "finalized" || currentRFQ.SealedBidStatus == "no_valid_reveals" {
		return buildSealedBidSummary(currentRFQ, nil, 0), nil
	}

	committed, err := s.DB.ListCommittedBidCommitsByRFQTx(ctx, dbTx, rfqID)
	if err != nil {
		return nil, err
	}

	bidderCooldowns := make(map[string]int64)
	for _, commit := range committed {
		discipline, disciplineErr := s.DB.GetSealedBidderDisciplineTx(ctx, dbTx, commit.Bidder)
		strikeCount := 1
		if disciplineErr == nil {
			strikeCount = discipline.NonRevealCount + 1
		} else if !errors.Is(disciplineErr, sql.ErrNoRows) {
			return nil, disciplineErr
		}
		cooldownUntil := now.Add(sealedBidCooldownDuration(strikeCount)).Unix()
		if existing, ok := bidderCooldowns[commit.Bidder]; !ok || cooldownUntil > existing {
			bidderCooldowns[commit.Bidder] = cooldownUntil
		}
	}
	for bidder, cooldownUntil := range bidderCooldowns {
		if upsertErr := s.DB.UpsertSealedBidderDisciplineTx(ctx, dbTx, bidder, cooldownUntil); upsertErr != nil {
			return nil, upsertErr
		}
	}
	if err := s.DB.ExpireCommittedBidCommitsTx(ctx, dbTx, rfqID); err != nil {
		return nil, err
	}
	if err := s.DB.UpdateRFQSealedBiddingStateTx(
		ctx,
		dbTx,
		rfqID,
		finalizedStatus,
		sealedBidSelectionRule,
		bestBidID,
		now.Unix(),
	); err != nil {
		return nil, err
	}
	if err := dbTx.Commit(); err != nil {
		return nil, fmt.Errorf("commit db tx: %w", err)
	}

	currentRFQ.SealedBidStatus = finalizedStatus
	currentRFQ.SealedBidSelectionRule = sealedBidSelectionRule
	currentRFQ.BestBidID = bestBidID
	currentRFQ.SealedBidFinalizedAt = now.Unix()

	return buildSealedBidSummary(currentRFQ, eligibleBidIDs, len(committed)), nil
}

type CreateRFQParams struct {
	Title                    string
	Description              string
	Buyer                    string
	Token                    string
	BudgetMin                string
	BudgetMax                string
	Deadline                 int64
	ReviewPeriodSeconds      int64
	DisputePeriodSeconds     int64
	ArbitratorTimeoutSeconds int64
	Verifier                 string
	Arbitrator               string
	WorkerStake              string
	MilestonesJSON           string
	RequirementsJSON         string
	RequiredCredentialsJSON  string
	RequiredProofProtocol    string
	ServiceTier              int // 0 = low_assurance (default), 1 = high_assurance (paper §5.3)
	BiddingMode              string
	CommitDeadline           int64
	RevealDeadline           int64
	ExpiresAt                int64
	ParentEscrowID           *int64 // Optional: links RFQ to a parent escrow for sub-delegation (paper §4.8)
}

type CommitBidParams struct {
	RFQID      int64
	Bidder     string
	Commitment string
	Nonce      string
}

type RevealBidParams struct {
	RFQID             int64
	Bidder            string
	Nonce             string
	Salt              string
	Amount            string
	EstimatedDuration int64
	ReputationBond    string
	MilestonesJSON    string
	Message           string
	ExpiresAt         int64
	StakeMandateID    string // Optional AP2 mandate ID for Sybil-resistant stake-on-bid (paper §6)
	CredentialsJSON   string // JSON array of attestation-v1 payloads (paper §4.6 Table 3)
}

type AcceptBidParams struct {
	RFQID  int64
	BidID  int64
	Caller string // must match RFQ buyer
}

type AcceptBidResult struct {
	Bid    *storage.Bid
	Escrow *storage.Escrow
	Task   *storage.Task
	TxHash string
}

// prepareRFQRecord validates p, normalises defaults, enforces parent_escrow_id
// authorization (paper §4.8), and returns a ready-to-insert RFQ record.
// It does not write to the database; the caller is responsible for the write.
func (s *Service) prepareRFQRecord(ctx context.Context, p CreateRFQParams) (*storage.RFQ, error) {
	budgetMin, ok := new(big.Int).SetString(p.BudgetMin, 10)
	if !ok || budgetMin.Sign() < 0 {
		return nil, errors.New("invalid budget_min")
	}
	budgetMax, ok := new(big.Int).SetString(p.BudgetMax, 10)
	if !ok || budgetMax.Sign() <= 0 {
		return nil, errors.New("invalid budget_max")
	}
	if budgetMin.Cmp(budgetMax) > 0 {
		return nil, errors.New("budget_min must be <= budget_max")
	}

	now := time.Now().Unix()
	if p.ExpiresAt <= now {
		return nil, errors.New("expires_at must be in the future")
	}
	if p.Deadline <= now {
		return nil, errors.New("deadline must be in the future")
	}
	if p.CommitDeadline == 0 {
		return nil, errors.New("commit_deadline is required")
	}
	if p.RevealDeadline == 0 {
		return nil, errors.New("reveal_deadline is required")
	}
	if p.CommitDeadline <= now {
		return nil, errors.New("commit_deadline must be in the future")
	}
	if p.RevealDeadline < p.CommitDeadline {
		return nil, errors.New("reveal_deadline must be >= commit_deadline")
	}
	if p.ExpiresAt < p.RevealDeadline {
		return nil, errors.New("expires_at must be >= reveal_deadline")
	}
	if p.RevealDeadline > p.Deadline {
		return nil, errors.New("reveal_deadline must be <= deadline")
	}

	if !common.IsHexAddress(p.Buyer) {
		return nil, errors.New("invalid buyer address")
	}

	if p.Token != "" && p.Token != "0x0000000000000000000000000000000000000000" {
		if !common.IsHexAddress(p.Token) {
			return nil, errors.New("invalid token address")
		}
	}
	biddingMode := strings.TrimSpace(p.BiddingMode)
	if biddingMode == "" {
		biddingMode = "sealed"
	}
	if biddingMode != "sealed" && biddingMode != "open" {
		return nil, errors.New("invalid bidding_mode: must be 'sealed' or 'open'")
	}

	if p.Verifier != "" && !common.IsHexAddress(p.Verifier) {
		return nil, errors.New("invalid verifier address")
	}
	if p.Arbitrator != "" && !common.IsHexAddress(p.Arbitrator) {
		return nil, errors.New("invalid arbitrator address")
	}

	if p.ServiceTier != 0 && p.ServiceTier != 1 {
		return nil, errors.New("invalid service_tier: must be 0 (low_assurance) or 1 (high_assurance)")
	}

	if p.WorkerStake == "" {
		p.WorkerStake = "0"
	}
	ws, ok := new(big.Int).SetString(p.WorkerStake, 10)
	if !ok || ws.Sign() < 0 {
		return nil, errors.New("invalid worker_stake")
	}

	if p.MilestonesJSON == "" {
		p.MilestonesJSON = "[]"
	}
	if p.RequirementsJSON == "" {
		p.RequirementsJSON = "{}"
	}

	var requirements map[string]any
	if err := json.Unmarshal([]byte(p.RequirementsJSON), &requirements); err != nil {
		return nil, fmt.Errorf("invalid requirements_json: %w", err)
	}
	if requirements == nil {
		requirements = map[string]any{}
	}

	var jsonProofProtocol string
	hasJSONProofProtocol := false
	if raw, ok := requirements["required_proof_protocol"]; ok {
		protocol, ok := raw.(string)
		if !ok {
			return nil, errors.New("requirements_json.required_proof_protocol must be a string")
		}
		if protocol != "groth16" {
			return nil, errors.New("required_proof_protocol must be 'groth16' when set")
		}
		jsonProofProtocol = protocol
		hasJSONProofProtocol = true
	}

	if p.RequiredProofProtocol != "" {
		if p.RequiredProofProtocol != "groth16" {
			return nil, errors.New("required_proof_protocol must be 'groth16' when set")
		}
		if hasJSONProofProtocol && jsonProofProtocol != p.RequiredProofProtocol {
			return nil, errors.New("required_proof_protocol mismatch between field and requirements_json")
		}
		requirements["required_proof_protocol"] = p.RequiredProofProtocol
	} else if hasJSONProofProtocol {
		p.RequiredProofProtocol = jsonProofProtocol
	}

	normalizedReq, err := json.Marshal(requirements)
	if err != nil {
		return nil, fmt.Errorf("marshal requirements_json: %w", err)
	}
	p.RequirementsJSON = string(normalizedReq)
	if p.RequiredCredentialsJSON == "" {
		p.RequiredCredentialsJSON = "[]"
	}
	if _, err := ParseCredentialRequirements(p.RequiredCredentialsJSON); err != nil {
		return nil, fmt.Errorf("invalid required_credentials_json: %w", err)
	}

	// Validate parent_escrow_id when sub-delegating: the buyer of the child RFQ
	// must be the active worker of the parent escrow (paper §4.8).
	if p.ParentEscrowID != nil {
		parentEscrow, err := s.DB.GetEscrow(ctx, *p.ParentEscrowID)
		if err != nil {
			return nil, fmt.Errorf("parent_escrow_id %d not found: %w", *p.ParentEscrowID, err)
		}
		activeWorker := parentEscrow.ActiveWorker
		if activeWorker == "" {
			activeWorker = parentEscrow.Worker
		}
		if !strings.EqualFold(common.HexToAddress(p.Buyer).Hex(), common.HexToAddress(activeWorker).Hex()) {
			return nil, fmt.Errorf("sub-delegation RFQ buyer (%s) must be the active worker of parent escrow %d (%s)",
				p.Buyer, *p.ParentEscrowID, activeWorker)
		}
	}

	specHash := crypto.Keccak256Hash([]byte(p.Title + p.Description))
	sealedStatus := ""
	sealedSelectionRule := ""
	if biddingMode == "sealed" {
		sealedStatus = sealedBidPhase(time.Now(), &storage.RFQ{
			BiddingMode:    biddingMode,
			CommitDeadline: p.CommitDeadline,
			RevealDeadline: p.RevealDeadline,
		})
		sealedSelectionRule = sealedBidSelectionRule
	}

	return &storage.RFQ{
		Title:                    p.Title,
		Description:              p.Description,
		SpecHash:                 specHash.Hex(),
		Buyer:                    p.Buyer,
		Token:                    p.Token,
		BudgetMin:                p.BudgetMin,
		BudgetMax:                p.BudgetMax,
		Deadline:                 p.Deadline,
		ReviewPeriodSeconds:      p.ReviewPeriodSeconds,
		DisputePeriodSeconds:     p.DisputePeriodSeconds,
		ArbitratorTimeoutSeconds: p.ArbitratorTimeoutSeconds,
		Verifier:                 p.Verifier,
		Arbitrator:               p.Arbitrator,
		WorkerStake:              p.WorkerStake,
		MilestonesJSON:           p.MilestonesJSON,
		RequirementsJSON:         p.RequirementsJSON,
		RequiredCredentialsJSON:  p.RequiredCredentialsJSON,
		BiddingMode:              biddingMode,
		CommitDeadline:           p.CommitDeadline,
		RevealDeadline:           p.RevealDeadline,
		SealedBidStatus:          sealedStatus,
		SealedBidSelectionRule:   sealedSelectionRule,
		ServiceTier:              p.ServiceTier,
		ParentEscrowID:           p.ParentEscrowID,
		Status:                   "open",
		ExpiresAt:                p.ExpiresAt,
	}, nil
}

func (s *Service) CreateRFQ(ctx context.Context, p CreateRFQParams) (*storage.RFQ, error) {
	rfqRecord, err := s.prepareRFQRecord(ctx, p)
	if err != nil {
		return nil, err
	}

	cooldownSeconds := int64(0)
	if s.Cfg != nil {
		cooldownSeconds = s.Cfg.RebidCooldownSeconds
	}

	if rfqRecord.ParentEscrowID != nil && cooldownSeconds > 0 {
		rfq, err := s.DB.CreateRFQWithParentCooldown(ctx, rfqRecord, cooldownSeconds)
		if err != nil {
			var cooldownErr *storage.ParentRFQCooldownError
			if errors.As(err, &cooldownErr) {
				retryAt := cooldownErr.LatestCreatedAt.Add(time.Duration(cooldownErr.CooldownSeconds) * time.Second)
				nowUTC := time.Now().UTC()
				retryAfter := retryAt.Sub(nowUTC)
				if retryAfter < 0 {
					retryAfter = 0
				}
				return nil, &RebidCooldownError{
					ParentEscrowID: cooldownErr.ParentEscrowID,
					RetryAt:        retryAt,
					RetryAfter:     retryAfter,
				}
			}
			return nil, fmt.Errorf("create rfq: %w", err)
		}
		return rfq, nil
	}
	rfq, err := s.DB.CreateRFQ(ctx, rfqRecord)
	if err != nil {
		return nil, fmt.Errorf("create rfq: %w", err)
	}
	return rfq, nil
}

// CreateRFQTx validates p and inserts the RFQ within the provided transaction.
// The rebid cooldown window is not enforced; use CreateRFQ for the standard
// sub-delegation path where cooldown applies.
func (s *Service) CreateRFQTx(ctx context.Context, tx *sql.Tx, p CreateRFQParams) (*storage.RFQ, error) {
	rfqRecord, err := s.prepareRFQRecord(ctx, p)
	if err != nil {
		return nil, err
	}
	rfq, err := s.DB.CreateRFQTx(ctx, tx, rfqRecord)
	if err != nil {
		return nil, fmt.Errorf("create rfq: %w", err)
	}
	return rfq, nil
}

func canonicalUintString(raw, fieldName string, allowZero bool) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("%s is required", fieldName)
	}
	n, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return "", fmt.Errorf("invalid %s", fieldName)
	}
	if allowZero {
		if n.Sign() < 0 {
			return "", fmt.Errorf("invalid %s: negative value", fieldName)
		}
	} else if n.Sign() <= 0 {
		return "", fmt.Errorf("invalid %s", fieldName)
	}
	return n.String(), nil
}

func canonicalMilestonesJSON(raw string) (string, error) {
	if raw == "" {
		return "[]", nil
	}
	var items []milestoneJSON
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return "", fmt.Errorf("invalid milestones_json: %w", err)
	}
	normalized, err := json.Marshal(items)
	if err != nil {
		return "", fmt.Errorf("marshal milestones_json: %w", err)
	}
	return string(normalized), nil
}

func computeBidCommitment(rfqID int64, p RevealBidParams) (string, error) {
	if rfqID <= 0 {
		return "", errors.New("invalid rfq_id")
	}
	if !common.IsHexAddress(p.Bidder) {
		return "", errors.New("invalid bidder address")
	}
	if strings.TrimSpace(p.Nonce) == "" {
		return "", errors.New("nonce is required")
	}
	if strings.TrimSpace(p.Salt) == "" {
		return "", errors.New("salt is required")
	}
	if p.EstimatedDuration < 0 {
		return "", errors.New("estimated_duration must be >= 0")
	}
	if p.ExpiresAt <= 0 {
		return "", errors.New("expires_at must be > 0")
	}

	amount, err := canonicalUintString(p.Amount, "amount", false)
	if err != nil {
		return "", err
	}
	reputationBond, err := canonicalUintString(p.ReputationBond, "reputation_bond", true)
	if err != nil {
		return "", err
	}
	milestonesJSON, err := canonicalMilestonesJSON(p.MilestonesJSON)
	if err != nil {
		return "", err
	}
	milestonesHash := crypto.Keccak256Hash([]byte(milestonesJSON)).Hex()
	messageHash := crypto.Keccak256Hash([]byte(p.Message)).Hex()
	stakeMandateHash := crypto.Keccak256Hash([]byte(strings.TrimSpace(p.StakeMandateID))).Hex()
	payload := strings.Join([]string{
		"agent-escrow:sealed-bid:v1",
		strconv.FormatInt(rfqID, 10),
		strings.ToLower(common.HexToAddress(p.Bidder).Hex()),
		amount,
		strconv.FormatInt(p.EstimatedDuration, 10),
		reputationBond,
		milestonesHash,
		messageHash,
		strconv.FormatInt(p.ExpiresAt, 10),
		stakeMandateHash,
		p.Nonce,
		p.Salt,
	}, "|")
	return crypto.Keccak256Hash([]byte(payload)).Hex(), nil
}

func (s *Service) CommitBid(ctx context.Context, p CommitBidParams) (*storage.BidCommit, error) {
	rfq, err := s.DB.GetRFQ(ctx, p.RFQID)
	if err != nil {
		return nil, fmt.Errorf("rfq not found: %w", err)
	}
	if rfq.Status != "open" {
		return nil, fmt.Errorf("rfq is not open (status: %s)", rfq.Status)
	}

	now := time.Now().Unix()
	if rfq.CommitDeadline <= now {
		return nil, errors.New("commit phase has ended")
	}

	if !common.IsHexAddress(p.Bidder) {
		return nil, errors.New("invalid bidder address")
	}
	p.Bidder = common.HexToAddress(p.Bidder).Hex()
	if p.Bidder == common.HexToAddress(rfq.Buyer).Hex() {
		return nil, errors.New("bidder cannot be the same as the rfq buyer")
	}
	if rfq.BiddingMode == "sealed" {
		cooldownErr, cooldownLookupErr := s.activeSealedBidCooldown(ctx, p.Bidder, time.Unix(now, 0).UTC())
		if cooldownLookupErr != nil {
			return nil, cooldownLookupErr
		}
		if cooldownErr != nil {
			return nil, errors.Join(storage.ErrSealedBidCooldownActive, cooldownErr)
		}
	}
	if len(p.Nonce) == 0 {
		return nil, errors.New("nonce is required")
	}
	if !strings.HasPrefix(p.Commitment, "0x") || len(p.Commitment) != 66 {
		return nil, errors.New("invalid commitment: must be 0x-prefixed 32-byte hex")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(p.Commitment, "0x")); err != nil {
		return nil, errors.New("invalid commitment: must be valid hex")
	}
	p.Commitment = strings.ToLower(p.Commitment)

	recentCommitCount, err := s.DB.CountRecentBidCommitsByRFQBidder(
		ctx, p.RFQID, p.Bidder, commitRateLimitWindowSeconds, time.Now(),
	)
	if err != nil {
		return nil, err
	}
	if recentCommitCount >= maxCommitRequestsPerMinute {
		return nil, fmt.Errorf("rate limit exceeded: max %d commits per bidder per %d seconds", maxCommitRequestsPerMinute, commitRateLimitWindowSeconds)
	}

	if existingByNonce, err := s.DB.GetBidCommitByRFQBidderNonce(ctx, p.RFQID, p.Bidder, p.Nonce); err == nil {
		return nil, fmt.Errorf("duplicate nonce for bidder in rfq (existing_commit_id=%d); replacements require a new nonce", existingByNonce.ID)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if existingByCommitment, err := s.DB.GetBidCommitByRFQBidderCommitment(ctx, p.RFQID, p.Bidder, p.Commitment); err == nil {
		return nil, fmt.Errorf("duplicate commitment for bidder in rfq (existing_commit_id=%d)", existingByCommitment.ID)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	dbTx, err := s.DB.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin db tx: %w", err)
	}
	defer dbTx.Rollback()
	if err := s.DB.SupersedeCommittedBidCommitsTx(ctx, dbTx, p.RFQID, p.Bidder); err != nil {
		return nil, err
	}
	c, err := s.DB.CreateBidCommitTx(ctx, dbTx, &storage.BidCommit{
		RFQID:      p.RFQID,
		Bidder:     p.Bidder,
		Commitment: p.Commitment,
		Nonce:      p.Nonce,
		Status:     "committed",
	})
	if err != nil {
		if errors.Is(err, storage.ErrDuplicateBidCommitNonce) {
			existingByNonce, lookupErr := s.DB.GetBidCommitByRFQBidderNonce(ctx, p.RFQID, p.Bidder, p.Nonce)
			if lookupErr == nil {
				return nil, fmt.Errorf("duplicate nonce for bidder in rfq (existing_commit_id=%d); replacements require a new nonce", existingByNonce.ID)
			}
			return nil, errors.New("duplicate nonce for bidder in rfq; replacements require a new nonce")
		}
		if errors.Is(err, storage.ErrDuplicateBidCommitCommitment) {
			existingByCommitment, lookupErr := s.DB.GetBidCommitByRFQBidderCommitment(ctx, p.RFQID, p.Bidder, p.Commitment)
			if lookupErr == nil {
				return nil, fmt.Errorf("duplicate commitment for bidder in rfq (existing_commit_id=%d)", existingByCommitment.ID)
			}
			return nil, errors.New("duplicate commitment for bidder in rfq")
		}
		return nil, fmt.Errorf("create bid_commit: %w", err)
	}
	if err := dbTx.Commit(); err != nil {
		return nil, fmt.Errorf("commit db tx: %w", err)
	}
	return c, nil
}

func (s *Service) RevealBid(ctx context.Context, p RevealBidParams) (*storage.Bid, error) {
	rfq, err := s.DB.GetRFQ(ctx, p.RFQID)
	if err != nil {
		return nil, fmt.Errorf("rfq not found: %w", err)
	}
	if rfq.Status != "open" {
		return nil, fmt.Errorf("rfq is not open (status: %s)", rfq.Status)
	}

	now := time.Now().Unix()
	if now < rfq.CommitDeadline {
		return nil, errors.New("reveal phase has not started")
	}
	if now > rfq.RevealDeadline {
		if err := s.DB.ExpireCommittedBidCommits(ctx, p.RFQID); err != nil {
			return nil, err
		}
		return nil, errors.New("reveal phase has ended")
	}
	if !common.IsHexAddress(p.Bidder) {
		return nil, errors.New("invalid bidder address")
	}
	p.Bidder = common.HexToAddress(p.Bidder).Hex()
	if p.Bidder == common.HexToAddress(rfq.Buyer).Hex() {
		return nil, errors.New("bidder cannot be the same as the rfq buyer")
	}
	if p.Amount == "" {
		return nil, errors.New("amount is required")
	}
	if p.Nonce == "" {
		return nil, errors.New("nonce is required")
	}
	if p.Salt == "" {
		return nil, errors.New("salt is required")
	}
	if p.EstimatedDuration < 0 {
		return nil, errors.New("estimated_duration must be >= 0")
	}
	if p.ExpiresAt == 0 {
		p.ExpiresAt = rfq.Deadline
	}
	if p.ReputationBond == "" {
		p.ReputationBond = "0"
	}
	milestonesJSON, err := canonicalMilestonesJSON(p.MilestonesJSON)
	if err != nil {
		return nil, err
	}
	p.MilestonesJSON = milestonesJSON

	commit, err := s.DB.GetBidCommitByRFQBidderNonce(ctx, p.RFQID, p.Bidder, p.Nonce)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("commit not found for bidder+nonce")
		}
		return nil, err
	}
	if commit.Status != "committed" {
		return nil, fmt.Errorf("commit is not revealable (status: %s)", commit.Status)
	}

	expected, err := computeBidCommitment(p.RFQID, p)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(expected, commit.Commitment) {
		return nil, errors.New("invalid reveal: commitment mismatch")
	}

	amount, ok := new(big.Int).SetString(p.Amount, 10)
	if !ok || amount.Sign() <= 0 {
		return nil, errors.New("invalid amount")
	}
	budgetMin, ok := new(big.Int).SetString(rfq.BudgetMin, 10)
	if !ok {
		return nil, fmt.Errorf("invalid rfq budget_min: %s", rfq.BudgetMin)
	}
	budgetMax, ok := new(big.Int).SetString(rfq.BudgetMax, 10)
	if !ok {
		return nil, fmt.Errorf("invalid rfq budget_max: %s", rfq.BudgetMax)
	}
	if amount.Cmp(budgetMin) < 0 || amount.Cmp(budgetMax) > 0 {
		return nil, fmt.Errorf("amount must be between budget_min (%s) and budget_max (%s)", rfq.BudgetMin, rfq.BudgetMax)
	}

	if p.ExpiresAt <= now {
		return nil, errors.New("bid expires_at must be in the future")
	}
	if p.ExpiresAt > rfq.Deadline {
		return nil, errors.New("bid expires_at must not exceed rfq deadline")
	}
	rb, ok := new(big.Int).SetString(p.ReputationBond, 10)
	if !ok {
		return nil, errors.New("invalid reputation_bond")
	}
	if rb.Sign() < 0 {
		return nil, errors.New("invalid reputation_bond: negative value")
	}

	// Validate and verify attestation credentials if provided.
	credJSON := p.CredentialsJSON
	if credJSON == "" {
		credJSON = "[]"
	}
	attestations, err := ParseAttestations(credJSON)
	if err != nil {
		return nil, fmt.Errorf("invalid credentials_json: %w", err)
	}
	now2 := time.Now()
	for i, att := range attestations {
		if err := ValidateAttestation(&att, p.Bidder, now2); err != nil {
			return nil, fmt.Errorf("credential[%d] validation failed: %w", i, err)
		}
	}

	requirements, err := ParseCredentialRequirements(rfq.RequiredCredentialsJSON)
	if err != nil {
		return nil, fmt.Errorf("parse rfq credential requirements: %w", err)
	}
	matchResult := MatchRequirements(requirements, attestations)
	matchSummaryJSON := MarshalVerificationResult(matchResult)

	dbTx, err := s.DB.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin db tx: %w", err)
	}
	bid, err := s.DB.CreateBidTx(ctx, dbTx, &storage.Bid{
		RFQID:                  p.RFQID,
		Bidder:                 p.Bidder,
		Amount:                 p.Amount,
		EstimatedDuration:      p.EstimatedDuration,
		ReputationBond:         p.ReputationBond,
		MilestonesJSON:         p.MilestonesJSON,
		Message:                p.Message,
		Status:                 "pending",
		ExpiresAt:              p.ExpiresAt,
		StakeMandateID:         p.StakeMandateID,
		CredentialsJSON:        credJSON,
		CredentialVerified:     matchResult.Verified,
		CredentialMatchSummary: matchSummaryJSON,
	})
	if err != nil {
		dbTx.Rollback()
		return nil, fmt.Errorf("create bid: %w", err)
	}
	if err := s.DB.UpdateBidCommitRevealTx(ctx, dbTx, commit.ID, bid.ID); err != nil {
		dbTx.Rollback()
		return nil, fmt.Errorf("update bid commit reveal: %w", err)
	}
	if err := dbTx.Commit(); err != nil {
		return nil, fmt.Errorf("commit db tx: %w", err)
	}

	return bid, nil
}

// AcceptBid accepts a bid, creates an on-chain escrow, and updates the database.
func (s *Service) AcceptBid(ctx context.Context, p AcceptBidParams) (*AcceptBidResult, error) {
	rfq, err := s.DB.GetRFQ(ctx, p.RFQID)
	if err != nil {
		return nil, fmt.Errorf("rfq not found: %w", err)
	}
	if rfq.Status != "open" {
		return nil, fmt.Errorf("rfq is not open (status: %s)", rfq.Status)
	}
	now := time.Now().Unix()
	var sealedSummary *SealedBidSummary
	if rfq.BiddingMode == "sealed" && now > rfq.RevealDeadline {
		sealedSummary, err = s.FinalizeSealedBidding(ctx, p.RFQID)
		if err != nil {
			return nil, err
		}
		rfq, err = s.DB.GetRFQ(ctx, p.RFQID)
		if err != nil {
			return nil, fmt.Errorf("rfq not found after finalization: %w", err)
		}
	}
	if rfq.BiddingMode == "sealed" && now <= rfq.RevealDeadline {
		return nil, errors.New("cannot accept before reveal phase ends")
	}
	if rfq.Deadline <= now {
		return nil, errors.New("rfq deadline has passed")
	}
	if p.Caller == "" || p.Caller != rfq.Buyer {
		return nil, errors.New("only the rfq buyer can accept bids")
	}

	bid, err := s.DB.GetBid(ctx, p.BidID)
	if err != nil {
		return nil, fmt.Errorf("bid not found: %w", err)
	}
	if bid.RFQID != p.RFQID {
		return nil, errors.New("bid does not belong to this rfq")
	}
	if bid.Status != "pending" {
		return nil, fmt.Errorf("bid is not pending (status: %s)", bid.Status)
	}

	if bid.ExpiresAt <= now {
		return nil, errors.New("bid has expired")
	}
	if sealedSummary != nil {
		if sealedSummary.SealedBidStatus == "no_valid_reveals" {
			return nil, errors.New("no valid revealed bids are available for acceptance")
		}
		if sealedSummary.BestBidID == nil || *sealedSummary.BestBidID != bid.ID {
			return nil, errors.New("bid is not the selected best bid for this sealed RFQ")
		}
	}
	// Enforce credential match when the RFQ has required credentials.
	requirements, credErr := ParseCredentialRequirements(rfq.RequiredCredentialsJSON)
	if credErr != nil {
		return nil, fmt.Errorf("parse rfq credential requirements: %w", credErr)
	}
	if len(requirements) > 0 && !bid.CredentialVerified {
		return nil, errors.New("bid does not satisfy rfq credential requirements")
	}

	commit, err := s.DB.GetBidCommitByRevealedBidID(ctx, bid.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("bid is missing sealed commit reveal linkage")
		}
		return nil, err
	}
	if commit.Status != "revealed" {
		return nil, fmt.Errorf("bid commit is not revealable (status: %s)", commit.Status)
	}

	amount, ok := new(big.Int).SetString(bid.Amount, 10)
	if !ok {
		return nil, errors.New("invalid bid amount")
	}

	workerStakeVal := big.NewInt(0)
	if rfq.WorkerStake != "" && rfq.WorkerStake != "0" {
		ws, ok := new(big.Int).SetString(rfq.WorkerStake, 10)
		if !ok {
			return nil, errors.New("invalid rfq worker_stake")
		}
		workerStakeVal = ws
	}

	specHash := crypto.Keccak256Hash([]byte(rfq.Title + rfq.Description))

	var tokenAddr common.Address
	if rfq.Token != "" && rfq.Token != "0x0000000000000000000000000000000000000000" {
		tokenAddr = common.HexToAddress(rfq.Token)
	}
	if rfq.Verifier == "" || !common.IsHexAddress(rfq.Verifier) {
		return nil, errors.New("rfq verifier is required to create escrow")
	}
	var verifierPanel [7]common.Address
	verifierPanel[0] = common.HexToAddress(rfq.Verifier)

	var parentEscrowAddr common.Address
	if rfq.ParentEscrowID != nil {
		parentEscrow, err := s.DB.GetEscrow(ctx, *rfq.ParentEscrowID)
		if err != nil {
			return nil, fmt.Errorf("parent escrow lookup failed: %w", err)
		}
		if !common.IsHexAddress(parentEscrow.EscrowAddress) {
			return nil, fmt.Errorf("parent escrow %d has invalid on-chain address %q", *rfq.ParentEscrowID, parentEscrow.EscrowAddress)
		}
		parentEscrowAddr = common.HexToAddress(parentEscrow.EscrowAddress)
	}

	// Use bid milestones if provided, fall back to RFQ milestones.
	milestonesRaw := bid.MilestonesJSON
	if milestonesRaw == "" || milestonesRaw == "[]" {
		milestonesRaw = rfq.MilestonesJSON
	}
	milestones, err := parseMilestonesJSON(milestonesRaw)
	if err != nil {
		return nil, fmt.Errorf("milestones: %w", err)
	}

	factory := common.HexToAddress(s.Cfg.FactoryAddress)
	submissionDeadline, err := numconv.Int64ToUint64(rfq.Deadline, "rfq.deadline")
	if err != nil {
		return nil, err
	}
	reviewPeriodSeconds, err := numconv.Int64ToUint64(rfq.ReviewPeriodSeconds, "rfq.review_period_seconds")
	if err != nil {
		return nil, err
	}
	disputePeriodSeconds, err := numconv.Int64ToUint64(rfq.DisputePeriodSeconds, "rfq.dispute_period_seconds")
	if err != nil {
		return nil, err
	}
	arbitratorTimeoutSeconds, err := numconv.Int64ToUint64(rfq.ArbitratorTimeoutSeconds, "rfq.arbitrator_timeout_seconds")
	if err != nil {
		return nil, err
	}
	params := chain.CreateEscrowParams{
		Buyer:                    common.HexToAddress(rfq.Buyer),
		Worker:                   common.HexToAddress(bid.Bidder),
		VerifierPanel:            verifierPanel,
		QuorumThreshold:          1,
		QuorumVerifierCount:      1,
		VerifierStakePerVerifier: big.NewInt(0),
		Arbitrator:               common.HexToAddress(rfq.Arbitrator),
		Amount:                   amount,
		WorkerStake:              workerStakeVal,
		SubmissionDeadline:       submissionDeadline,
		ReviewPeriodSeconds:      reviewPeriodSeconds,
		DisputePeriodSeconds:     disputePeriodSeconds,
		TaskSpecHash:             specHash,
		ArbitratorTimeoutSeconds: arbitratorTimeoutSeconds,
		Token:                    tokenAddr,
		ServiceTier:              uint8(rfq.ServiceTier), //nolint:gosec // validated 0-1 at RFQ creation
		ParentEscrow:             parentEscrowAddr,
		Milestones:               milestones,
	}

	tx, err := s.Chain.CreateEscrow(ctx, factory, params)
	if err != nil {
		return nil, fmt.Errorf("chain CreateEscrow: %w", err)
	}

	result, err := chain.WaitMinedAndParseEscrow(ctx, s.Chain, tx.Hash())
	if err != nil {
		return nil, fmt.Errorf("receipt error: %w", err)
	}

	milestoneCount := 1
	if len(milestones) > 0 {
		milestoneCount = len(milestones)
	}

	dbTx, err := s.DB.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin db tx: %w", err)
	}

	task, err := s.DB.CreateTaskTx(ctx, dbTx, rfq.Title, rfq.Description, specHash.Hex())
	if err != nil {
		dbTx.Rollback()
		return nil, fmt.Errorf("db CreateTask: %w", err)
	}

	escrow, err := s.DB.CreateEscrowTx(ctx, dbTx, &storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  s.Cfg.ChainID,
		FactoryAddress:           s.Cfg.FactoryAddress,
		EscrowAddress:            result.EscrowAddress.Hex(),
		EscrowID:                 result.EscrowID,
		Buyer:                    rfq.Buyer,
		Worker:                   bid.Bidder,
		Verifier:                 rfq.Verifier,
		VerifierPanelJSON:        fmt.Sprintf("[%q]", strings.ToLower(rfq.Verifier)),
		QuorumThreshold:          1,
		QuorumVerifierCount:      1,
		VerifierStakePerVerifier: "0",
		Arbitrator:               rfq.Arbitrator,
		Amount:                   bid.Amount,
		WorkerStake:              workerStakeVal.String(),
		Token:                    tokenAddr.Hex(),
		Status:                   "created",
		SubmissionDeadline:       rfq.Deadline,
		ReviewPeriodSeconds:      rfq.ReviewPeriodSeconds,
		DisputePeriodSeconds:     rfq.DisputePeriodSeconds,
		ArbitratorTimeoutSeconds: rfq.ArbitratorTimeoutSeconds,
		MilestoneCount:           milestoneCount,
		CurrentMilestone:         0,
		ActiveWorker:             bid.Bidder,
		ServiceTier:              rfq.ServiceTier,
		ParentEscrowID:           rfq.ParentEscrowID,
	})
	if err != nil {
		dbTx.Rollback()
		return nil, fmt.Errorf("db CreateEscrow: %w", err)
	}

	for i, m := range milestones {
		milestoneDeadline, convErr := numconv.Uint64ToInt64(m.SubmissionDeadline, fmt.Sprintf("milestones[%d].submission_deadline", i))
		if convErr != nil {
			dbTx.Rollback()
			return nil, convErr
		}
		_, err := s.DB.CreateMilestoneTx(ctx, dbTx, &storage.MilestoneRecord{
			EscrowID:           escrow.ID,
			MilestoneIndex:     i,
			Amount:             m.Amount.String(),
			SubmissionDeadline: milestoneDeadline,
			Status:             "pending",
		})
		if err != nil {
			dbTx.Rollback()
			return nil, fmt.Errorf("db CreateMilestone[%d]: %w", i, err)
		}
	}

	if err := s.DB.AcceptBidTx(ctx, dbTx, bid.ID, escrow.ID); err != nil {
		dbTx.Rollback()
		return nil, fmt.Errorf("db AcceptBid: %w", err)
	}
	if err := s.DB.UpdateRFQStatusTx(ctx, dbTx, rfq.ID, "closed"); err != nil {
		dbTx.Rollback()
		return nil, fmt.Errorf("db UpdateRFQStatus: %w", err)
	}
	if err := s.DB.RejectPendingBidsTx(ctx, dbTx, rfq.ID, bid.ID); err != nil {
		dbTx.Rollback()
		return nil, fmt.Errorf("db RejectPendingBids: %w", err)
	}
	if err := s.DB.UpdateBidCommitStatusByRevealedBidTx(ctx, dbTx, bid.ID, "accepted"); err != nil {
		dbTx.Rollback()
		return nil, fmt.Errorf("db UpdateBidCommitStatusByRevealedBid: %w", err)
	}
	if err := s.DB.RejectUnacceptedBidCommitsTx(ctx, dbTx, rfq.ID, bid.ID); err != nil {
		dbTx.Rollback()
		return nil, fmt.Errorf("db RejectUnacceptedBidCommits: %w", err)
	}

	if err := dbTx.Commit(); err != nil {
		return nil, fmt.Errorf("commit db tx: %w", err)
	}

	if err := s.Idx.RunOnce(ctx); err != nil {
		slog.Warn("post-accept indexer run failed", "rfq_id", rfq.ID, "error", err)
	}

	updatedBid, err := s.DB.GetBid(ctx, bid.ID)
	if err != nil {
		return nil, fmt.Errorf("db GetBid after accept: %w", err)
	}

	return &AcceptBidResult{
		Bid:    updatedBid,
		Escrow: escrow,
		Task:   task,
		TxHash: tx.Hash().Hex(),
	}, nil
}
