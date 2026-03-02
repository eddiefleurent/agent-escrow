package escrow

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/attestation"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/numconv"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const zeroAddress = "0x0000000000000000000000000000000000000000"

// ErrValidation is returned when caller-provided input fails validation.
// HTTP handlers should map this to a 4xx response; MCP tools may surface the message directly.
var ErrValidation = errors.New("validation error")

const (
	escrowStatusPendingCreate       = "pending"
	escrowStatusSubmittingCreateTx  = "submitting"
	escrowStatusPendingConfirmation = "pending_confirmation"
	escrowStatusCreateFinalized     = "created"
)

func IsValidation(err error) bool {
	return errors.Is(err, ErrValidation)
}

// Service centralizes escrow lifecycle orchestration shared by API, MCP, and UCP.
type Service struct {
	DB    *storage.DB
	Chain chain.ChainClient
	Idx   *indexer.Indexer
	Cfg   *config.Config
}

func NewService(db *storage.DB, chainClient chain.ChainClient, idx *indexer.Indexer, cfg *config.Config) *Service {
	return &Service{
		DB:    db,
		Chain: chainClient,
		Idx:   idx,
		Cfg:   cfg,
	}
}

type CreateEscrowInput struct {
	Title       string
	Description string

	Buyer         string
	Worker        string
	Arbitrator    string
	VerifierPanel []string

	QuorumThreshold          uint8
	QuorumVerifierCount      uint8
	VerifierStakePerVerifier *big.Int

	Amount      *big.Int
	WorkerStake *big.Int

	SubmissionDeadline       uint64
	ReviewPeriodSeconds      uint64
	DisputePeriodSeconds     uint64
	ArbitratorTimeoutSeconds uint64

	SubmissionDeadlineDB       int64
	ReviewPeriodSecondsDB      int64
	DisputePeriodSecondsDB     int64
	ArbitratorTimeoutSecondsDB int64

	Token       common.Address
	ServiceTier uint8

	Milestones         []chain.MilestoneParam
	MilestoneDeadlines []int64

	BackupWorker            common.Address
	BackupDeadlineExtension uint64
	BackupDeadlineDB        int64

	ZKVerifier common.Address
	CircuitID  [32]byte

	ParentEscrowID *int64
	ParentEscrow   common.Address

	TaskSpecHash common.Hash
}

type CreateEscrowResult struct {
	EscrowID       int64
	TxHash         string
	TaskID         int64
	EscrowAddress  string
	ChainEscrowID  int64
	MilestoneCount int
}

type createEscrowIntentMilestone struct {
	Amount             string `json:"amount"`
	SubmissionDeadline int64  `json:"submission_deadline"`
}

type createEscrowIntentPayload struct {
	ChainID                  int64                         `json:"chain_id"`
	FactoryAddress           string                        `json:"factory_address"`
	TaskSpecHash             string                        `json:"task_spec_hash"`
	Buyer                    string                        `json:"buyer"`
	Worker                   string                        `json:"worker"`
	Arbitrator               string                        `json:"arbitrator"`
	VerifierPanel            []string                      `json:"verifier_panel"`
	QuorumThreshold          uint8                         `json:"quorum_threshold"`
	QuorumVerifierCount      uint8                         `json:"quorum_verifier_count"`
	VerifierStakePerVerifier string                        `json:"verifier_stake_per_verifier"`
	Amount                   string                        `json:"amount"`
	WorkerStake              string                        `json:"worker_stake"`
	SubmissionDeadline       uint64                        `json:"submission_deadline"`
	ReviewPeriodSeconds      uint64                        `json:"review_period_seconds"`
	DisputePeriodSeconds     uint64                        `json:"dispute_period_seconds"`
	ArbitratorTimeoutSeconds uint64                        `json:"arbitrator_timeout_seconds"`
	Token                    string                        `json:"token"`
	ServiceTier              uint8                         `json:"service_tier"`
	Milestones               []createEscrowIntentMilestone `json:"milestones"`
	BackupWorker             string                        `json:"backup_worker"`
	BackupDeadlineExtension  uint64                        `json:"backup_deadline_extension"`
	ZKVerifier               string                        `json:"zk_verifier"`
	CircuitID                string                        `json:"circuit_id"`
	ParentEscrowID           int64                         `json:"parent_escrow_id"`
	ParentEscrow             string                        `json:"parent_escrow"`
}

func pendingEscrowAddress(createIntentID string) string {
	return "pending:" + strings.TrimPrefix(createIntentID, "0x")
}

func buildCreateEscrowIntentID(
	input CreateEscrowInput,
	chainID int64,
	factory common.Address,
	buyer common.Address,
	worker common.Address,
	arbitrator common.Address,
	verifierPanel []string,
) (string, error) {
	milestones := make([]createEscrowIntentMilestone, len(input.Milestones))
	for i, m := range input.Milestones {
		milestones[i] = createEscrowIntentMilestone{
			Amount:             m.Amount.String(),
			SubmissionDeadline: input.MilestoneDeadlines[i],
		}
	}

	parentEscrowID := int64(0)
	if input.ParentEscrowID != nil {
		parentEscrowID = *input.ParentEscrowID
	}

	payload := createEscrowIntentPayload{
		ChainID:                  chainID,
		FactoryAddress:           strings.ToLower(factory.Hex()),
		TaskSpecHash:             strings.ToLower(input.TaskSpecHash.Hex()),
		Buyer:                    strings.ToLower(buyer.Hex()),
		Worker:                   strings.ToLower(worker.Hex()),
		Arbitrator:               strings.ToLower(arbitrator.Hex()),
		VerifierPanel:            verifierPanel,
		QuorumThreshold:          input.QuorumThreshold,
		QuorumVerifierCount:      input.QuorumVerifierCount,
		VerifierStakePerVerifier: input.VerifierStakePerVerifier.String(),
		Amount:                   input.Amount.String(),
		WorkerStake:              input.WorkerStake.String(),
		SubmissionDeadline:       input.SubmissionDeadline,
		ReviewPeriodSeconds:      input.ReviewPeriodSeconds,
		DisputePeriodSeconds:     input.DisputePeriodSeconds,
		ArbitratorTimeoutSeconds: input.ArbitratorTimeoutSeconds,
		Token:                    strings.ToLower(input.Token.Hex()),
		ServiceTier:              input.ServiceTier,
		Milestones:               milestones,
		BackupWorker:             strings.ToLower(input.BackupWorker.Hex()),
		BackupDeadlineExtension:  input.BackupDeadlineExtension,
		ZKVerifier:               strings.ToLower(input.ZKVerifier.Hex()),
		CircuitID:                strings.ToLower(fmt.Sprintf("0x%x", input.CircuitID)),
		ParentEscrowID:           parentEscrowID,
		ParentEscrow:             strings.ToLower(input.ParentEscrow.Hex()),
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal create intent payload: %w", err)
	}
	return crypto.Keccak256Hash(raw).Hex(), nil
}

func (s *Service) CreateEscrow(ctx context.Context, input CreateEscrowInput) (*CreateEscrowResult, error) {
	if s.Cfg == nil {
		return nil, errors.New("escrow service misconfigured: missing config")
	}
	if len(input.VerifierPanel) == 0 {
		return nil, fmt.Errorf("%w: verifier panel is required", ErrValidation)
	}
	if input.QuorumVerifierCount == 0 {
		return nil, fmt.Errorf("%w: quorum verifier count must be > 0", ErrValidation)
	}
	if input.QuorumVerifierCount > 7 {
		return nil, fmt.Errorf("%w: quorum verifier count %d exceeds maximum of 7", ErrValidation, input.QuorumVerifierCount)
	}
	if input.QuorumThreshold == 0 || input.QuorumThreshold > input.QuorumVerifierCount {
		return nil, fmt.Errorf(
			"%w: quorum threshold must be > 0 and <= quorum verifier count (threshold=%d, quorum_verifier_count=%d)",
			ErrValidation,
			input.QuorumThreshold,
			input.QuorumVerifierCount,
		)
	}
	if len(input.VerifierPanel) < int(input.QuorumVerifierCount) {
		return nil, fmt.Errorf("%w: verifier panel length %d is smaller than quorum verifier count %d", ErrValidation, len(input.VerifierPanel), input.QuorumVerifierCount)
	}
	if len(input.Milestones) != len(input.MilestoneDeadlines) {
		return nil, fmt.Errorf("%w: milestone metadata mismatch: %d milestones vs %d deadlines", ErrValidation, len(input.Milestones), len(input.MilestoneDeadlines))
	}
	if input.Amount == nil {
		return nil, fmt.Errorf("%w: amount is required", ErrValidation)
	}
	if input.Amount.Sign() < 0 {
		return nil, fmt.Errorf("%w: amount must not be negative", ErrValidation)
	}
	buyer, err := parseAddress("input.Buyer", input.Buyer)
	if err != nil {
		return nil, err
	}
	worker, err := parseAddress("input.Worker", input.Worker)
	if err != nil {
		return nil, err
	}
	arbitrator, err := parseAddress("input.Arbitrator", input.Arbitrator)
	if err != nil {
		return nil, err
	}
	if buyer == worker {
		return nil, fmt.Errorf("%w: buyer and worker must be distinct addresses", ErrValidation)
	}
	if buyer == arbitrator {
		return nil, fmt.Errorf("%w: buyer and arbitrator must be distinct addresses", ErrValidation)
	}
	if worker == arbitrator {
		return nil, fmt.Errorf("%w: worker and arbitrator must be distinct addresses", ErrValidation)
	}
	factory, err := parseAddress("s.Cfg.FactoryAddress", s.Cfg.FactoryAddress)
	if err != nil {
		return nil, err
	}
	if input.VerifierStakePerVerifier == nil {
		input.VerifierStakePerVerifier = big.NewInt(0)
	} else if input.VerifierStakePerVerifier.Sign() < 0 {
		return nil, fmt.Errorf("%w: verifier_stake_per_verifier must not be negative", ErrValidation)
	}
	if input.WorkerStake == nil {
		input.WorkerStake = big.NewInt(0)
	} else if input.WorkerStake.Sign() < 0 {
		return nil, fmt.Errorf("%w: worker_stake must not be negative", ErrValidation)
	}

	var verifierPanel [7]common.Address
	panelForJSON := make([]string, int(input.QuorumVerifierCount))
	seenVerifiers := make(map[common.Address]int, int(input.QuorumVerifierCount))
	for i := 0; i < int(input.QuorumVerifierCount); i++ {
		if !common.IsHexAddress(input.VerifierPanel[i]) {
			return nil, fmt.Errorf("%w: verifier_panel[%d] %q is not a valid hex address", ErrValidation, i, input.VerifierPanel[i])
		}
		addr := common.HexToAddress(input.VerifierPanel[i])
		if addr == (common.Address{}) {
			return nil, fmt.Errorf("%w: input.VerifierPanel[%d] must not be the zero address", ErrValidation, i)
		}
		if addr == buyer {
			return nil, fmt.Errorf("%w: verifier_panel[%d] must not match buyer", ErrValidation, i)
		}
		if addr == worker {
			return nil, fmt.Errorf("%w: verifier_panel[%d] must not match worker", ErrValidation, i)
		}
		if addr == arbitrator {
			return nil, fmt.Errorf("%w: verifier_panel[%d] must not match arbitrator", ErrValidation, i)
		}
		if priorIdx, exists := seenVerifiers[addr]; exists {
			return nil, fmt.Errorf("%w: verifier_panel[%d] duplicates verifier_panel[%d]", ErrValidation, i, priorIdx)
		}
		seenVerifiers[addr] = i
		verifierPanel[i] = addr
		panelForJSON[i] = strings.ToLower(addr.Hex())
	}

	// Pre-validate all milestones before any chain or DB side-effects.
	for i, milestone := range input.Milestones {
		if milestone.Amount == nil {
			return nil, fmt.Errorf("%w: milestone %d: amount is required", ErrValidation, i)
		}
		if milestone.Amount.Sign() < 0 {
			return nil, fmt.Errorf("%w: milestone %d: amount must not be negative", ErrValidation, i)
		}
	}

	params := chain.CreateEscrowParams{
		Buyer:                    buyer,
		Worker:                   worker,
		VerifierPanel:            verifierPanel,
		QuorumThreshold:          input.QuorumThreshold,
		QuorumVerifierCount:      input.QuorumVerifierCount,
		VerifierStakePerVerifier: input.VerifierStakePerVerifier,
		Arbitrator:               arbitrator,
		Amount:                   input.Amount,
		WorkerStake:              input.WorkerStake,
		SubmissionDeadline:       input.SubmissionDeadline,
		ReviewPeriodSeconds:      input.ReviewPeriodSeconds,
		DisputePeriodSeconds:     input.DisputePeriodSeconds,
		TaskSpecHash:             input.TaskSpecHash,
		ArbitratorTimeoutSeconds: input.ArbitratorTimeoutSeconds,
		Token:                    input.Token,
		ServiceTier:              input.ServiceTier,
		Milestones:               input.Milestones,
		BackupWorker:             input.BackupWorker,
		BackupDeadlineExtension:  input.BackupDeadlineExtension,
		ZKVerifier:               input.ZKVerifier,
		CircuitID:                input.CircuitID,
		ParentEscrow:             input.ParentEscrow,
	}

	milestoneCount := 1
	if len(input.Milestones) > 0 {
		milestoneCount = len(input.Milestones)
	}
	panelJSONBytes, err := json.Marshal(panelForJSON)
	if err != nil {
		return nil, fmt.Errorf("marshal verifier panel: %w", err)
	}
	createIntentID, err := buildCreateEscrowIntentID(
		input,
		s.Cfg.ChainID,
		factory,
		buyer,
		worker,
		arbitrator,
		panelForJSON,
	)
	if err != nil {
		return nil, fmt.Errorf("build create intent id: %w", err)
	}

	escrowRecord, err := s.DB.GetEscrowByCreateIntentID(ctx, createIntentID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("get escrow by create intent: %w", err)
		}
		dbTx, txErr := s.DB.BeginTx(ctx)
		if txErr != nil {
			return nil, fmt.Errorf("begin db transaction: %w", txErr)
		}
		defer dbTx.Rollback()

		task, txErr := s.DB.CreateTaskTx(ctx, dbTx, input.Title, input.Description, input.TaskSpecHash.Hex())
		if txErr != nil {
			return nil, fmt.Errorf("create task: %w", txErr)
		}

		escrowRecord, txErr = s.DB.CreateEscrowTx(ctx, dbTx, &storage.Escrow{
			TaskID:                   task.ID,
			ChainID:                  s.Cfg.ChainID,
			FactoryAddress:           s.Cfg.FactoryAddress,
			EscrowAddress:            pendingEscrowAddress(createIntentID),
			EscrowID:                 0,
			CreateIntentID:           createIntentID,
			CreateTxHash:             "",
			Buyer:                    input.Buyer,
			Worker:                   input.Worker,
			Verifier:                 panelForJSON[0],
			VerifierPanelJSON:        string(panelJSONBytes),
			QuorumThreshold:          int(input.QuorumThreshold),
			QuorumVerifierCount:      int(input.QuorumVerifierCount),
			VerifierStakePerVerifier: input.VerifierStakePerVerifier.String(),
			Arbitrator:               input.Arbitrator,
			Amount:                   input.Amount.String(),
			WorkerStake:              input.WorkerStake.String(),
			Token:                    input.Token.Hex(),
			Status:                   escrowStatusPendingCreate,
			SubmissionDeadline:       input.SubmissionDeadlineDB,
			ReviewPeriodSeconds:      input.ReviewPeriodSecondsDB,
			DisputePeriodSeconds:     input.DisputePeriodSecondsDB,
			ArbitratorTimeoutSeconds: input.ArbitratorTimeoutSecondsDB,
			MilestoneCount:           milestoneCount,
			CurrentMilestone:         0,
			BackupWorker:             input.BackupWorker.Hex(),
			BackupDeadlineExtension:  input.BackupDeadlineDB,
			ActiveWorker:             input.Worker,
			ServiceTier:              int(input.ServiceTier),
			ZKVerifier:               input.ZKVerifier.Hex(),
			CircuitID:                fmt.Sprintf("0x%x", input.CircuitID),
			ParentEscrowID:           input.ParentEscrowID,
		})
		if txErr != nil {
			if errors.Is(txErr, storage.ErrDuplicateEscrowCreateIntent) {
				if rbErr := dbTx.Rollback(); rbErr != nil {
					return nil, fmt.Errorf("rollback duplicate create-intent transaction: %w", rbErr)
				}
				escrowRecord, err = s.DB.GetEscrowByCreateIntentID(ctx, createIntentID)
				if err != nil {
					return nil, fmt.Errorf("get escrow by create intent after duplicate: %w", err)
				}
			} else {
				return nil, fmt.Errorf("create escrow db record: %w", txErr)
			}
		} else if txErr := dbTx.Commit(); txErr != nil {
			return nil, fmt.Errorf("commit db transaction: %w", txErr)
		}
	}

	if escrowRecord.Status == escrowStatusCreateFinalized && common.IsHexAddress(escrowRecord.EscrowAddress) && escrowRecord.EscrowID > 0 {
		return &CreateEscrowResult{
			EscrowID:       escrowRecord.ID,
			TxHash:         escrowRecord.CreateTxHash,
			TaskID:         escrowRecord.TaskID,
			EscrowAddress:  escrowRecord.EscrowAddress,
			ChainEscrowID:  escrowRecord.EscrowID,
			MilestoneCount: escrowRecord.MilestoneCount,
		}, nil
	}

	txHash := strings.TrimSpace(escrowRecord.CreateTxHash)
	if txHash == "" {
		transitioned, transitionErr := s.DB.TransitionEscrowStatus(
			ctx,
			escrowRecord.ID,
			escrowStatusPendingCreate,
			escrowStatusSubmittingCreateTx,
		)
		if transitionErr != nil {
			return nil, fmt.Errorf("transition create status to submitting: %w", transitionErr)
		}
		if !transitioned {
			latest, getErr := s.DB.GetEscrow(ctx, escrowRecord.ID)
			if getErr != nil {
				return nil, fmt.Errorf("get escrow after status transition conflict: %w", getErr)
			}
			escrowRecord = latest
			txHash = strings.TrimSpace(escrowRecord.CreateTxHash)
			if txHash == "" {
				return nil, fmt.Errorf("escrow create request %s is already %s; retry after the in-flight attempt completes", createIntentID, escrowRecord.Status)
			}
		} else {
			chainTx, chainErr := s.Chain.CreateEscrow(ctx, factory, params)
			if chainErr != nil {
				if resetErr := s.DB.UpdateEscrowStatus(ctx, escrowRecord.ID, escrowStatusPendingCreate); resetErr != nil {
					slog.Warn("failed to reset pending escrow after createEscrow error", "escrow_id", escrowRecord.ID, "error", resetErr)
				}
				return nil, fmt.Errorf("chain create escrow: %w", chainErr)
			}
			txHash = chainTx.Hash().Hex()
			persistErr := s.DB.SetEscrowCreateTxHash(ctx, escrowRecord.ID, txHash, escrowStatusSubmittingCreateTx, escrowStatusPendingConfirmation)
			if persistErr != nil {
				const maxPersistAttempts = 3
				lastErr := persistErr
				for attempt := 2; attempt <= maxPersistAttempts; attempt++ {
					slog.Warn(
						"persist create tx hash failed; retrying",
						"escrow_id", escrowRecord.ID,
						"create_intent_id", createIntentID,
						"attempt", attempt-1,
						"max_attempts", maxPersistAttempts,
						"error", lastErr,
					)
					backoff := time.Duration(attempt-1) * 200 * time.Millisecond
					timer := time.NewTimer(backoff)
					select {
					case <-ctx.Done():
						timer.Stop()
						return nil, fmt.Errorf(
							"on-chain create submitted but tx-hash persistence failed (create_intent_id=%s, status=%s, tx_hash=%s): %w",
							createIntentID,
							escrowStatusSubmittingCreateTx,
							txHash,
							ctx.Err(),
						)
					case <-timer.C:
					}
					lastErr = s.DB.SetEscrowCreateTxHash(ctx, escrowRecord.ID, txHash, escrowStatusSubmittingCreateTx, escrowStatusPendingConfirmation)
					if lastErr == nil {
						break
					}
				}
				if lastErr != nil {
					status := escrowStatusSubmittingCreateTx
					latest, getErr := s.DB.GetEscrow(ctx, escrowRecord.ID)
					if getErr != nil {
						slog.Warn("failed to load escrow after tx-hash persistence failure", "escrow_id", escrowRecord.ID, "error", getErr)
					} else if strings.TrimSpace(latest.Status) != "" {
						status = latest.Status
					}
					return nil, fmt.Errorf(
						"on-chain create submitted but tx-hash persistence failed (create_intent_id=%s, status=%s, tx_hash=%s): %w",
						createIntentID,
						status,
						txHash,
						lastErr,
					)
				}
			}
		}
	}
	if txHash == "" {
		return nil, fmt.Errorf("escrow create request %s has no transaction hash to resume", createIntentID)
	}
	receiptResult, err := chain.WaitMinedAndParseEscrow(ctx, s.Chain, common.HexToHash(txHash))
	if err != nil {
		return nil, fmt.Errorf("wait escrow receipt: %w", err)
	}

	dbTx, err := s.DB.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin db transaction: %w", err)
	}
	defer dbTx.Rollback()

	err = s.DB.FinalizeEscrowCreateTx(
		ctx,
		dbTx,
		escrowRecord.ID,
		receiptResult.EscrowAddress.Hex(),
		receiptResult.EscrowID,
		escrowStatusCreateFinalized,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			latest, getErr := s.DB.GetEscrow(ctx, escrowRecord.ID)
			if getErr == nil &&
				latest.Status == escrowStatusCreateFinalized &&
				common.IsHexAddress(latest.EscrowAddress) &&
				latest.EscrowID > 0 {
				return &CreateEscrowResult{
					EscrowID:       latest.ID,
					TxHash:         strings.TrimSpace(latest.CreateTxHash),
					TaskID:         latest.TaskID,
					EscrowAddress:  latest.EscrowAddress,
					ChainEscrowID:  latest.EscrowID,
					MilestoneCount: latest.MilestoneCount,
				}, nil
			}
		}
		return nil, fmt.Errorf("finalize escrow create record: %w", err)
	}

	for i, milestone := range input.Milestones {
		_, err := s.DB.CreateMilestoneTx(ctx, dbTx, &storage.MilestoneRecord{
			EscrowID:           escrowRecord.ID,
			MilestoneIndex:     i,
			Amount:             milestone.Amount.String(),
			SubmissionDeadline: input.MilestoneDeadlines[i],
			Status:             "pending",
		})
		if err != nil {
			return nil, fmt.Errorf("create milestone %d: %w", i, err)
		}
	}

	if err := dbTx.Commit(); err != nil {
		return nil, fmt.Errorf("commit db transaction: %w", err)
	}

	s.runIndexerOnce(ctx)

	return &CreateEscrowResult{
		EscrowID:       escrowRecord.ID,
		TxHash:         txHash,
		TaskID:         escrowRecord.TaskID,
		EscrowAddress:  receiptResult.EscrowAddress.Hex(),
		ChainEscrowID:  receiptResult.EscrowID,
		MilestoneCount: milestoneCount,
	}, nil
}

func (s *Service) ResolveEscrowID(ctx context.Context, raw string) (*storage.Escrow, error) {
	if common.IsHexAddress(raw) {
		return s.DB.GetEscrowByAddress(ctx, common.HexToAddress(raw).Hex())
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid escrow_id: expected numeric id or 0x address, got %q", raw)
	}
	return s.DB.GetEscrow(ctx, id)
}

func IsERC20Token(token string) bool {
	return token != "" && token != zeroAddress
}

func NormalizeToken(token string) string {
	if token == "" || token == zeroAddress {
		return ""
	}
	return token
}

// parseAddress validates and converts a hex address string, returning ErrValidation if malformed or zero.
func parseAddress(field, raw string) (common.Address, error) {
	if !common.IsHexAddress(raw) {
		return common.Address{}, fmt.Errorf("%w: %s %q is not a valid hex address", ErrValidation, field, raw)
	}
	addr := common.HexToAddress(raw)
	if addr == (common.Address{}) {
		return common.Address{}, fmt.Errorf("%w: %s must not be the zero address", ErrValidation, field)
	}
	return addr, nil
}

func HasStake(escrow *storage.Escrow) bool {
	amt, ok := new(big.Int).SetString(escrow.WorkerStake, 10)
	return ok && amt.Sign() > 0
}

func ParseProofHashHex(raw string) ([32]byte, error) {
	var out [32]byte
	if raw == "" {
		return out, nil
	}
	if !strings.HasPrefix(raw, "0x") {
		return out, errors.New("expected 0x-prefixed hex")
	}
	normalized := raw[2:]
	if len(normalized) != 64 {
		return out, fmt.Errorf("expected 32-byte hex (64 chars), got %d", len(normalized))
	}
	b, err := hex.DecodeString(normalized)
	if err != nil {
		return out, err
	}
	copy(out[:], b)
	return out, nil
}

func ParseProofHexBytes(raw string) ([]byte, error) {
	if raw == "" {
		return nil, errors.New("proof is required")
	}
	if !strings.HasPrefix(raw, "0x") {
		return nil, errors.New("expected 0x-prefixed hex")
	}
	normalized := raw[2:]
	if len(normalized)%2 != 0 {
		return nil, errors.New("hex length must be even")
	}
	b, err := hex.DecodeString(normalized)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, errors.New("proof is empty")
	}
	return b, nil
}

func (s *Service) FundEscrow(ctx context.Context, escrow *storage.Escrow) (string, error) {
	amount, ok := new(big.Int).SetString(escrow.Amount, 10)
	if !ok {
		return "", fmt.Errorf("malformed escrow amount in database: %q", escrow.Amount)
	}
	escrowAddr, err := parseAddress("escrow_address", escrow.EscrowAddress)
	if err != nil {
		return "", err
	}
	if IsERC20Token(escrow.Token) {
		tokenAddr, err := parseAddress("token", escrow.Token)
		if err != nil {
			return "", err
		}
		approveTx, err := s.Chain.ApproveERC20(ctx, tokenAddr, escrowAddr, amount)
		if err != nil {
			return "", fmt.Errorf("approve erc20: %w", err)
		}
		approveReceipt, err := chain.WaitMined(ctx, s.Chain, approveTx.Hash())
		if err != nil {
			return "", fmt.Errorf("wait approve receipt: %w", err)
		}
		if approveReceipt.Status != 1 {
			return "", errors.New("approve transaction reverted")
		}
		tx, err := s.Chain.Fund(ctx, escrowAddr, nil)
		if err != nil {
			return "", fmt.Errorf("fund escrow: %w", err)
		}
		s.runIndexerOnce(ctx)
		return tx.Hash().Hex(), nil
	}

	tx, err := s.Chain.Fund(ctx, escrowAddr, amount)
	if err != nil {
		return "", fmt.Errorf("fund escrow: %w", err)
	}
	s.runIndexerOnce(ctx)
	return tx.Hash().Hex(), nil
}

func (s *Service) DepositWorkerStake(ctx context.Context, escrow *storage.Escrow) (string, error) {
	stakeAmount, ok := new(big.Int).SetString(escrow.WorkerStake, 10)
	if !ok || stakeAmount.Sign() <= 0 {
		return "", fmt.Errorf("%w: this escrow does not require a worker stake", ErrValidation)
	}
	escrowAddr, err := parseAddress("escrow_address", escrow.EscrowAddress)
	if err != nil {
		return "", err
	}
	return s.processStakeDeposit(ctx, escrowAddr, escrow.Token, stakeAmount, s.Chain.DepositStake)
}

func (s *Service) DepositVerifierStake(ctx context.Context, escrow *storage.Escrow) (string, error) {
	stakeAmount, ok := new(big.Int).SetString(escrow.VerifierStakePerVerifier, 10)
	if !ok || stakeAmount.Sign() <= 0 {
		return "", fmt.Errorf("%w: this escrow does not require verifier stake", ErrValidation)
	}
	escrowAddr, err := parseAddress("escrow_address", escrow.EscrowAddress)
	if err != nil {
		return "", err
	}
	return s.processStakeDeposit(ctx, escrowAddr, escrow.Token, stakeAmount, s.Chain.DepositVerifierStake)
}

func (s *Service) processStakeDeposit(
	ctx context.Context,
	escrowAddr common.Address,
	token string,
	stakeAmount *big.Int,
	deposit func(context.Context, common.Address, *big.Int) (*types.Transaction, error),
) (string, error) {
	if IsERC20Token(token) {
		tokenAddr, err := parseAddress("token", token)
		if err != nil {
			return "", err
		}
		approveTx, err := s.Chain.ApproveERC20(ctx, tokenAddr, escrowAddr, stakeAmount)
		if err != nil {
			return "", fmt.Errorf("approve erc20: %w", err)
		}
		approveReceipt, err := chain.WaitMined(ctx, s.Chain, approveTx.Hash())
		if err != nil {
			return "", fmt.Errorf("wait approve receipt: %w", err)
		}
		if approveReceipt.Status != 1 {
			return "", errors.New("approve transaction reverted")
		}
		tx, err := deposit(ctx, escrowAddr, nil)
		if err != nil {
			return "", fmt.Errorf("deposit stake: %w", err)
		}
		s.runIndexerOnce(ctx)
		return tx.Hash().Hex(), nil
	}

	tx, err := deposit(ctx, escrowAddr, stakeAmount)
	if err != nil {
		return "", fmt.Errorf("deposit stake: %w", err)
	}
	s.runIndexerOnce(ctx)
	return tx.Hash().Hex(), nil
}

func (s *Service) WithdrawStake(ctx context.Context, escrow *storage.Escrow) (string, error) {
	addr, err := parseAddress("escrow_address", escrow.EscrowAddress)
	if err != nil {
		return "", err
	}
	tx, err := s.Chain.WithdrawStake(ctx, addr)
	if err != nil {
		return "", fmt.Errorf("withdraw stake: %w", err)
	}
	s.runIndexerOnce(ctx)
	return tx.Hash().Hex(), nil
}

type SubmitRequest struct {
	SubmissionURI        string
	ProofHash            string
	MilestoneIndex       *int
	AttestationChainJSON string
}

func (s *Service) SubmitWork(ctx context.Context, escrow *storage.Escrow, req SubmitRequest) (string, error) {
	milestoneIndex, err := validateMilestoneIndex(escrow.MilestoneCount, req.MilestoneIndex)
	if err != nil {
		return "", err
	}
	proofHash, err := ParseProofHashHex(req.ProofHash)
	if err != nil {
		return "", fmt.Errorf("%w: invalid proof_hash: %w", ErrValidation, err)
	}
	hash := crypto.Keccak256Hash([]byte(req.SubmissionURI))
	var hashBytes [32]byte
	copy(hashBytes[:], hash.Bytes())
	validatedAttestationChain, err := s.validateAttestationChain(ctx, escrow.ID, req.AttestationChainJSON)
	if err != nil {
		return "", err
	}

	addr, err := parseAddress("escrow_address", escrow.EscrowAddress)
	if err != nil {
		return "", err
	}
	var tx *types.Transaction
	if milestoneIndex != nil {
		msIdxU8, convErr := numconv.IntToUint8(*milestoneIndex, "milestone_index")
		if convErr != nil {
			return "", convErr
		}
		tx, err = s.Chain.SubmitMilestone(ctx, addr, msIdxU8, hashBytes, req.SubmissionURI, proofHash)
		if err != nil {
			return "", fmt.Errorf("submit milestone: %w", err)
		}
	} else {
		tx, err = s.Chain.Submit(ctx, addr, hashBytes, req.SubmissionURI, proofHash)
		if err != nil {
			return "", fmt.Errorf("submit: %w", err)
		}
	}

	if validatedAttestationChain != nil {
		if err := s.persistAttestationChain(ctx, escrow.ID, milestoneIndex, validatedAttestationChain.validation, validatedAttestationChain.attestations); err != nil {
			return "", fmt.Errorf("persist attestation chain: %w", err)
		}
	}
	s.runIndexerOnce(ctx)
	return tx.Hash().Hex(), nil
}

func (s *Service) ApproveWork(ctx context.Context, escrow *storage.Escrow, role string, milestoneIndex *int) (string, error) {
	resolvedMilestone, err := validateMilestoneIndex(escrow.MilestoneCount, milestoneIndex)
	if err != nil {
		return "", err
	}

	addr, err := parseAddress("escrow_address", escrow.EscrowAddress)
	if err != nil {
		return "", err
	}
	if resolvedMilestone != nil {
		msIdxU8, convErr := numconv.IntToUint8(*resolvedMilestone, "milestone_index")
		if convErr != nil {
			return "", convErr
		}
		switch role {
		case "buyer":
			tx, err := s.Chain.ApproveMilestoneByBuyer(ctx, addr, msIdxU8)
			if err != nil {
				return "", fmt.Errorf("approve milestone by buyer: %w", err)
			}
			s.runIndexerOnce(ctx)
			return tx.Hash().Hex(), nil
		case "verifier":
			tx, err := s.Chain.CastMilestoneVerifierVote(ctx, addr, msIdxU8, true, "")
			if err != nil {
				return "", fmt.Errorf("approve milestone by verifier vote: %w", err)
			}
			s.runIndexerOnce(ctx)
			return tx.Hash().Hex(), nil
		default:
			return "", fmt.Errorf("%w: role must be 'buyer' or 'verifier'", ErrValidation)
		}
	}

	switch role {
	case "buyer":
		tx, err := s.Chain.ApproveByBuyer(ctx, addr)
		if err != nil {
			return "", fmt.Errorf("approve by buyer: %w", err)
		}
		s.runIndexerOnce(ctx)
		return tx.Hash().Hex(), nil
	case "verifier":
		tx, err := s.Chain.CastVerifierVote(ctx, addr, true, "")
		if err != nil {
			return "", fmt.Errorf("approve by verifier vote: %w", err)
		}
		s.runIndexerOnce(ctx)
		return tx.Hash().Hex(), nil
	default:
		return "", fmt.Errorf("%w: role must be 'buyer' or 'verifier'", ErrValidation)
	}
}

func (s *Service) VerifyAndApprove(ctx context.Context, escrow *storage.Escrow, proof []byte, milestoneIndex *int) (string, error) {
	resolvedMilestone, err := validateMilestoneIndex(escrow.MilestoneCount, milestoneIndex)
	if err != nil {
		return "", err
	}
	addr, err := parseAddress("escrow_address", escrow.EscrowAddress)
	if err != nil {
		return "", err
	}
	if resolvedMilestone != nil {
		msIdxU8, convErr := numconv.IntToUint8(*resolvedMilestone, "milestone_index")
		if convErr != nil {
			return "", convErr
		}
		tx, err := s.Chain.VerifyAndApproveMilestone(ctx, addr, msIdxU8, proof)
		if err != nil {
			return "", fmt.Errorf("verify and approve milestone: %w", err)
		}
		s.runIndexerOnce(ctx)
		return tx.Hash().Hex(), nil
	}
	tx, err := s.Chain.VerifyAndApprove(ctx, addr, proof)
	if err != nil {
		return "", fmt.Errorf("verify and approve: %w", err)
	}
	s.runIndexerOnce(ctx)
	return tx.Hash().Hex(), nil
}

func (s *Service) CastVerifierVote(ctx context.Context, escrow *storage.Escrow, approve bool, reasonURI string, milestoneIndex *int) (string, error) {
	resolvedMilestone, err := validateMilestoneIndex(escrow.MilestoneCount, milestoneIndex)
	if err != nil {
		return "", err
	}
	addr, err := parseAddress("escrow_address", escrow.EscrowAddress)
	if err != nil {
		return "", err
	}
	if resolvedMilestone != nil {
		msIdxU8, convErr := numconv.IntToUint8(*resolvedMilestone, "milestone_index")
		if convErr != nil {
			return "", convErr
		}
		tx, err := s.Chain.CastMilestoneVerifierVote(ctx, addr, msIdxU8, approve, reasonURI)
		if err != nil {
			return "", fmt.Errorf("cast milestone verifier vote: %w", err)
		}
		s.runIndexerOnce(ctx)
		return tx.Hash().Hex(), nil
	}

	tx, err := s.Chain.CastVerifierVote(ctx, addr, approve, reasonURI)
	if err != nil {
		return "", fmt.Errorf("cast verifier vote: %w", err)
	}
	s.runIndexerOnce(ctx)
	return tx.Hash().Hex(), nil
}

func (s *Service) DisputeWork(ctx context.Context, escrow *storage.Escrow, role, reasonURI string, milestoneIndex *int) (string, error) {
	resolvedMilestone, err := validateMilestoneIndex(escrow.MilestoneCount, milestoneIndex)
	if err != nil {
		return "", err
	}
	addr, err := parseAddress("escrow_address", escrow.EscrowAddress)
	if err != nil {
		return "", err
	}
	if resolvedMilestone != nil {
		msIdxU8, convErr := numconv.IntToUint8(*resolvedMilestone, "milestone_index")
		if convErr != nil {
			return "", convErr
		}
		switch role {
		case "buyer":
			tx, err := s.Chain.DisputeMilestone(ctx, addr, msIdxU8, reasonURI)
			if err != nil {
				return "", fmt.Errorf("dispute milestone: %w", err)
			}
			s.runIndexerOnce(ctx)
			return tx.Hash().Hex(), nil
		case "verifier":
			tx, err := s.Chain.CastMilestoneVerifierVote(ctx, addr, msIdxU8, false, reasonURI)
			if err != nil {
				return "", fmt.Errorf("reject milestone via verifier vote: %w", err)
			}
			s.runIndexerOnce(ctx)
			return tx.Hash().Hex(), nil
		case "worker":
			tx, err := s.Chain.EscalateMilestoneSilence(ctx, addr, msIdxU8, reasonURI)
			if err != nil {
				return "", fmt.Errorf("escalate milestone silence: %w", err)
			}
			s.runIndexerOnce(ctx)
			return tx.Hash().Hex(), nil
		default:
			return "", fmt.Errorf("%w: role must be 'buyer', 'verifier', or 'worker'", ErrValidation)
		}
	}

	switch role {
	case "buyer":
		tx, err := s.Chain.Dispute(ctx, addr, reasonURI)
		if err != nil {
			return "", fmt.Errorf("dispute: %w", err)
		}
		s.runIndexerOnce(ctx)
		return tx.Hash().Hex(), nil
	case "verifier":
		tx, err := s.Chain.CastVerifierVote(ctx, addr, false, reasonURI)
		if err != nil {
			return "", fmt.Errorf("reject via verifier vote: %w", err)
		}
		s.runIndexerOnce(ctx)
		return tx.Hash().Hex(), nil
	case "worker":
		tx, err := s.Chain.EscalateSilence(ctx, addr, reasonURI)
		if err != nil {
			return "", fmt.Errorf("escalate silence: %w", err)
		}
		s.runIndexerOnce(ctx)
		return tx.Hash().Hex(), nil
	default:
		return "", fmt.Errorf("%w: role must be 'buyer', 'verifier', or 'worker'", ErrValidation)
	}
}

func (s *Service) ResolveDispute(ctx context.Context, escrow *storage.Escrow, workerAwardBps uint16, resolutionURI string, milestoneIndex *int) (string, error) {
	if workerAwardBps > 10_000 {
		return "", fmt.Errorf("%w: worker_award_bps must be between 0 and 10000", ErrValidation)
	}
	resolvedMilestone, err := validateMilestoneIndex(escrow.MilestoneCount, milestoneIndex)
	if err != nil {
		return "", err
	}
	addr, err := parseAddress("escrow_address", escrow.EscrowAddress)
	if err != nil {
		return "", err
	}
	if resolvedMilestone != nil {
		msIdxU8, convErr := numconv.IntToUint8(*resolvedMilestone, "milestone_index")
		if convErr != nil {
			return "", convErr
		}
		tx, err := s.Chain.ResolveMilestoneDispute(ctx, addr, msIdxU8, workerAwardBps, resolutionURI)
		if err != nil {
			return "", fmt.Errorf("resolve milestone dispute: %w", err)
		}
		s.runIndexerOnce(ctx)
		return tx.Hash().Hex(), nil
	}

	tx, err := s.Chain.ResolveDispute(ctx, addr, workerAwardBps, resolutionURI)
	if err != nil {
		return "", fmt.Errorf("resolve dispute: %w", err)
	}
	s.runIndexerOnce(ctx)
	return tx.Hash().Hex(), nil
}

func (s *Service) ClaimTimeoutRefund(ctx context.Context, escrow *storage.Escrow, milestoneIndex *int) (string, error) {
	resolvedMilestone, err := validateOptionalMilestoneIndex(escrow.MilestoneCount, milestoneIndex)
	if err != nil {
		return "", err
	}
	addr, err := parseAddress("escrow_address", escrow.EscrowAddress)
	if err != nil {
		return "", err
	}
	if resolvedMilestone != nil {
		msIdxU8, convErr := numconv.IntToUint8(*resolvedMilestone, "milestone_index")
		if convErr != nil {
			return "", convErr
		}
		tx, err := s.Chain.ClaimMilestoneTimeoutRefund(ctx, addr, msIdxU8)
		if err != nil {
			return "", fmt.Errorf("claim milestone timeout refund: %w", err)
		}
		s.runIndexerOnce(ctx)
		return tx.Hash().Hex(), nil
	}
	tx, err := s.Chain.ClaimTimeoutRefund(ctx, addr)
	if err != nil {
		return "", fmt.Errorf("claim timeout refund: %w", err)
	}
	s.runIndexerOnce(ctx)
	return tx.Hash().Hex(), nil
}

func (s *Service) ClaimArbitratorTimeout(ctx context.Context, escrow *storage.Escrow, milestoneIndex *int) (string, error) {
	resolvedMilestone, err := validateOptionalMilestoneIndex(escrow.MilestoneCount, milestoneIndex)
	if err != nil {
		return "", err
	}
	addr, err := parseAddress("escrow_address", escrow.EscrowAddress)
	if err != nil {
		return "", err
	}
	if resolvedMilestone != nil {
		msIdxU8, convErr := numconv.IntToUint8(*resolvedMilestone, "milestone_index")
		if convErr != nil {
			return "", convErr
		}
		tx, err := s.Chain.ClaimMilestoneArbitratorTimeout(ctx, addr, msIdxU8)
		if err != nil {
			return "", fmt.Errorf("claim milestone arbitrator timeout: %w", err)
		}
		s.runIndexerOnce(ctx)
		return tx.Hash().Hex(), nil
	}
	tx, err := s.Chain.ClaimArbitratorTimeout(ctx, addr)
	if err != nil {
		return "", fmt.Errorf("claim arbitrator timeout: %w", err)
	}
	s.runIndexerOnce(ctx)
	return tx.Hash().Hex(), nil
}

func (s *Service) CancelBeforeFunding(ctx context.Context, escrow *storage.Escrow) (string, error) {
	addr, err := parseAddress("escrow_address", escrow.EscrowAddress)
	if err != nil {
		return "", err
	}
	tx, err := s.Chain.CancelBeforeFunding(ctx, addr)
	if err != nil {
		return "", fmt.Errorf("cancel before funding: %w", err)
	}
	s.runIndexerOnce(ctx)
	return tx.Hash().Hex(), nil
}

func (s *Service) AbortRemainingMilestones(ctx context.Context, escrow *storage.Escrow) (string, error) {
	if escrow.MilestoneCount <= 1 {
		return "", fmt.Errorf("%w: abort_remaining_milestones is only available for multi-milestone escrows", ErrValidation)
	}
	addr, err := parseAddress("escrow_address", escrow.EscrowAddress)
	if err != nil {
		return "", err
	}
	tx, err := s.Chain.AbortRemainingMilestones(ctx, addr)
	if err != nil {
		return "", fmt.Errorf("abort remaining milestones: %w", err)
	}
	s.runIndexerOnce(ctx)
	return tx.Hash().Hex(), nil
}

func (s *Service) ActivateBackup(ctx context.Context, escrow *storage.Escrow) (string, error) {
	if escrow.BackupWorker == "" || escrow.BackupWorker == zeroAddress {
		return "", fmt.Errorf("%w: this escrow has no backup worker designated", ErrValidation)
	}
	if escrow.BackupActivated {
		return "", fmt.Errorf("%w: backup already activated", ErrValidation)
	}
	addr, err := parseAddress("escrow_address", escrow.EscrowAddress)
	if err != nil {
		return "", err
	}
	tx, err := s.Chain.ActivateBackup(ctx, addr)
	if err != nil {
		return "", fmt.Errorf("activate backup: %w", err)
	}
	s.runIndexerOnce(ctx)
	return tx.Hash().Hex(), nil
}

func validateMilestoneIndex(milestoneCount int, milestoneIndex *int) (*int, error) {
	if milestoneCount > 1 {
		if milestoneIndex == nil {
			return nil, fmt.Errorf("%w: milestone_index required for multi-milestone escrow", ErrValidation)
		}
		if *milestoneIndex < 0 || *milestoneIndex >= milestoneCount {
			return nil, fmt.Errorf("%w: milestone_index %d out of range [0, %d)", ErrValidation, *milestoneIndex, milestoneCount)
		}
		return milestoneIndex, nil
	}
	if milestoneIndex != nil {
		return nil, fmt.Errorf("%w: milestone_index is not valid for single-milestone escrows", ErrValidation)
	}
	return nil, nil
}

func validateOptionalMilestoneIndex(milestoneCount int, milestoneIndex *int) (*int, error) {
	if milestoneIndex == nil {
		return nil, nil
	}
	if milestoneCount <= 1 {
		return nil, fmt.Errorf("%w: milestone_index is not valid for single-milestone escrows", ErrValidation)
	}
	if *milestoneIndex < 0 || *milestoneIndex >= milestoneCount {
		return nil, fmt.Errorf("%w: milestone_index %d out of range [0, %d)", ErrValidation, *milestoneIndex, milestoneCount)
	}
	return milestoneIndex, nil
}

type validatedAttestationChain struct {
	validation   attestation.ChainValidationResult
	attestations []attestation.CompletionAttestation
}

func (s *Service) validateAttestationChain(ctx context.Context, escrowID int64, chainJSON string) (*validatedAttestationChain, error) {
	childEscrows, err := s.DB.ListChildEscrows(ctx, escrowID)
	if err != nil {
		return nil, fmt.Errorf("failed to check child escrows: %w", err)
	}

	if len(childEscrows) > 0 {
		atts, parseErr := attestation.ParseCompletionAttestations(chainJSON)
		if parseErr != nil {
			return nil, fmt.Errorf("%w: invalid attestation_chain_json: %w", ErrValidation, parseErr)
		}
		if len(atts) == 0 {
			return nil, fmt.Errorf("%w: attestation_chain_json required when escrow has sub-delegated child escrows", ErrValidation)
		}
		childIDs := make([]int64, len(childEscrows))
		for i, child := range childEscrows {
			childIDs[i] = child.ID
		}
		validation := attestation.ValidateChain(atts, childIDs, time.Now())
		if !validation.Valid {
			return nil, fmt.Errorf("%w: attestation chain validation failed: %s", ErrValidation, strings.Join(validation.Reasons, "; "))
		}
		return &validatedAttestationChain{
			validation:   validation,
			attestations: atts,
		}, nil
	}

	if chainJSON != "" && chainJSON != "[]" {
		atts, parseErr := attestation.ParseCompletionAttestations(chainJSON)
		if parseErr != nil {
			return nil, fmt.Errorf("%w: invalid attestation_chain_json: %w", ErrValidation, parseErr)
		}
		if len(atts) > 0 {
			validation := attestation.ValidateChain(atts, nil, time.Now())
			if !validation.Valid {
				return nil, fmt.Errorf("%w: attestation chain validation failed: %s", ErrValidation, strings.Join(validation.Reasons, "; "))
			}
			return &validatedAttestationChain{
				validation:   validation,
				attestations: atts,
			}, nil
		}
	}
	return nil, nil
}

func (s *Service) persistAttestationChain(ctx context.Context, escrowID int64, milestoneIndex *int, validation attestation.ChainValidationResult, atts []attestation.CompletionAttestation) error {
	tx, err := s.DB.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("begin attestation tx: %w", err)
	}
	defer tx.Rollback()

	record, err := s.DB.CreateAttestationChainTx(ctx, tx, &storage.AttestationChain{
		EscrowID:                escrowID,
		MilestoneIndex:          milestoneIndex,
		RootHash:                validation.RootHash,
		Verified:                validation.Valid,
		VerificationSummaryJSON: attestation.MarshalChainValidationResult(validation),
	})
	if err != nil {
		return fmt.Errorf("create attestation chain: %w", err)
	}
	for _, att := range atts {
		_, err := s.DB.CreateAttestationLinkTx(ctx, tx, &storage.AttestationLink{
			ChainID:       record.ID,
			LinkID:        att.LinkID,
			ParentLinkID:  att.ParentLinkID,
			FromAddress:   att.FromAddress,
			ToAddress:     att.ToAddress,
			ChildEscrowID: att.ChildEscrowID,
			TaskSpecHash:  att.TaskSpecHash,
			OutcomeHash:   att.OutcomeHash,
			IssuedAt:      att.IssuedAt,
			ExpiresAt:     att.ExpiresAt,
			Nonce:         att.Nonce,
			Signature:     att.Signature,
		})
		if err != nil {
			return fmt.Errorf("create attestation link %s: %w", att.LinkID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit attestation tx: %w", err)
	}
	return nil
}

func (s *Service) runIndexerOnce(ctx context.Context) {
	if s.Idx != nil {
		if err := s.Idx.RunOnce(ctx); err != nil {
			slog.Warn("indexer RunOnce failed", "error", err)
		}
	}
}
