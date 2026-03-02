package escrow

import (
	"context"
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
	if !common.IsHexAddress(input.Buyer) {
		return nil, fmt.Errorf("%w: buyer %q is not a valid hex address", ErrValidation, input.Buyer)
	}
	if !common.IsHexAddress(input.Worker) {
		return nil, fmt.Errorf("%w: worker %q is not a valid hex address", ErrValidation, input.Worker)
	}
	if !common.IsHexAddress(input.Arbitrator) {
		return nil, fmt.Errorf("%w: arbitrator %q is not a valid hex address", ErrValidation, input.Arbitrator)
	}
	if !common.IsHexAddress(s.Cfg.FactoryAddress) {
		return nil, fmt.Errorf("%w: factory address %q is not a valid hex address", ErrValidation, s.Cfg.FactoryAddress)
	}
	if input.VerifierStakePerVerifier == nil {
		input.VerifierStakePerVerifier = big.NewInt(0)
	}
	if input.WorkerStake == nil {
		input.WorkerStake = big.NewInt(0)
	}

	var verifierPanel [7]common.Address
	panelForJSON := make([]string, int(input.QuorumVerifierCount))
	for i := 0; i < int(input.QuorumVerifierCount); i++ {
		if !common.IsHexAddress(input.VerifierPanel[i]) {
			return nil, fmt.Errorf("%w: verifier_panel[%d] %q is not a valid hex address", ErrValidation, i, input.VerifierPanel[i])
		}
		addr := common.HexToAddress(input.VerifierPanel[i])
		verifierPanel[i] = addr
		panelForJSON[i] = strings.ToLower(addr.Hex())
	}

	factory := common.HexToAddress(s.Cfg.FactoryAddress)
	params := chain.CreateEscrowParams{
		Buyer:                    common.HexToAddress(input.Buyer),
		Worker:                   common.HexToAddress(input.Worker),
		VerifierPanel:            verifierPanel,
		QuorumThreshold:          input.QuorumThreshold,
		QuorumVerifierCount:      input.QuorumVerifierCount,
		VerifierStakePerVerifier: input.VerifierStakePerVerifier,
		Arbitrator:               common.HexToAddress(input.Arbitrator),
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

	tx, err := s.Chain.CreateEscrow(ctx, factory, params)
	if err != nil {
		return nil, fmt.Errorf("chain create escrow: %w", err)
	}
	receiptResult, err := chain.WaitMinedAndParseEscrow(ctx, s.Chain, tx.Hash())
	if err != nil {
		return nil, fmt.Errorf("wait escrow receipt: %w", err)
	}

	task, err := s.DB.CreateTask(ctx, input.Title, input.Description, input.TaskSpecHash.Hex())
	if err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}

	milestoneCount := 1
	if len(input.Milestones) > 0 {
		milestoneCount = len(input.Milestones)
	}
	panelJSONBytes, err := json.Marshal(panelForJSON)
	if err != nil {
		return nil, fmt.Errorf("marshal verifier panel: %w", err)
	}

	escrowRecord, err := s.DB.CreateEscrow(ctx, &storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  s.Cfg.ChainID,
		FactoryAddress:           s.Cfg.FactoryAddress,
		EscrowAddress:            receiptResult.EscrowAddress.Hex(),
		EscrowID:                 receiptResult.EscrowID,
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
		Status:                   "created",
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
	if err != nil {
		return nil, fmt.Errorf("create escrow db record: %w", err)
	}

	for i, milestone := range input.Milestones {
		if milestone.Amount == nil {
			return nil, fmt.Errorf("milestone %d: amount is required", i)
		}
		if milestone.Amount.Sign() < 0 {
			return nil, fmt.Errorf("milestone %d: amount must not be negative", i)
		}
		_, err := s.DB.CreateMilestone(ctx, &storage.MilestoneRecord{
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

	s.runIndexerOnce(ctx)

	return &CreateEscrowResult{
		EscrowID:       escrowRecord.ID,
		TxHash:         tx.Hash().Hex(),
		TaskID:         task.ID,
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
	escrowAddr := common.HexToAddress(escrow.EscrowAddress)
	if IsERC20Token(escrow.Token) {
		tokenAddr := common.HexToAddress(escrow.Token)
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
		return "", errors.New("this escrow does not require a worker stake")
	}
	return s.processStakeDeposit(ctx, common.HexToAddress(escrow.EscrowAddress), escrow.Token, stakeAmount, s.Chain.DepositStake)
}

func (s *Service) DepositVerifierStake(ctx context.Context, escrow *storage.Escrow) (string, error) {
	stakeAmount, ok := new(big.Int).SetString(escrow.VerifierStakePerVerifier, 10)
	if !ok || stakeAmount.Sign() <= 0 {
		return "", errors.New("this escrow does not require verifier stake")
	}
	return s.processStakeDeposit(ctx, common.HexToAddress(escrow.EscrowAddress), escrow.Token, stakeAmount, s.Chain.DepositVerifierStake)
}

func (s *Service) processStakeDeposit(
	ctx context.Context,
	escrowAddr common.Address,
	token string,
	stakeAmount *big.Int,
	deposit func(context.Context, common.Address, *big.Int) (*types.Transaction, error),
) (string, error) {
	if IsERC20Token(token) {
		tokenAddr := common.HexToAddress(token)
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
	tx, err := s.Chain.WithdrawStake(ctx, common.HexToAddress(escrow.EscrowAddress))
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
	if err := s.validateAndPersistAttestationChain(ctx, escrow.ID, milestoneIndex, req.AttestationChainJSON); err != nil {
		return "", err
	}

	hash := crypto.Keccak256Hash([]byte(req.SubmissionURI))
	var hashBytes [32]byte
	copy(hashBytes[:], hash.Bytes())
	proofHash, err := ParseProofHashHex(req.ProofHash)
	if err != nil {
		return "", fmt.Errorf("invalid proof_hash: %w", err)
	}

	addr := common.HexToAddress(escrow.EscrowAddress)
	if milestoneIndex != nil {
		msIdxU8, convErr := numconv.IntToUint8(*milestoneIndex, "milestone_index")
		if convErr != nil {
			return "", convErr
		}
		tx, err := s.Chain.SubmitMilestone(ctx, addr, msIdxU8, hashBytes, req.SubmissionURI, proofHash)
		if err != nil {
			return "", fmt.Errorf("submit milestone: %w", err)
		}
		s.runIndexerOnce(ctx)
		return tx.Hash().Hex(), nil
	}

	tx, err := s.Chain.Submit(ctx, addr, hashBytes, req.SubmissionURI, proofHash)
	if err != nil {
		return "", fmt.Errorf("submit: %w", err)
	}
	s.runIndexerOnce(ctx)
	return tx.Hash().Hex(), nil
}

func (s *Service) ApproveWork(ctx context.Context, escrow *storage.Escrow, role string, milestoneIndex *int) (string, error) {
	resolvedMilestone, err := validateMilestoneIndex(escrow.MilestoneCount, milestoneIndex)
	if err != nil {
		return "", err
	}

	addr := common.HexToAddress(escrow.EscrowAddress)
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
			return "", errors.New("role must be 'buyer' or 'verifier'")
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
		return "", errors.New("role must be 'buyer' or 'verifier'")
	}
}

func (s *Service) VerifyAndApprove(ctx context.Context, escrow *storage.Escrow, proof []byte, milestoneIndex *int) (string, error) {
	resolvedMilestone, err := validateMilestoneIndex(escrow.MilestoneCount, milestoneIndex)
	if err != nil {
		return "", err
	}
	addr := common.HexToAddress(escrow.EscrowAddress)
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
	addr := common.HexToAddress(escrow.EscrowAddress)
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
	addr := common.HexToAddress(escrow.EscrowAddress)
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
			return "", errors.New("role must be 'buyer', 'verifier', or 'worker'")
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
		return "", errors.New("role must be 'buyer', 'verifier', or 'worker'")
	}
}

func (s *Service) ResolveDispute(ctx context.Context, escrow *storage.Escrow, workerAwardBps uint16, resolutionURI string, milestoneIndex *int) (string, error) {
	if workerAwardBps > 10_000 {
		return "", errors.New("worker_award_bps must be between 0 and 10000")
	}
	resolvedMilestone, err := validateMilestoneIndex(escrow.MilestoneCount, milestoneIndex)
	if err != nil {
		return "", err
	}
	addr := common.HexToAddress(escrow.EscrowAddress)
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
	addr := common.HexToAddress(escrow.EscrowAddress)
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
	addr := common.HexToAddress(escrow.EscrowAddress)
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
	tx, err := s.Chain.CancelBeforeFunding(ctx, common.HexToAddress(escrow.EscrowAddress))
	if err != nil {
		return "", fmt.Errorf("cancel before funding: %w", err)
	}
	s.runIndexerOnce(ctx)
	return tx.Hash().Hex(), nil
}

func (s *Service) AbortRemainingMilestones(ctx context.Context, escrow *storage.Escrow) (string, error) {
	if escrow.MilestoneCount <= 1 {
		return "", errors.New("abort_remaining_milestones is only available for multi-milestone escrows")
	}
	tx, err := s.Chain.AbortRemainingMilestones(ctx, common.HexToAddress(escrow.EscrowAddress))
	if err != nil {
		return "", fmt.Errorf("abort remaining milestones: %w", err)
	}
	s.runIndexerOnce(ctx)
	return tx.Hash().Hex(), nil
}

func (s *Service) ActivateBackup(ctx context.Context, escrow *storage.Escrow) (string, error) {
	if escrow.BackupWorker == "" || escrow.BackupWorker == zeroAddress {
		return "", errors.New("this escrow has no backup worker designated")
	}
	if escrow.BackupActivated {
		return "", errors.New("backup already activated")
	}
	tx, err := s.Chain.ActivateBackup(ctx, common.HexToAddress(escrow.EscrowAddress))
	if err != nil {
		return "", fmt.Errorf("activate backup: %w", err)
	}
	s.runIndexerOnce(ctx)
	return tx.Hash().Hex(), nil
}

func validateMilestoneIndex(milestoneCount int, milestoneIndex *int) (*int, error) {
	if milestoneCount > 1 {
		if milestoneIndex == nil {
			return nil, errors.New("milestone_index required for multi-milestone escrow")
		}
		if *milestoneIndex < 0 || *milestoneIndex >= milestoneCount {
			return nil, fmt.Errorf("milestone_index %d out of range [0, %d)", *milestoneIndex, milestoneCount)
		}
		return milestoneIndex, nil
	}
	if milestoneIndex != nil {
		return nil, errors.New("milestone_index is not valid for single-milestone escrows")
	}
	return nil, nil
}

func validateOptionalMilestoneIndex(milestoneCount int, milestoneIndex *int) (*int, error) {
	if milestoneIndex == nil {
		return nil, nil
	}
	if milestoneCount <= 1 {
		return nil, errors.New("milestone_index is not valid for single-milestone escrows")
	}
	if *milestoneIndex < 0 || *milestoneIndex >= milestoneCount {
		return nil, fmt.Errorf("milestone_index %d out of range [0, %d)", *milestoneIndex, milestoneCount)
	}
	return milestoneIndex, nil
}

func (s *Service) validateAndPersistAttestationChain(ctx context.Context, escrowID int64, milestoneIndex *int, chainJSON string) error {
	childEscrows, err := s.DB.ListChildEscrows(ctx, escrowID)
	if err != nil {
		return fmt.Errorf("failed to check child escrows: %w", err)
	}

	if len(childEscrows) > 0 {
		atts, parseErr := attestation.ParseCompletionAttestations(chainJSON)
		if parseErr != nil {
			return fmt.Errorf("invalid attestation_chain_json: %w", parseErr)
		}
		if len(atts) == 0 {
			return errors.New("attestation_chain_json required when escrow has sub-delegated child escrows")
		}
		childIDs := make([]int64, len(childEscrows))
		for i, child := range childEscrows {
			childIDs[i] = child.ID
		}
		validation := attestation.ValidateChain(atts, childIDs, time.Now())
		if !validation.Valid {
			return fmt.Errorf("attestation chain validation failed: %s", strings.Join(validation.Reasons, "; "))
		}
		return s.persistAttestationChain(ctx, escrowID, milestoneIndex, validation, atts)
	}

	if chainJSON != "" && chainJSON != "[]" {
		atts, parseErr := attestation.ParseCompletionAttestations(chainJSON)
		if parseErr != nil {
			return fmt.Errorf("invalid attestation_chain_json: %w", parseErr)
		}
		if len(atts) > 0 {
			validation := attestation.ValidateChain(atts, nil, time.Now())
			if !validation.Valid {
				return fmt.Errorf("attestation chain validation failed: %s", strings.Join(validation.Reasons, "; "))
			}
			return s.persistAttestationChain(ctx, escrowID, milestoneIndex, validation, atts)
		}
	}
	return nil
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
