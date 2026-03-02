package ucp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	escrowservice "github.com/eddiefleurent/agent-escrow/go-server/internal/escrow"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/numconv"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
)

var (
	ErrInvalidRequest      = errors.New("invalid ucp request")
	ErrIdempotencyConflict = errors.New("ucp idempotency conflict")
)

type Service struct {
	DB           *storage.DB
	Escrow       *escrowservice.Service
	ProviderName string
	ProviderURL  string
}

func NewService(db *storage.DB, escrowSvc *escrowservice.Service, providerName, providerURL string) *Service {
	return &Service{
		DB:           db,
		Escrow:       escrowSvc,
		ProviderName: providerName,
		ProviderURL:  providerURL,
	}
}

func ProjectStatus(escrowStatus string) CheckoutStatus {
	switch escrowStatus {
	case "settled":
		return CheckoutStatusCompleted
	case "cancelled", "refunded":
		return CheckoutStatusCanceled
	case "disputed":
		return CheckoutStatusRequiresEscalation
	case "submitted":
		return CheckoutStatusReadyForComplete
	case "approved", "resolved":
		return CheckoutStatusCompleteInProgress
	default:
		return CheckoutStatusIncomplete
	}
}

func (s *Service) BuildWellKnownProfile() WellKnownProfile {
	return WellKnownProfile{
		Version:      "1.0.0",
		ProviderName: s.ProviderName,
		ProviderURL:  s.ProviderURL,
		Operations: []string{
			"create_checkout",
			"get_checkout",
			"update_checkout",
			"complete_checkout",
			"cancel_checkout",
		},
		Endpoints: map[string]string{
			"create_checkout":   "/api/v1/ucp/checkouts",
			"get_checkout":      "/api/v1/ucp/checkouts/{checkout_id}",
			"update_checkout":   "/api/v1/ucp/checkouts/{checkout_id}",
			"complete_checkout": "/api/v1/ucp/checkouts/{checkout_id}/complete",
			"cancel_checkout":   "/api/v1/ucp/checkouts/{checkout_id}/cancel",
		},
		StatusMap: map[string]string{
			"incomplete":           "Escrow exists but does not yet satisfy completion conditions.",
			"requires_escalation":  "Escrow is disputed and requires explicit dispute resolution.",
			"ready_for_complete":   "Submission exists and is waiting for approval/verification action.",
			"complete_in_progress": "Settlement is in flight; poll for terminal state.",
			"completed":            "Escrow reached terminal settled path.",
			"canceled":             "Escrow reached terminal canceled/refunded path.",
		},
	}
}

func (s *Service) CreateCheckout(ctx context.Context, req CreateCheckoutRequest) (*Checkout, error) {
	return s.withIdempotency(ctx, req.IdempotencyKey, "create_checkout", req, func() (*Checkout, error) {
		if req.EscrowID == nil && req.CreateEscrow == nil {
			return nil, fmt.Errorf("%w: either escrow_id or create_escrow is required", ErrInvalidRequest)
		}

		checkoutID := strings.TrimSpace(req.CheckoutID)
		if checkoutID == "" {
			checkoutID = uuid.NewString()
		}
		sessionID := strings.TrimSpace(req.SessionID)
		if sessionID == "" {
			sessionID = uuid.NewString()
		}

		var escrowRec *storage.Escrow
		var err error
		if req.EscrowID != nil {
			escrowRec, err = s.DB.GetEscrow(ctx, *req.EscrowID)
			if err != nil {
				return nil, fmt.Errorf("get escrow: %w", err)
			}
		} else {
			escrowRec, err = s.createEscrowFromPayload(ctx, req.CreateEscrow)
			if err != nil {
				return nil, err
			}
		}

		projected := ProjectStatus(escrowRec.Status)
		requestHash := hashRequest("create_checkout", req)
		session, err := s.DB.CreateUCPSession(ctx, &storage.UCPSession{
			CheckoutID:      checkoutID,
			SessionID:       sessionID,
			EscrowID:        escrowRec.ID,
			UCPStatus:       string(projected),
			IdempotencyKey:  req.IdempotencyKey,
			LastOperation:   "create_checkout",
			LastRequestHash: requestHash,
			LastTxHash:      "",
		})
		if err != nil {
			return nil, fmt.Errorf("create ucp session: %w", err)
		}

		if req.AutoFund && escrowRec.Status == "created" {
			txHash, fundErr := s.Escrow.FundEscrow(ctx, escrowRec)
			if fundErr != nil {
				return nil, fmt.Errorf("auto fund escrow: %w", fundErr)
			}
			escrowRec, err = s.DB.GetEscrow(ctx, escrowRec.ID)
			if err != nil {
				return nil, fmt.Errorf("refresh escrow after auto fund: %w", err)
			}
			projected = ProjectStatus(escrowRec.Status)
			if err := s.DB.UpdateUCPSessionProjection(
				ctx,
				session.CheckoutID,
				string(projected),
				req.IdempotencyKey,
				"fund",
				hashRequest("update_checkout", UpdateCheckoutRequest{Operation: "fund"}),
				txHash,
			); err != nil {
				return nil, fmt.Errorf("update ucp session projection: %w", err)
			}
			session.LastOperation = "fund"
			session.LastTxHash = txHash
			session.UCPStatus = string(projected)
		}

		return composeCheckout(session, escrowRec), nil
	})
}

func (s *Service) GetCheckout(ctx context.Context, checkoutID string) (*Checkout, error) {
	session, err := s.DB.GetUCPSessionByCheckoutID(ctx, checkoutID)
	if err != nil {
		return nil, err
	}
	escrowRec, err := s.DB.GetEscrow(ctx, session.EscrowID)
	if err != nil {
		return nil, err
	}
	projected := ProjectStatus(escrowRec.Status)
	if session.UCPStatus != string(projected) {
		if err := s.DB.UpdateUCPSessionProjection(
			ctx,
			session.CheckoutID,
			string(projected),
			session.IdempotencyKey,
			session.LastOperation,
			session.LastRequestHash,
			session.LastTxHash,
		); err == nil {
			session.UCPStatus = string(projected)
		}
	}
	return composeCheckout(session, escrowRec), nil
}

func (s *Service) UpdateCheckout(ctx context.Context, checkoutID string, req UpdateCheckoutRequest) (*Checkout, error) {
	return s.withIdempotency(ctx, req.IdempotencyKey, "update_checkout", req, func() (*Checkout, error) {
		session, err := s.DB.GetUCPSessionByCheckoutID(ctx, checkoutID)
		if err != nil {
			return nil, err
		}
		escrowRec, err := s.DB.GetEscrow(ctx, session.EscrowID)
		if err != nil {
			return nil, err
		}

		op := strings.TrimSpace(req.Operation)
		if op == "" {
			return nil, fmt.Errorf("%w: operation is required", ErrInvalidRequest)
		}

		var txHash string
		switch op {
		case "fund":
			txHash, err = s.Escrow.FundEscrow(ctx, escrowRec)
		case "deposit_stake":
			txHash, err = s.Escrow.DepositWorkerStake(ctx, escrowRec)
		case "deposit_verifier_stake":
			txHash, err = s.Escrow.DepositVerifierStake(ctx, escrowRec)
		case "withdraw_stake":
			txHash, err = s.Escrow.WithdrawStake(ctx, escrowRec)
		case "submit":
			if strings.TrimSpace(req.SubmissionURI) == "" {
				return nil, fmt.Errorf("%w: submission_uri is required for submit", ErrInvalidRequest)
			}
			txHash, err = s.Escrow.SubmitWork(ctx, escrowRec, escrowservice.SubmitRequest{
				SubmissionURI:  req.SubmissionURI,
				ProofHash:      req.ProofHash,
				MilestoneIndex: req.MilestoneIndex,
			})
		case "approve":
			if strings.TrimSpace(req.Role) == "" {
				return nil, fmt.Errorf("%w: role is required for approve", ErrInvalidRequest)
			}
			txHash, err = s.Escrow.ApproveWork(ctx, escrowRec, req.Role, req.MilestoneIndex)
		case "verify_and_approve":
			proof, parseErr := escrowservice.ParseProofHexBytes(req.Proof)
			if parseErr != nil {
				return nil, fmt.Errorf("%w: invalid proof: %w", ErrInvalidRequest, parseErr)
			}
			txHash, err = s.Escrow.VerifyAndApprove(ctx, escrowRec, proof, req.MilestoneIndex)
		case "cast_verifier_vote":
			if req.Approve == nil {
				return nil, fmt.Errorf("%w: approve is required for cast_verifier_vote", ErrInvalidRequest)
			}
			txHash, err = s.Escrow.CastVerifierVote(ctx, escrowRec, *req.Approve, req.ReasonURI, req.MilestoneIndex)
		case "dispute":
			if strings.TrimSpace(req.Role) == "" {
				return nil, fmt.Errorf("%w: role is required for dispute", ErrInvalidRequest)
			}
			txHash, err = s.Escrow.DisputeWork(ctx, escrowRec, req.Role, req.ReasonURI, req.MilestoneIndex)
		case "resolve":
			if req.WorkerAwardBps == nil {
				return nil, fmt.Errorf("%w: worker_award_bps is required for resolve", ErrInvalidRequest)
			}
			txHash, err = s.Escrow.ResolveDispute(ctx, escrowRec, *req.WorkerAwardBps, req.ResolutionURI, req.MilestoneIndex)
		case "claim_timeout_refund":
			txHash, err = s.Escrow.ClaimTimeoutRefund(ctx, escrowRec, req.MilestoneIndex)
		case "claim_arbitrator_timeout":
			txHash, err = s.Escrow.ClaimArbitratorTimeout(ctx, escrowRec, req.MilestoneIndex)
		case "abort_remaining_milestones":
			txHash, err = s.Escrow.AbortRemainingMilestones(ctx, escrowRec)
		case "activate_backup":
			txHash, err = s.Escrow.ActivateBackup(ctx, escrowRec)
		default:
			return nil, fmt.Errorf("%w: unsupported operation %q", ErrInvalidRequest, op)
		}
		if err != nil {
			return nil, err
		}

		escrowRec, err = s.DB.GetEscrow(ctx, escrowRec.ID)
		if err != nil {
			return nil, err
		}
		projected := ProjectStatus(escrowRec.Status)
		requestHash := hashRequest("update_checkout", req)
		if err := s.DB.UpdateUCPSessionProjection(
			ctx,
			session.CheckoutID,
			string(projected),
			req.IdempotencyKey,
			op,
			requestHash,
			txHash,
		); err != nil {
			return nil, err
		}
		session.UCPStatus = string(projected)
		session.LastOperation = op
		session.LastRequestHash = requestHash
		session.LastTxHash = txHash
		session.IdempotencyKey = req.IdempotencyKey
		return composeCheckout(session, escrowRec), nil
	})
}

func (s *Service) CompleteCheckout(ctx context.Context, checkoutID string, req CompleteCheckoutRequest) (*Checkout, error) {
	return s.withIdempotency(ctx, req.IdempotencyKey, "complete_checkout", req, func() (*Checkout, error) {
		session, err := s.DB.GetUCPSessionByCheckoutID(ctx, checkoutID)
		if err != nil {
			return nil, err
		}
		escrowRec, err := s.DB.GetEscrow(ctx, session.EscrowID)
		if err != nil {
			return nil, err
		}

		projected := ProjectStatus(escrowRec.Status)
		txHash := ""
		lastOperation := "complete_checkout"

		switch projected {
		case CheckoutStatusCompleted, CheckoutStatusCanceled, CheckoutStatusRequiresEscalation:
			// Terminal/escalation states are read-only from complete_checkout.
		case CheckoutStatusReadyForComplete:
			if req.Proof != "" {
				proofBytes, parseErr := escrowservice.ParseProofHexBytes(req.Proof)
				if parseErr != nil {
					return nil, fmt.Errorf("%w: invalid proof: %w", ErrInvalidRequest, parseErr)
				}
				txHash, err = s.Escrow.VerifyAndApprove(ctx, escrowRec, proofBytes, req.MilestoneIndex)
				lastOperation = "verify_and_approve"
			} else if req.Role != "" {
				txHash, err = s.Escrow.ApproveWork(ctx, escrowRec, req.Role, req.MilestoneIndex)
				lastOperation = "approve"
			}
			if err != nil {
				return nil, err
			}
			escrowRec, err = s.DB.GetEscrow(ctx, escrowRec.ID)
			if err != nil {
				return nil, err
			}
			projected = ProjectStatus(escrowRec.Status)
		default:
			// incomplete / complete_in_progress: no-op (polling semantics).
		}

		requestHash := hashRequest("complete_checkout", req)
		if err := s.DB.UpdateUCPSessionProjection(
			ctx,
			session.CheckoutID,
			string(projected),
			req.IdempotencyKey,
			lastOperation,
			requestHash,
			txHash,
		); err != nil {
			return nil, err
		}
		session.UCPStatus = string(projected)
		session.LastOperation = lastOperation
		session.LastRequestHash = requestHash
		session.LastTxHash = txHash
		session.IdempotencyKey = req.IdempotencyKey
		return composeCheckout(session, escrowRec), nil
	})
}

func (s *Service) CancelCheckout(ctx context.Context, checkoutID string, req CancelCheckoutRequest) (*Checkout, error) {
	return s.withIdempotency(ctx, req.IdempotencyKey, "cancel_checkout", req, func() (*Checkout, error) {
		session, err := s.DB.GetUCPSessionByCheckoutID(ctx, checkoutID)
		if err != nil {
			return nil, err
		}
		escrowRec, err := s.DB.GetEscrow(ctx, session.EscrowID)
		if err != nil {
			return nil, err
		}
		txHash := ""
		lastOperation := "cancel_checkout"
		switch escrowRec.Status {
		case "settled":
			return nil, fmt.Errorf("%w: cannot cancel a settled checkout", ErrInvalidRequest)
		case "cancelled", "refunded":
			// Already in a canceled terminal path.
		case "created":
			txHash, err = s.Escrow.CancelBeforeFunding(ctx, escrowRec)
			lastOperation = "cancel_before_funding"
		case "disputed":
			txHash, err = s.Escrow.ClaimArbitratorTimeout(ctx, escrowRec, req.MilestoneIndex)
			lastOperation = "claim_arbitrator_timeout"
		default:
			txHash, err = s.Escrow.ClaimTimeoutRefund(ctx, escrowRec, req.MilestoneIndex)
			lastOperation = "claim_timeout_refund"
		}
		if err != nil {
			return nil, err
		}

		escrowRec, err = s.DB.GetEscrow(ctx, escrowRec.ID)
		if err != nil {
			return nil, err
		}
		projected := ProjectStatus(escrowRec.Status)
		requestHash := hashRequest("cancel_checkout", req)
		if err := s.DB.UpdateUCPSessionProjection(
			ctx,
			session.CheckoutID,
			string(projected),
			req.IdempotencyKey,
			lastOperation,
			requestHash,
			txHash,
		); err != nil {
			return nil, err
		}
		session.UCPStatus = string(projected)
		session.LastOperation = lastOperation
		session.LastRequestHash = requestHash
		session.LastTxHash = txHash
		session.IdempotencyKey = req.IdempotencyKey
		return composeCheckout(session, escrowRec), nil
	})
}

func composeCheckout(session *storage.UCPSession, escrowRec *storage.Escrow) *Checkout {
	return &Checkout{
		CheckoutID:    session.CheckoutID,
		SessionID:     session.SessionID,
		EscrowID:      session.EscrowID,
		UCPStatus:     CheckoutStatus(session.UCPStatus),
		EscrowStatus:  escrowRec.Status,
		LastOperation: session.LastOperation,
		LastTxHash:    session.LastTxHash,
		NextAction:    nextActionForEscrow(escrowRec),
		Escrow:        escrowRec,
	}
}

func nextActionForEscrow(escrowRec *storage.Escrow) string {
	switch escrowRec.Status {
	case "created":
		return "fund"
	case "funded":
		if escrowservice.HasStake(escrowRec) {
			return "deposit_stake"
		}
		return "submit"
	case "submitted":
		return "approve or dispute"
	case "approved", "resolved":
		return "wait for settlement indexing"
	case "disputed":
		return "resolve or timeout refund path"
	case "settled":
		return "completed"
	case "cancelled", "refunded":
		return "canceled"
	default:
		return "inspect escrow status"
	}
}

func (s *Service) withIdempotency(
	ctx context.Context,
	idempotencyKey string,
	operation string,
	request any,
	fn func() (*Checkout, error),
) (*Checkout, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return fn()
	}
	requestHash := hashRequest(operation, request)

	existing, err := s.DB.GetUCPIdempotency(ctx, idempotencyKey)
	if err == nil {
		if existing.Operation != operation || existing.RequestHash != requestHash {
			return nil, fmt.Errorf("%w: key %q was already used for a different request", ErrIdempotencyConflict, idempotencyKey)
		}
		var cached Checkout
		if unmarshalErr := json.Unmarshal([]byte(existing.ResponseJSON), &cached); unmarshalErr != nil {
			return nil, fmt.Errorf("decode cached idempotent response: %w", unmarshalErr)
		}
		return &cached, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	resp, err := fn()
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal idempotent response: %w", err)
	}
	if err := s.DB.CreateUCPIdempotency(ctx, &storage.UCPIdempotency{
		IdempotencyKey: idempotencyKey,
		Operation:      operation,
		RequestHash:    requestHash,
		ResponseJSON:   string(b),
		CheckoutID:     resp.CheckoutID,
	}); err != nil {
		return nil, err
	}
	return resp, nil
}

func hashRequest(operation string, request any) string {
	payload, err := json.Marshal(request)
	if err != nil {
		payload = []byte(fmt.Sprintf("%v", request))
	}
	sum := sha256.Sum256(append([]byte(operation+"|"), payload...))
	return hex.EncodeToString(sum[:])
}

func (s *Service) createEscrowFromPayload(ctx context.Context, payload *CreateEscrowPayload) (*storage.Escrow, error) {
	if payload == nil {
		return nil, fmt.Errorf("%w: create_escrow payload is required", ErrInvalidRequest)
	}
	if s.Escrow == nil || s.Escrow.Cfg == nil {
		return nil, errors.New("ucp service misconfigured: escrow service/config missing")
	}

	amount, ok := new(big.Int).SetString(payload.Amount, 10)
	if !ok {
		return nil, fmt.Errorf("%w: invalid amount", ErrInvalidRequest)
	}
	if err := chain.ValidateComplexityFloor(amount, s.Escrow.Cfg.ComplexityFloor); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}

	deadline, err := strconv.ParseUint(payload.SubmissionDeadline, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid submission_deadline", ErrInvalidRequest)
	}
	review, err := strconv.ParseUint(payload.ReviewPeriodSeconds, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid review_period_seconds", ErrInvalidRequest)
	}
	dispute, err := strconv.ParseUint(payload.DisputePeriodSeconds, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid dispute_period_seconds", ErrInvalidRequest)
	}
	arbTimeout, err := strconv.ParseUint(payload.ArbitratorTimeoutSeconds, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid arbitrator_timeout_seconds", ErrInvalidRequest)
	}

	if !isValidAddress(payload.Buyer) {
		return nil, fmt.Errorf("%w: invalid buyer address", ErrInvalidRequest)
	}
	if !isValidAddress(payload.Worker) {
		return nil, fmt.Errorf("%w: invalid worker address", ErrInvalidRequest)
	}
	if !isValidAddress(payload.Arbitrator) {
		return nil, fmt.Errorf("%w: invalid arbitrator address", ErrInvalidRequest)
	}

	if len(payload.VerifierPanel) == 0 || len(payload.VerifierPanel) > 7 {
		return nil, fmt.Errorf("%w: verifier_panel must include between 1 and 7 addresses", ErrInvalidRequest)
	}
	if payload.QuorumVerifierCount <= 0 || payload.QuorumVerifierCount > 7 {
		return nil, fmt.Errorf("%w: quorum_verifier_count must be between 1 and 7", ErrInvalidRequest)
	}
	if len(payload.VerifierPanel) < payload.QuorumVerifierCount {
		return nil, fmt.Errorf("%w: verifier_panel length must be at least quorum_verifier_count", ErrInvalidRequest)
	}
	if payload.QuorumThreshold <= 0 || payload.QuorumThreshold > payload.QuorumVerifierCount {
		return nil, fmt.Errorf("%w: quorum_threshold must be between 1 and quorum_verifier_count", ErrInvalidRequest)
	}

	buyerAddr := common.HexToAddress(payload.Buyer)
	workerAddr := common.HexToAddress(payload.Worker)
	arbitratorAddr := common.HexToAddress(payload.Arbitrator)
	panelForJSON := make([]string, payload.QuorumVerifierCount)
	seenVerifier := make(map[common.Address]bool, payload.QuorumVerifierCount)
	for i := 0; i < payload.QuorumVerifierCount; i++ {
		if !isValidAddress(payload.VerifierPanel[i]) {
			return nil, fmt.Errorf("%w: invalid verifier_panel[%d] address", ErrInvalidRequest, i)
		}
		addr := common.HexToAddress(payload.VerifierPanel[i])
		if seenVerifier[addr] {
			return nil, fmt.Errorf("%w: duplicate verifier_panel[%d] address", ErrInvalidRequest, i)
		}
		if addr == buyerAddr || addr == workerAddr || addr == arbitratorAddr {
			return nil, fmt.Errorf("%w: verifier_panel[%d] must not overlap buyer, worker, or arbitrator", ErrInvalidRequest, i)
		}
		seenVerifier[addr] = true
		panelForJSON[i] = strings.ToLower(addr.Hex())
	}

	workerStakeVal := big.NewInt(0)
	if payload.WorkerStake != "" {
		ws, ok := new(big.Int).SetString(payload.WorkerStake, 10)
		if !ok || ws.Sign() < 0 {
			return nil, fmt.Errorf("%w: invalid worker_stake", ErrInvalidRequest)
		}
		workerStakeVal = ws
	}
	verifierStakeVal := big.NewInt(0)
	if payload.VerifierStakePerVerifier != "" {
		vsv, ok := new(big.Int).SetString(payload.VerifierStakePerVerifier, 10)
		if !ok || vsv.Sign() < 0 {
			return nil, fmt.Errorf("%w: invalid verifier_stake_per_verifier", ErrInvalidRequest)
		}
		verifierStakeVal = vsv
	}

	var tokenAddr common.Address
	if payload.Token != "" {
		if !common.IsHexAddress(payload.Token) {
			return nil, fmt.Errorf("%w: invalid token address", ErrInvalidRequest)
		}
		tokenAddr = common.HexToAddress(payload.Token)
	}

	var milestones []chain.MilestoneParam
	msDeadlines := make([]int64, 0, len(payload.Milestones))
	for _, m := range payload.Milestones {
		msAmount, ok := new(big.Int).SetString(m.Amount, 10)
		if !ok {
			return nil, fmt.Errorf("%w: invalid milestone amount", ErrInvalidRequest)
		}
		msDeadline, err := strconv.ParseUint(m.SubmissionDeadline, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid milestone submission_deadline", ErrInvalidRequest)
		}
		msDeadlineI64, err := numconv.Uint64ToInt64(msDeadline, "milestones.submission_deadline")
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
		}
		milestones = append(milestones, chain.MilestoneParam{
			Amount:             msAmount,
			SubmissionDeadline: msDeadline,
		})
		msDeadlines = append(msDeadlines, msDeadlineI64)
	}

	var backupWorker common.Address
	if payload.BackupWorker != "" {
		if !isValidAddress(payload.BackupWorker) {
			return nil, fmt.Errorf("%w: invalid backup_worker address", ErrInvalidRequest)
		}
		backupWorker = common.HexToAddress(payload.BackupWorker)
	}
	var backupDeadline uint64
	if payload.BackupDeadlineExtension != "" {
		if backupWorker == (common.Address{}) {
			return nil, fmt.Errorf("%w: backup_deadline_extension set without backup_worker", ErrInvalidRequest)
		}
		bde, err := strconv.ParseUint(payload.BackupDeadlineExtension, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid backup_deadline_extension", ErrInvalidRequest)
		}
		backupDeadline = bde
	}

	var zkVerifier common.Address
	if payload.ZKVerifier != "" {
		if !common.IsHexAddress(payload.ZKVerifier) {
			return nil, fmt.Errorf("%w: invalid zk_verifier address", ErrInvalidRequest)
		}
		zkVerifier = common.HexToAddress(payload.ZKVerifier)
	}
	circuitID, err := escrowservice.ParseProofHashHex(payload.CircuitID)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid circuit_id: %w", ErrInvalidRequest, err)
	}
	if (zkVerifier == (common.Address{})) != (payload.CircuitID == "") {
		return nil, fmt.Errorf("%w: zk_verifier and circuit_id must either both be set or both omitted", ErrInvalidRequest)
	}
	if payload.ServiceTier < 0 || payload.ServiceTier > 1 {
		return nil, fmt.Errorf("%w: invalid service_tier: must be 0 or 1", ErrInvalidRequest)
	}
	serviceTier := uint8(payload.ServiceTier)

	var parentEscrowID *int64
	var parentEscrowAddr common.Address
	if payload.ParentEscrowID != nil {
		parent, err := s.DB.GetEscrow(ctx, *payload.ParentEscrowID)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid parent_escrow_id: %w", ErrInvalidRequest, err)
		}
		parsedParent := common.HexToAddress(parent.EscrowAddress)
		if !common.IsHexAddress(parent.EscrowAddress) || parsedParent == (common.Address{}) {
			return nil, fmt.Errorf("%w: parent escrow has invalid on-chain address", ErrInvalidRequest)
		}
		if parent.ChainID != s.Escrow.Cfg.ChainID || common.HexToAddress(parent.FactoryAddress) != common.HexToAddress(s.Escrow.Cfg.FactoryAddress) {
			return nil, fmt.Errorf("%w: parent escrow is from different chain/factory", ErrInvalidRequest)
		}
		activeWorker := parent.ActiveWorker
		if activeWorker == "" {
			activeWorker = parent.Worker
		}
		if !strings.EqualFold(common.HexToAddress(payload.Buyer).Hex(), common.HexToAddress(activeWorker).Hex()) {
			return nil, fmt.Errorf("%w: sub-delegation buyer must be active worker of parent escrow", ErrInvalidRequest)
		}
		parentEscrowAddr = parsedParent
		parentEscrowID = payload.ParentEscrowID
	}

	submissionDeadline, err := numconv.Uint64ToInt64(deadline, "submission_deadline")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	reviewPeriod, err := numconv.Uint64ToInt64(review, "review_period_seconds")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	disputePeriod, err := numconv.Uint64ToInt64(dispute, "dispute_period_seconds")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	arbitratorTimeout, err := numconv.Uint64ToInt64(arbTimeout, "arbitrator_timeout_seconds")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	backupDeadlineI64, err := numconv.Uint64ToInt64(backupDeadline, "backup_deadline_extension")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	quorumThreshold, err := numconv.IntToUint8(payload.QuorumThreshold, "quorum_threshold")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	quorumCount, err := numconv.IntToUint8(payload.QuorumVerifierCount, "quorum_verifier_count")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}

	result, err := s.Escrow.CreateEscrow(ctx, escrowservice.CreateEscrowInput{
		Title:       payload.Title,
		Description: payload.Description,

		Buyer:         payload.Buyer,
		Worker:        payload.Worker,
		Arbitrator:    payload.Arbitrator,
		VerifierPanel: panelForJSON,

		QuorumThreshold:          quorumThreshold,
		QuorumVerifierCount:      quorumCount,
		VerifierStakePerVerifier: verifierStakeVal,

		Amount:      amount,
		WorkerStake: workerStakeVal,

		SubmissionDeadline:       deadline,
		ReviewPeriodSeconds:      review,
		DisputePeriodSeconds:     dispute,
		ArbitratorTimeoutSeconds: arbTimeout,

		SubmissionDeadlineDB:       submissionDeadline,
		ReviewPeriodSecondsDB:      reviewPeriod,
		DisputePeriodSecondsDB:     disputePeriod,
		ArbitratorTimeoutSecondsDB: arbitratorTimeout,

		Token:       tokenAddr,
		ServiceTier: serviceTier,

		Milestones:         milestones,
		MilestoneDeadlines: msDeadlines,

		BackupWorker:            backupWorker,
		BackupDeadlineExtension: backupDeadline,
		BackupDeadlineDB:        backupDeadlineI64,

		ZKVerifier: zkVerifier,
		CircuitID:  circuitID,

		ParentEscrowID: parentEscrowID,
		ParentEscrow:   parentEscrowAddr,

		TaskSpecHash: crypto.Keccak256Hash([]byte(payload.Title + payload.Description)),
	})
	if err != nil {
		return nil, err
	}
	return s.DB.GetEscrow(ctx, result.EscrowID)
}

func isValidAddress(addr string) bool {
	return common.IsHexAddress(addr) && addr != "0x0000000000000000000000000000000000000000"
}
