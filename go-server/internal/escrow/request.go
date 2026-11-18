package escrow

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/numconv"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// CreateMilestoneRequest carries a milestone amount and its submission deadline.
type CreateMilestoneRequest[D ~string] struct {
	Amount             D `json:"amount" jsonschema:"Milestone amount in wei or smallest unit"`
	SubmissionDeadline D `json:"submission_deadline" jsonschema:"Unix timestamp for milestone submission deadline"`
}

// CreateRequest is the shared wire shape. HTTP and UCP use native JSON types,
// while MCP supplies its numeric-string type for decimal and integer fields.
type CreateRequest[D ~string, N int | ~string, P *int64 | ~string] struct {
	Title                    string                      `json:"title" jsonschema:"Task title"`
	Description              string                      `json:"description" jsonschema:"Task description"`
	Buyer                    string                      `json:"buyer" jsonschema:"Buyer address (0x...)"`
	Worker                   string                      `json:"worker" jsonschema:"Worker address (0x...). Must be distinct from buyer, verifier panel members, and arbitrator."`
	VerifierPanel            []string                    `json:"verifier_panel" jsonschema:"Verifier panel addresses (1-7 entries). Members must be distinct and must not overlap other roles."`
	QuorumThreshold          N                           `json:"quorum_threshold" jsonschema:"Votes required to finalize verifier decision (1..quorum_verifier_count)."`
	QuorumVerifierCount      N                           `json:"quorum_verifier_count" jsonschema:"Number of active verifier panel entries (1..7). verifier_panel must have at least this many entries."`
	VerifierStakePerVerifier D                           `json:"verifier_stake_per_verifier,omitempty" jsonschema:"Optional Schelling stake each verifier must deposit before voting; omit or 0 for no verifier stake."`
	Arbitrator               string                      `json:"arbitrator" jsonschema:"Arbitrator address (0x...). Must be distinct from buyer, worker, and all verifier panel members."`
	Amount                   D                           `json:"amount" jsonschema:"Total amount in wei (ETH) or smallest unit (ERC20)"`
	WorkerStake              D                           `json:"worker_stake,omitempty" jsonschema:"Worker anti-Sybil stake in wei or smallest unit; omit or 0 for no stake"`
	SubmissionDeadline       D                           `json:"submission_deadline" jsonschema:"Unix timestamp for submission deadline"`
	ReviewPeriodSeconds      D                           `json:"review_period_seconds" jsonschema:"Review period in seconds"`
	DisputePeriodSeconds     D                           `json:"dispute_period_seconds" jsonschema:"Dispute period in seconds"`
	ArbitratorTimeoutSeconds D                           `json:"arbitrator_timeout_seconds" jsonschema:"Arbitrator timeout in seconds"`
	Token                    string                      `json:"token,omitempty" jsonschema:"Omit or leave empty for ETH. ERC20 token address otherwise."`
	ServiceTier              N                           `json:"service_tier,omitempty" jsonschema:"0 = low_assurance (optimistic, default), 1 = high_assurance (verifier approval required). Paper §5.3."`
	Milestones               []CreateMilestoneRequest[D] `json:"milestones,omitempty" jsonschema:"Optional array of milestones; omit for single-milestone (V1) escrow"`
	BackupWorker             string                      `json:"backup_worker,omitempty" jsonschema:"Optional backup worker address; omit for no backup agent"`
	BackupDeadlineExtension  D                           `json:"backup_deadline_extension,omitempty" jsonschema:"Seconds to extend deadline when backup activates; omit or 0 for no extension"`
	ZKVerifier               string                      `json:"zk_verifier,omitempty" jsonschema:"Optional on-chain ZK verifier contract address; required with circuit_id"`
	CircuitID                string                      `json:"circuit_id,omitempty" jsonschema:"Optional 0x-prefixed bytes32 circuit identifier; required with zk_verifier"`
	ParentEscrowID           P                           `json:"parent_escrow_id,omitempty" jsonschema:"Optional parent escrow ID for sub-delegation. Buyer must match the parent's active worker."`
}

// CreateEscrowRequest is the JSON payload accepted by the HTTP and UCP endpoints.
type CreateEscrowRequest = CreateRequest[string, int, *int64]

// ParseCreateRequest validates and resolves a create payload before any writes or chain calls.
func ParseCreateRequest[D ~string, N int | ~string, P *int64 | ~string](
	ctx context.Context, req CreateRequest[D, N, P], db *storage.DB, cfg *config.Config,
) (CreateEscrowInput, error) {
	amount, ok := new(big.Int).SetString(string(req.Amount), 10)
	if !ok {
		return CreateEscrowInput{}, errors.New("invalid amount")
	}
	if err := chain.ValidateComplexityFloor(amount, cfg.ComplexityFloor); err != nil {
		return CreateEscrowInput{}, err
	}
	if amount.Sign() < 0 {
		return CreateEscrowInput{}, errors.New("amount must not be negative")
	}

	deadline, deadlineDB, err := parseCreatePeriod(string(req.SubmissionDeadline), "submission_deadline", "submission_deadline")
	if err != nil {
		return CreateEscrowInput{}, err
	}
	review, reviewDB, err := parseCreatePeriod(string(req.ReviewPeriodSeconds), "review_period_seconds", "review_period_seconds")
	if err != nil {
		return CreateEscrowInput{}, err
	}
	dispute, disputeDB, err := parseCreatePeriod(string(req.DisputePeriodSeconds), "dispute_period_seconds", "dispute_period_seconds")
	if err != nil {
		return CreateEscrowInput{}, err
	}
	arbTimeout, arbTimeoutDB, err := parseCreatePeriod(string(req.ArbitratorTimeoutSeconds), "arbitrator_timeout_seconds", "arbitrator_timeout_seconds")
	if err != nil {
		return CreateEscrowInput{}, err
	}

	buyer, err := parseCreateAddress(req.Buyer, "buyer")
	if err != nil {
		return CreateEscrowInput{}, err
	}
	worker, err := parseCreateAddress(req.Worker, "worker")
	if err != nil {
		return CreateEscrowInput{}, err
	}
	arbitrator, err := parseCreateAddress(req.Arbitrator, "arbitrator")
	if err != nil {
		return CreateEscrowInput{}, err
	}
	if err := validateDistinctCreateRoles(buyer, worker, arbitrator); err != nil {
		return CreateEscrowInput{}, err
	}

	if len(req.VerifierPanel) == 0 || len(req.VerifierPanel) > 7 {
		return CreateEscrowInput{}, errors.New("verifier_panel must include between 1 and 7 addresses")
	}
	quorumCount, err := strconv.ParseUint(fmt.Sprint(req.QuorumVerifierCount), 10, 8)
	if err != nil || quorumCount == 0 || quorumCount > 7 {
		return CreateEscrowInput{}, errors.New("quorum_verifier_count must be between 1 and 7")
	}
	if len(req.VerifierPanel) < int(quorumCount) {
		return CreateEscrowInput{}, errors.New("verifier_panel length must be at least quorum_verifier_count")
	}
	quorumThreshold, err := strconv.ParseUint(fmt.Sprint(req.QuorumThreshold), 10, 8)
	if err != nil || quorumThreshold == 0 || quorumThreshold > quorumCount {
		return CreateEscrowInput{}, errors.New("quorum_threshold must be between 1 and quorum_verifier_count")
	}
	// Only active entries are sent on-chain and persisted.
	seen := make(map[common.Address]bool, quorumCount)
	for i, raw := range req.VerifierPanel[:quorumCount] {
		addr, err := parseCreateAddress(raw, fmt.Sprintf("verifier_panel[%d]", i))
		if err != nil {
			return CreateEscrowInput{}, err
		}
		if seen[addr] {
			return CreateEscrowInput{}, fmt.Errorf("duplicate verifier_panel[%d] address", i)
		}
		if addr == buyer || addr == worker || addr == arbitrator {
			return CreateEscrowInput{}, fmt.Errorf("verifier_panel[%d] must not overlap buyer, worker, or arbitrator", i)
		}
		seen[addr] = true
	}

	workerStake, err := parseCreateStake(string(req.WorkerStake), "worker_stake")
	if err != nil {
		return CreateEscrowInput{}, err
	}
	verifierStake, err := parseCreateStake(string(req.VerifierStakePerVerifier), "verifier_stake_per_verifier")
	if err != nil {
		return CreateEscrowInput{}, err
	}

	if req.Token != "" && !common.IsHexAddress(req.Token) {
		return CreateEscrowInput{}, errors.New("invalid token address")
	}
	var milestones []chain.MilestoneParam
	milestoneDeadlines := make([]int64, len(req.Milestones))
	for i, m := range req.Milestones {
		amount, ok := new(big.Int).SetString(string(m.Amount), 10)
		if !ok {
			return CreateEscrowInput{}, errors.New("invalid milestone amount")
		}
		if amount.Sign() < 0 {
			return CreateEscrowInput{}, errors.New("milestone amount must not be negative")
		}
		deadline, dbDeadline, err := parseCreatePeriod(string(m.SubmissionDeadline), "milestone submission_deadline", fmt.Sprintf("milestones[%d].submission_deadline", i))
		if err != nil {
			return CreateEscrowInput{}, err
		}
		milestones = append(milestones, chain.MilestoneParam{Amount: amount, SubmissionDeadline: deadline})
		milestoneDeadlines[i] = dbDeadline
	}

	// The JSON endpoints rejected the canonical zero address. Other hex
	// spellings of zero still denote an omitted backup when no extension is set.
	if req.BackupWorker != "" && (!common.IsHexAddress(req.BackupWorker) || req.BackupWorker == (common.Address{}).Hex()) {
		return CreateEscrowInput{}, errors.New("invalid backup_worker address")
	}
	backupWorker := common.HexToAddress(req.BackupWorker)
	var backupDeadline uint64
	var backupDeadlineDB int64
	if req.BackupDeadlineExtension != "" {
		if backupWorker == (common.Address{}) {
			return CreateEscrowInput{}, errors.New("backup_deadline_extension set without backup_worker")
		}
		backupDeadline, backupDeadlineDB, err = parseCreatePeriod(string(req.BackupDeadlineExtension), "backup_deadline_extension", "backup_deadline_extension")
		if err != nil {
			return CreateEscrowInput{}, err
		}
	}

	if req.ZKVerifier != "" && !common.IsHexAddress(req.ZKVerifier) {
		return CreateEscrowInput{}, errors.New("invalid zk_verifier address")
	}
	zkVerifier := common.HexToAddress(req.ZKVerifier)
	circuitID, err := numconv.ParseOptionalBytes32Hex(req.CircuitID)
	if err != nil {
		return CreateEscrowInput{}, fmt.Errorf("invalid circuit_id: %w", err)
	}
	if (zkVerifier == (common.Address{})) != (req.CircuitID == "") {
		return CreateEscrowInput{}, errors.New("zk_verifier and circuit_id must either both be set or both omitted")
	}

	var tier uint8
	switch fmt.Sprint(req.ServiceTier) {
	case "", "0":
	case "1":
		tier = 1
	default:
		return CreateEscrowInput{}, errors.New("invalid service_tier: must be 0 (low_assurance) or 1 (high_assurance)")
	}

	parentID, err := parseCreateParentID(req.ParentEscrowID)
	if err != nil {
		return CreateEscrowInput{}, err
	}
	var parent common.Address
	if parentID != nil {
		parent, err = resolveCreateParent(ctx, db, cfg, *parentID, req.Buyer, buyer)
		if err != nil {
			return CreateEscrowInput{}, err
		}
	}

	return CreateEscrowInput{
		Title:                      req.Title,
		Description:                req.Description,
		Buyer:                      req.Buyer,
		Worker:                     req.Worker,
		Arbitrator:                 req.Arbitrator,
		VerifierPanel:              req.VerifierPanel,
		QuorumThreshold:            uint8(quorumThreshold),
		QuorumVerifierCount:        uint8(quorumCount),
		VerifierStakePerVerifier:   verifierStake,
		Amount:                     amount,
		WorkerStake:                workerStake,
		SubmissionDeadline:         deadline,
		SubmissionDeadlineDB:       deadlineDB,
		ReviewPeriodSeconds:        review,
		ReviewPeriodSecondsDB:      reviewDB,
		DisputePeriodSeconds:       dispute,
		DisputePeriodSecondsDB:     disputeDB,
		ArbitratorTimeoutSeconds:   arbTimeout,
		ArbitratorTimeoutSecondsDB: arbTimeoutDB,
		Token:                      common.HexToAddress(req.Token),
		ServiceTier:                tier,
		Milestones:                 milestones,
		MilestoneDeadlines:         milestoneDeadlines,
		BackupWorker:               backupWorker,
		BackupDeadlineExtension:    backupDeadline,
		BackupDeadlineDB:           backupDeadlineDB,
		ZKVerifier:                 zkVerifier,
		CircuitID:                  circuitID,
		ParentEscrowID:             parentID,
		ParentEscrow:               parent,
		TaskSpecHash:               crypto.Keccak256Hash([]byte(req.Title + req.Description)),
	}, nil
}

func parseCreatePeriod(raw, field, dbField string) (uint64, int64, error) {
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid %s", field)
	}
	signed, err := numconv.Uint64ToInt64(value, dbField)
	return value, signed, err
}

func parseCreateAddress(raw, field string) (common.Address, error) {
	if !common.IsHexAddress(raw) {
		return common.Address{}, fmt.Errorf("invalid %s address", field)
	}
	addr := common.HexToAddress(raw)
	if addr == (common.Address{}) {
		return common.Address{}, fmt.Errorf("zero %s address", field)
	}
	return addr, nil
}

func parseCreateStake(raw, field string) (*big.Int, error) {
	if raw == "" {
		return big.NewInt(0), nil
	}
	stake, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return nil, fmt.Errorf("invalid %s", field)
	}
	if stake.Sign() < 0 {
		return nil, fmt.Errorf("%s must not be negative", field)
	}
	return stake, nil
}

func parseCreateParentID[P *int64 | ~string](raw P) (*int64, error) {
	if id, ok := any(raw).(*int64); ok {
		return id, nil
	}
	text := fmt.Sprint(raw)
	if text == "" {
		return nil, nil
	}
	id, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid parent_escrow_id: %w", err)
	}
	return &id, nil
}

func resolveCreateParent(ctx context.Context, db *storage.DB, cfg *config.Config, id int64, rawBuyer string, buyer common.Address) (common.Address, error) {
	parent, err := db.GetEscrow(ctx, id)
	if err != nil {
		return common.Address{}, fmt.Errorf("invalid parent_escrow_id: %w", err)
	}
	addr := common.HexToAddress(parent.EscrowAddress)
	if !common.IsHexAddress(parent.EscrowAddress) || addr == (common.Address{}) {
		return common.Address{}, fmt.Errorf("parent escrow %d has invalid on-chain address", id)
	}
	if parent.ChainID != cfg.ChainID || common.HexToAddress(parent.FactoryAddress) != common.HexToAddress(cfg.FactoryAddress) {
		return common.Address{}, fmt.Errorf("parent escrow %d is from different chain/factory", id)
	}
	activeWorker := parent.ActiveWorker
	if activeWorker == "" {
		activeWorker = parent.Worker
	}
	if buyer != common.HexToAddress(activeWorker) {
		return common.Address{}, fmt.Errorf("sub-delegation buyer (%s) must be active worker of parent escrow %d (%s)", rawBuyer, id, activeWorker)
	}
	return addr, nil
}
