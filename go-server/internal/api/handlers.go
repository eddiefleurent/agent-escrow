package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/attestation"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/bidding"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/numconv"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type Handlers struct {
	db    *storage.DB
	chain chain.ChainClient
	idx   *indexer.Indexer
	cfg   *config.Config
}

func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{"status": "ok"}

	if h.chain != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		blockNum, err := h.chain.BlockNumber(ctx)
		if err != nil {
			resp["status"] = "degraded"
			resp["chain"] = map[string]any{"error": err.Error()}
			writeJSON(w, http.StatusServiceUnavailable, resp)
			return
		}
		resp["chain"] = map[string]any{
			"block_number": blockNum,
			"chain_id":     h.chain.ChainID().Int64(),
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

type milestoneRequest struct {
	Amount             string `json:"amount"`
	SubmissionDeadline string `json:"submission_deadline"`
}

type createEscrowRequest struct {
	Title                    string             `json:"title"`
	Description              string             `json:"description"`
	Buyer                    string             `json:"buyer"`
	Worker                   string             `json:"worker"`
	Verifier                 string             `json:"verifier"`
	Arbitrator               string             `json:"arbitrator"`
	Amount                   string             `json:"amount"`
	WorkerStake              string             `json:"worker_stake,omitempty"`
	SubmissionDeadline       string             `json:"submission_deadline"`
	ReviewPeriodSeconds      string             `json:"review_period_seconds"`
	DisputePeriodSeconds     string             `json:"dispute_period_seconds"`
	ArbitratorTimeoutSeconds string             `json:"arbitrator_timeout_seconds"`
	Token                    string             `json:"token,omitempty"`
	Milestones               []milestoneRequest `json:"milestones,omitempty"`
	BackupWorker             string             `json:"backup_worker,omitempty"`
	BackupDeadlineExtension  string             `json:"backup_deadline_extension,omitempty"`
}

func (h *Handlers) CreateEscrow(w http.ResponseWriter, r *http.Request) {
	var req createEscrowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	amount, ok := new(big.Int).SetString(req.Amount, 10)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid amount"})
		return
	}
	if err := chain.ValidateComplexityFloor(amount, h.cfg.ComplexityFloor); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	deadline, err := strconv.ParseUint(req.SubmissionDeadline, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid submission_deadline"})
		return
	}
	review, err := strconv.ParseUint(req.ReviewPeriodSeconds, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid review_period_seconds"})
		return
	}
	dispute, err := strconv.ParseUint(req.DisputePeriodSeconds, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid dispute_period_seconds"})
		return
	}
	arbTimeout, err := strconv.ParseUint(req.ArbitratorTimeoutSeconds, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid arbitrator_timeout_seconds"})
		return
	}

	specHash := crypto.Keccak256Hash([]byte(req.Title + req.Description))

	for _, pair := range []struct {
		name, addr string
	}{
		{"buyer", req.Buyer},
		{"worker", req.Worker},
		{"verifier", req.Verifier},
		{"arbitrator", req.Arbitrator},
	} {
		if !isValidAddress(pair.addr) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid %s address", pair.name)})
			return
		}
	}

	workerStakeVal := big.NewInt(0)
	if req.WorkerStake != "" {
		ws, ok := new(big.Int).SetString(req.WorkerStake, 10)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid worker_stake"})
			return
		}
		workerStakeVal = ws
	}

	var tokenAddr common.Address
	if req.Token != "" {
		if !common.IsHexAddress(req.Token) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid token address"})
			return
		}
		tokenAddr = common.HexToAddress(req.Token)
	}

	var milestones []chain.MilestoneParam
	for _, m := range req.Milestones {
		msAmount, ok := new(big.Int).SetString(m.Amount, 10)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid milestone amount"})
			return
		}
		msDeadline, err := strconv.ParseUint(m.SubmissionDeadline, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid milestone submission_deadline"})
			return
		}
		milestones = append(milestones, chain.MilestoneParam{
			Amount:             msAmount,
			SubmissionDeadline: msDeadline,
		})
	}

	var backupWorkerAddr common.Address
	if req.BackupWorker != "" {
		if !isValidAddress(req.BackupWorker) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid backup_worker address"})
			return
		}
		backupWorkerAddr = common.HexToAddress(req.BackupWorker)
	}
	var backupDeadlineExt uint64
	if req.BackupDeadlineExtension != "" {
		if backupWorkerAddr == (common.Address{}) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "backup_deadline_extension set without backup_worker"})
			return
		}
		bde, err := strconv.ParseUint(req.BackupDeadlineExtension, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid backup_deadline_extension"})
			return
		}
		backupDeadlineExt = bde
	}

	// Validate all uint64→int64 conversions before any on-chain or DB side effects.
	submissionDeadline, err := numconv.Uint64ToInt64(deadline, "submission_deadline")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	reviewPeriod, err := numconv.Uint64ToInt64(review, "review_period_seconds")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	disputePeriod, err := numconv.Uint64ToInt64(dispute, "dispute_period_seconds")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	arbitratorTimeout, err := numconv.Uint64ToInt64(arbTimeout, "arbitrator_timeout_seconds")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	backupDeadline, err := numconv.Uint64ToInt64(backupDeadlineExt, "backup_deadline_extension")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	msDeadlinesInt64 := make([]int64, len(milestones))
	for i, m := range milestones {
		msDeadlinesInt64[i], err = numconv.Uint64ToInt64(m.SubmissionDeadline, fmt.Sprintf("milestones[%d].submission_deadline", i))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}

	factory := common.HexToAddress(h.cfg.FactoryAddress)
	params := chain.CreateEscrowParams{
		Buyer:                    common.HexToAddress(req.Buyer),
		Worker:                   common.HexToAddress(req.Worker),
		Verifier:                 common.HexToAddress(req.Verifier),
		Arbitrator:               common.HexToAddress(req.Arbitrator),
		Amount:                   amount,
		WorkerStake:              workerStakeVal,
		SubmissionDeadline:       deadline,
		ReviewPeriodSeconds:      review,
		DisputePeriodSeconds:     dispute,
		TaskSpecHash:             specHash,
		ArbitratorTimeoutSeconds: arbTimeout,
		Token:                    tokenAddr,
		Milestones:               milestones,
		BackupWorker:             backupWorkerAddr,
		BackupDeadlineExtension:  backupDeadlineExt,
	}

	tx, err := h.chain.CreateEscrow(r.Context(), factory, params)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
		return
	}

	result, err := chain.WaitMinedAndParseEscrow(r.Context(), h.chain, tx.Hash())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("receipt: %v", err)})
		return
	}

	task, err := h.db.CreateTask(r.Context(), req.Title, req.Description, specHash.Hex())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("db: %v", err)})
		return
	}

	milestoneCount := 1
	if len(milestones) > 0 {
		milestoneCount = len(milestones)
	}

	escrow, err := h.db.CreateEscrow(r.Context(), &storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  h.cfg.ChainID,
		FactoryAddress:           h.cfg.FactoryAddress,
		EscrowAddress:            result.EscrowAddress.Hex(),
		EscrowID:                 result.EscrowID,
		Buyer:                    req.Buyer,
		Worker:                   req.Worker,
		Verifier:                 req.Verifier,
		Arbitrator:               req.Arbitrator,
		Amount:                   req.Amount,
		WorkerStake:              workerStakeVal.String(),
		Token:                    tokenAddr.Hex(),
		Status:                   "created",
		SubmissionDeadline:       submissionDeadline,
		ReviewPeriodSeconds:      reviewPeriod,
		DisputePeriodSeconds:     disputePeriod,
		ArbitratorTimeoutSeconds: arbitratorTimeout,
		MilestoneCount:           milestoneCount,
		CurrentMilestone:         0,
		BackupWorker:             backupWorkerAddr.Hex(),
		BackupDeadlineExtension:  backupDeadline,
		ActiveWorker:             req.Worker,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("db: %v", err)})
		return
	}

	for i, m := range milestones {
		_, err := h.db.CreateMilestone(r.Context(), &storage.MilestoneRecord{
			EscrowID:           escrow.ID,
			MilestoneIndex:     i,
			Amount:             m.Amount.String(),
			SubmissionDeadline: msDeadlinesInt64[i],
			Status:             "pending",
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("db milestone %d: %v", i, err)})
			return
		}
	}

	_ = h.idx.RunOnce(r.Context())

	writeJSON(w, http.StatusCreated, map[string]any{
		"escrow_id":       escrow.ID,
		"task_id":         task.ID,
		"tx_hash":         tx.Hash().Hex(),
		"escrow_address":  result.EscrowAddress.Hex(),
		"chain_escrow_id": result.EscrowID,
		"milestone_count": milestoneCount,
	})
}

func (h *Handlers) GetEscrow(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	escrow, err := h.db.GetEscrow(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	result := map[string]any{"escrow": escrow}

	if escrow.MilestoneCount > 1 {
		milestones, err := h.db.GetMilestonesByEscrow(r.Context(), id)
		if err != nil {
			slog.Error("failed to fetch milestones", "escrow_id", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch milestones"})
			return
		}
		result["milestones"] = milestones
	}

	// Include attestation chain summary if present.
	chains, chainErr := h.db.GetAttestationChainsByEscrow(r.Context(), id)
	if chainErr != nil {
		slog.Error("failed to fetch optional attestation_chains for escrow response", "escrow_id", id, "result_key", "attestation_chains", "error", chainErr)
	} else if len(chains) > 0 {
		result["attestation_chains"] = chains
	}

	// Include child escrow count for delegation visibility.
	children, childErr := h.db.ListChildEscrows(r.Context(), id)
	if childErr != nil {
		slog.Error("failed to fetch optional child_escrow_ids for escrow response", "escrow_id", id, "result_key", "child_escrow_ids", "error", childErr)
	} else if len(children) > 0 {
		childIDs := make([]int64, len(children))
		for i, c := range children {
			childIDs[i] = c.ID
		}
		result["child_escrow_ids"] = childIDs
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *Handlers) GetAttestationChain(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	if _, err := h.db.GetEscrow(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		} else {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch escrow"})
		}
		return
	}

	chains, err := h.db.GetAttestationChainsByEscrow(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch attestation chains"})
		return
	}

	type chainWithLinks struct {
		Chain *storage.AttestationChain  `json:"chain"`
		Links []*storage.AttestationLink `json:"links"`
	}

	var out []chainWithLinks
	for _, ac := range chains {
		links, linkErr := h.db.GetAttestationLinksByChain(r.Context(), ac.ID)
		if linkErr != nil {
			slog.Error("failed to fetch attestation links", "chain_id", ac.ID, "error", linkErr)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch attestation links"})
			return
		}
		out = append(out, chainWithLinks{Chain: ac, Links: links})
	}

	writeJSON(w, http.StatusOK, map[string]any{"escrow_id": id, "attestation_chains": out})
}

func (h *Handlers) ListChildEscrows(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	if _, err := h.db.GetEscrow(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		} else {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch escrow"})
		}
		return
	}

	children, err := h.db.ListChildEscrows(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list child escrows"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"parent_escrow_id": id, "children": children})
}

func (h *Handlers) ListEscrows(w http.ResponseWriter, r *http.Request) {
	role := r.URL.Query().Get("role")
	address := r.URL.Query().Get("address")
	status := r.URL.Query().Get("status")

	escrows, err := h.db.ListEscrows(r.Context(), role, address, status)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, escrows)
}

func (h *Handlers) FundEscrow(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	escrow, err := h.db.GetEscrow(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	amount, ok := new(big.Int).SetString(escrow.Amount, 10)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "malformed escrow amount in database"})
		return
	}

	escrowAddr := common.HexToAddress(escrow.EscrowAddress)
	isERC20 := escrow.Token != "" && escrow.Token != "0x0000000000000000000000000000000000000000"

	if isERC20 {
		tokenAddr := common.HexToAddress(escrow.Token)
		approveTx, err := h.chain.ApproveERC20(r.Context(), tokenAddr, escrowAddr, amount)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("approve: %v", err)})
			return
		}
		approveReceipt, err := chain.WaitMined(r.Context(), h.chain, approveTx.Hash())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("approve receipt: %v", err)})
			return
		}
		if approveReceipt.Status != 1 {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "approve transaction reverted"})
			return
		}
		tx, err := h.chain.Fund(r.Context(), escrowAddr, nil)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
			return
		}
		_ = h.idx.RunOnce(r.Context())
		writeJSON(w, http.StatusOK, map[string]string{"tx_hash": tx.Hash().Hex()})
		return
	}

	tx, err := h.chain.Fund(r.Context(), escrowAddr, amount)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
		return
	}

	_ = h.idx.RunOnce(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": tx.Hash().Hex()})
}

func (h *Handlers) DepositStake(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	escrow, err := h.db.GetEscrow(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	stakeAmount, ok := new(big.Int).SetString(escrow.WorkerStake, 10)
	if !ok || stakeAmount.Sign() <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "this escrow does not require a worker stake"})
		return
	}

	escrowAddr := common.HexToAddress(escrow.EscrowAddress)
	isERC20 := escrow.Token != "" && escrow.Token != "0x0000000000000000000000000000000000000000"

	if isERC20 {
		tokenAddr := common.HexToAddress(escrow.Token)
		approveTx, err := h.chain.ApproveERC20(r.Context(), tokenAddr, escrowAddr, stakeAmount)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("approve: %v", err)})
			return
		}
		approveReceipt, err := chain.WaitMined(r.Context(), h.chain, approveTx.Hash())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("approve receipt: %v", err)})
			return
		}
		if approveReceipt.Status != 1 {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "approve transaction reverted"})
			return
		}
		tx, err := h.chain.DepositStake(r.Context(), escrowAddr, nil)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
			return
		}
		_ = h.idx.RunOnce(r.Context())
		writeJSON(w, http.StatusOK, map[string]string{"tx_hash": tx.Hash().Hex()})
		return
	}

	tx, err := h.chain.DepositStake(r.Context(), escrowAddr, stakeAmount)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
		return
	}

	_ = h.idx.RunOnce(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": tx.Hash().Hex()})
}

type submitRequest struct {
	SubmissionURI        string `json:"submission_uri"`
	MilestoneIndex       *int   `json:"milestone_index,omitempty"`
	AttestationChainJSON string `json:"attestation_chain_json,omitempty"`
}

// persistAttestationChain persists an attestation chain and its links inside a single
// transaction. It writes an appropriate HTTP error response and returns true if an
// error occurred; the caller should return immediately in that case.
func (h *Handlers) persistAttestationChain(ctx context.Context, w http.ResponseWriter, escrowID int64, milestoneIndex *int, chainResult attestation.ChainValidationResult, atts []attestation.CompletionAttestation) bool {
	tx, txErr := h.db.BeginTx(ctx)
	if txErr != nil {
		slog.Error("failed to begin attestation persistence transaction", "escrow_id", escrowID, "error", txErr)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist attestation chain"})
		return true
	}
	defer tx.Rollback()

	acRecord, acErr := h.db.CreateAttestationChainTx(ctx, tx, &storage.AttestationChain{
		EscrowID:                escrowID,
		MilestoneIndex:          milestoneIndex,
		RootHash:                chainResult.RootHash,
		Verified:                chainResult.Valid,
		VerificationSummaryJSON: attestation.MarshalChainValidationResult(chainResult),
	})
	if acErr != nil {
		slog.Error("failed to persist attestation chain", "escrow_id", escrowID, "error", acErr)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist attestation chain"})
		return true
	}
	for _, att := range atts {
		_, linkErr := h.db.CreateAttestationLinkTx(ctx, tx, &storage.AttestationLink{
			ChainID:       acRecord.ID,
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
		if linkErr != nil {
			slog.Error("failed to persist attestation link", "chain_id", acRecord.ID, "link_id", att.LinkID, "error", linkErr)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist attestation chain"})
			return true
		}
	}
	if commitErr := tx.Commit(); commitErr != nil {
		slog.Error("failed to commit attestation persistence transaction", "escrow_id", escrowID, "chain_id", acRecord.ID, "error", commitErr)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist attestation chain"})
		return true
	}
	return false
}

func (h *Handlers) SubmitWork(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	var req submitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	escrow, err := h.db.GetEscrow(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	if escrow.MilestoneCount > 1 {
		if req.MilestoneIndex == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "milestone_index required for multi-milestone escrow"})
			return
		}
		msIdx := *req.MilestoneIndex
		if msIdx < 0 || msIdx >= escrow.MilestoneCount {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("milestone_index %d out of range [0, %d)", msIdx, escrow.MilestoneCount)})
			return
		}
	}

	// Attestation chain validation for sub-delegation (paper §4.8).
	childEscrows, childErr := h.db.ListChildEscrows(r.Context(), id)
	if childErr != nil {
		slog.Error("failed to fetch child escrows", "escrow_id", id, "error", childErr)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to check child escrows"})
		return
	}
	if len(childEscrows) > 0 {
		atts, parseErr := attestation.ParseCompletionAttestations(req.AttestationChainJSON)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid attestation_chain_json: %v", parseErr)})
			return
		}
		if len(atts) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "attestation_chain_json required when escrow has sub-delegated child escrows"})
			return
		}
		childIDs := make([]int64, len(childEscrows))
		for i, ce := range childEscrows {
			childIDs[i] = ce.ID
		}
		chainResult := attestation.ValidateChain(atts, childIDs, time.Now())
		if !chainResult.Valid {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error":   "attestation chain validation failed",
				"reasons": strings.Join(chainResult.Reasons, "; "),
			})
			return
		}
		if h.persistAttestationChain(r.Context(), w, id, req.MilestoneIndex, chainResult, atts) {
			return
		}
	} else if req.AttestationChainJSON != "" && req.AttestationChainJSON != "[]" {
		// No child escrows but chain provided -- store it anyway for provenance.
		atts, parseErr := attestation.ParseCompletionAttestations(req.AttestationChainJSON)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid attestation_chain_json: %v", parseErr)})
			return
		}
		if len(atts) > 0 {
			chainResult := attestation.ValidateChain(atts, nil, time.Now())
			if !chainResult.Valid {
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error":   "attestation chain validation failed",
					"reasons": strings.Join(chainResult.Reasons, "; "),
				})
				return
			}
			if h.persistAttestationChain(r.Context(), w, id, req.MilestoneIndex, chainResult, atts) {
				return
			}
		}
	}

	hash := crypto.Keccak256Hash([]byte(req.SubmissionURI))
	var hashBytes [32]byte
	copy(hashBytes[:], hash.Bytes())

	addr := common.HexToAddress(escrow.EscrowAddress)

	if escrow.MilestoneCount > 1 {
		msIdx := *req.MilestoneIndex
		msIdxU8, convErr := numconv.IntToUint8(msIdx, "milestone_index")
		if convErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": convErr.Error()})
			return
		}
		tx, err := h.chain.SubmitMilestone(r.Context(), addr, msIdxU8, hashBytes, req.SubmissionURI)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
			return
		}
		_ = h.idx.RunOnce(r.Context())
		writeJSON(w, http.StatusOK, map[string]string{"tx_hash": tx.Hash().Hex()})
		return
	}

	tx, err := h.chain.Submit(r.Context(), addr, hashBytes, req.SubmissionURI)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
		return
	}

	_ = h.idx.RunOnce(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": tx.Hash().Hex()})
}

type approveRequest struct {
	Role           string `json:"role"`
	MilestoneIndex *int   `json:"milestone_index,omitempty"`
}

func (h *Handlers) ApproveWork(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	var req approveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	escrow, err := h.db.GetEscrow(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	addr := common.HexToAddress(escrow.EscrowAddress)

	if escrow.MilestoneCount > 1 {
		if req.MilestoneIndex == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "milestone_index required for multi-milestone escrow"})
			return
		}
		msIdxVal := *req.MilestoneIndex
		if msIdxVal < 0 || msIdxVal >= escrow.MilestoneCount {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("milestone_index %d out of range [0, %d)", msIdxVal, escrow.MilestoneCount)})
			return
		}
		msIdx, convErr := numconv.IntToUint8(msIdxVal, "milestone_index")
		if convErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": convErr.Error()})
			return
		}
		var txHash string
		switch req.Role {
		case "buyer":
			tx, err := h.chain.ApproveMilestoneByBuyer(r.Context(), addr, msIdx)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
				return
			}
			txHash = tx.Hash().Hex()
		case "verifier":
			tx, err := h.chain.ApproveMilestoneByVerifier(r.Context(), addr, msIdx)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
				return
			}
			txHash = tx.Hash().Hex()
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be 'buyer' or 'verifier'"})
			return
		}
		_ = h.idx.RunOnce(r.Context())
		writeJSON(w, http.StatusOK, map[string]string{"tx_hash": txHash})
		return
	}

	var txHash string
	switch req.Role {
	case "buyer":
		tx, err := h.chain.ApproveByBuyer(r.Context(), addr)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
			return
		}
		txHash = tx.Hash().Hex()
	case "verifier":
		tx, err := h.chain.ApproveByVerifier(r.Context(), addr)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
			return
		}
		txHash = tx.Hash().Hex()
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be 'buyer' or 'verifier'"})
		return
	}

	_ = h.idx.RunOnce(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": txHash})
}

type disputeRequest struct {
	Role           string `json:"role"`
	ReasonURI      string `json:"reason_uri"`
	MilestoneIndex *int   `json:"milestone_index,omitempty"`
}

func (h *Handlers) DisputeWork(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	var req disputeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	escrow, err := h.db.GetEscrow(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	addr := common.HexToAddress(escrow.EscrowAddress)

	if escrow.MilestoneCount > 1 {
		if req.MilestoneIndex == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "milestone_index required for multi-milestone escrow"})
			return
		}
		msIdxVal := *req.MilestoneIndex
		if msIdxVal < 0 || msIdxVal >= escrow.MilestoneCount {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("milestone_index %d out of range [0, %d)", msIdxVal, escrow.MilestoneCount)})
			return
		}
		msIdx, convErr := numconv.IntToUint8(msIdxVal, "milestone_index")
		if convErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": convErr.Error()})
			return
		}
		var txHash string
		switch req.Role {
		case "buyer":
			tx, err := h.chain.DisputeMilestone(r.Context(), addr, msIdx, req.ReasonURI)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
				return
			}
			txHash = tx.Hash().Hex()
		case "verifier":
			tx, err := h.chain.RejectMilestoneByVerifier(r.Context(), addr, msIdx, req.ReasonURI)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
				return
			}
			txHash = tx.Hash().Hex()
		case "worker":
			tx, err := h.chain.EscalateMilestoneSilence(r.Context(), addr, msIdx, req.ReasonURI)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
				return
			}
			txHash = tx.Hash().Hex()
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be 'buyer', 'verifier', or 'worker'"})
			return
		}
		_ = h.idx.RunOnce(r.Context())
		writeJSON(w, http.StatusOK, map[string]string{"tx_hash": txHash})
		return
	}

	var txHash string
	switch req.Role {
	case "buyer":
		tx, err := h.chain.Dispute(r.Context(), addr, req.ReasonURI)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
			return
		}
		txHash = tx.Hash().Hex()
	case "verifier":
		tx, err := h.chain.RejectByVerifier(r.Context(), addr, req.ReasonURI)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
			return
		}
		txHash = tx.Hash().Hex()
	case "worker":
		tx, err := h.chain.EscalateSilence(r.Context(), addr, req.ReasonURI)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
			return
		}
		txHash = tx.Hash().Hex()
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be 'buyer', 'verifier', or 'worker'"})
		return
	}

	_ = h.idx.RunOnce(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": txHash})
}

type resolveRequest struct {
	WorkerAwardBps string `json:"worker_award_bps"`
	ResolutionURI  string `json:"resolution_uri"`
	MilestoneIndex *int   `json:"milestone_index,omitempty"`
}

func (h *Handlers) ResolveDispute(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	var req resolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	bps, err := strconv.ParseUint(req.WorkerAwardBps, 10, 16)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid worker_award_bps"})
		return
	}

	escrow, err := h.db.GetEscrow(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	addr := common.HexToAddress(escrow.EscrowAddress)

	if escrow.MilestoneCount > 1 {
		if req.MilestoneIndex == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "milestone_index required for multi-milestone escrow"})
			return
		}
		msIdxVal := *req.MilestoneIndex
		if msIdxVal < 0 || msIdxVal >= escrow.MilestoneCount {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("milestone_index %d out of range [0, %d)", msIdxVal, escrow.MilestoneCount)})
			return
		}
		msIdx, convErr := numconv.IntToUint8(msIdxVal, "milestone_index")
		if convErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": convErr.Error()})
			return
		}
		tx, err := h.chain.ResolveMilestoneDispute(r.Context(), addr, msIdx, uint16(bps), req.ResolutionURI)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
			return
		}
		_ = h.idx.RunOnce(r.Context())
		writeJSON(w, http.StatusOK, map[string]string{"tx_hash": tx.Hash().Hex()})
		return
	}

	tx, err := h.chain.ResolveDispute(r.Context(), addr, uint16(bps), req.ResolutionURI)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
		return
	}

	_ = h.idx.RunOnce(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": tx.Hash().Hex()})
}

func (h *Handlers) AbortRemainingMilestones(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	escrow, err := h.db.GetEscrow(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	if escrow.MilestoneCount <= 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "abort_remaining_milestones is only available for multi-milestone escrows"})
		return
	}

	tx, err := h.chain.AbortRemainingMilestones(r.Context(), common.HexToAddress(escrow.EscrowAddress))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
		return
	}

	_ = h.idx.RunOnce(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": tx.Hash().Hex()})
}

func (h *Handlers) ActivateBackup(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	escrow, err := h.db.GetEscrow(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	if escrow.BackupWorker == "" || escrow.BackupWorker == "0x0000000000000000000000000000000000000000" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "this escrow has no backup worker designated"})
		return
	}

	if escrow.BackupActivated {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "backup already activated"})
		return
	}

	tx, err := h.chain.ActivateBackup(r.Context(), common.HexToAddress(escrow.EscrowAddress))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
		return
	}

	_ = h.idx.RunOnce(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": tx.Hash().Hex()})
}

func (h *Handlers) GetReputation(w http.ResponseWriter, r *http.Request) {
	addr := r.PathValue("address")
	if !common.IsHexAddress(addr) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid address"})
		return
	}
	addr = strings.ToLower(common.HexToAddress(addr).Hex())

	role := r.URL.Query().Get("role")
	if role != "" && role != "worker" && role != "buyer" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be 'worker' or 'buyer'"})
		return
	}

	if role != "" {
		rep, err := h.db.GetReputation(r.Context(), addr, role)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusOK, map[string]any{
					"address": addr, "role": role,
					"completed": 0, "disputed": 0, "failed": 0,
				})
				return
			}
			slog.Error("GetReputation DB error", "address", addr, "role", role, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, http.StatusOK, rep)
		return
	}

	reps, err := h.db.GetReputationByAddress(r.Context(), addr)
	if err != nil {
		slog.Error("GetReputationByAddress DB error", "address", addr, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if len(reps) == 0 {
		writeJSON(w, http.StatusOK, []map[string]any{
			{"address": addr, "role": "worker", "completed": 0, "disputed": 0, "failed": 0},
			{"address": addr, "role": "buyer", "completed": 0, "disputed": 0, "failed": 0},
		})
		return
	}
	writeJSON(w, http.StatusOK, reps)
}

func (h *Handlers) biddingService() *bidding.Service {
	return &bidding.Service{
		DB:    h.db,
		Chain: h.chain,
		Idx:   h.idx,
		Cfg:   h.cfg,
	}
}

// RFQ Handlers

type createRFQRequest struct {
	Title                    string `json:"title"`
	Description              string `json:"description"`
	Buyer                    string `json:"buyer"`
	Token                    string `json:"token,omitempty"`
	BudgetMin                string `json:"budget_min"`
	BudgetMax                string `json:"budget_max"`
	Deadline                 string `json:"deadline"`
	ReviewPeriodSeconds      string `json:"review_period_seconds"`
	DisputePeriodSeconds     string `json:"dispute_period_seconds"`
	ArbitratorTimeoutSeconds string `json:"arbitrator_timeout_seconds"`
	Verifier                 string `json:"verifier,omitempty"`
	Arbitrator               string `json:"arbitrator,omitempty"`
	WorkerStake              string `json:"worker_stake,omitempty"`
	MilestonesJSON           string `json:"milestones_json,omitempty"`
	RequirementsJSON         string `json:"requirements_json,omitempty"`
	RequiredCredentialsJSON  string `json:"required_credentials_json,omitempty"`
	CommitDeadline           string `json:"commit_deadline,omitempty"`
	RevealDeadline           string `json:"reveal_deadline,omitempty"`
	ExpiresAt                string `json:"expires_at"`
	ParentEscrowID           *int64 `json:"parent_escrow_id,omitempty"`
}

func (h *Handlers) CreateRFQ(w http.ResponseWriter, r *http.Request) {
	var req createRFQRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	deadline, err := strconv.ParseInt(req.Deadline, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid deadline"})
		return
	}
	review, err := strconv.ParseInt(req.ReviewPeriodSeconds, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid review_period_seconds"})
		return
	}
	dispute, err := strconv.ParseInt(req.DisputePeriodSeconds, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid dispute_period_seconds"})
		return
	}
	arbTimeout, err := strconv.ParseInt(req.ArbitratorTimeoutSeconds, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid arbitrator_timeout_seconds"})
		return
	}
	expiresAt, err := strconv.ParseInt(req.ExpiresAt, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid expires_at"})
		return
	}
	var commitDeadline int64
	if req.CommitDeadline != "" {
		commitDeadline, err = strconv.ParseInt(req.CommitDeadline, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid commit_deadline"})
			return
		}
	}
	var revealDeadline int64
	if req.RevealDeadline != "" {
		revealDeadline, err = strconv.ParseInt(req.RevealDeadline, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid reveal_deadline"})
			return
		}
	}

	svc := h.biddingService()
	rfq, err := svc.CreateRFQ(r.Context(), bidding.CreateRFQParams{
		Title:                    req.Title,
		Description:              req.Description,
		Buyer:                    req.Buyer,
		Token:                    req.Token,
		BudgetMin:                req.BudgetMin,
		BudgetMax:                req.BudgetMax,
		Deadline:                 deadline,
		ReviewPeriodSeconds:      review,
		DisputePeriodSeconds:     dispute,
		ArbitratorTimeoutSeconds: arbTimeout,
		Verifier:                 req.Verifier,
		Arbitrator:               req.Arbitrator,
		WorkerStake:              req.WorkerStake,
		MilestonesJSON:           req.MilestonesJSON,
		RequirementsJSON:         req.RequirementsJSON,
		RequiredCredentialsJSON:  req.RequiredCredentialsJSON,
		CommitDeadline:           commitDeadline,
		RevealDeadline:           revealDeadline,
		ExpiresAt:                expiresAt,
		ParentEscrowID:           req.ParentEscrowID,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, rfq)
}

func (h *Handlers) ListRFQs(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	buyer := r.URL.Query().Get("buyer")

	rfqs, err := h.db.ListRFQs(r.Context(), status, buyer)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, rfqs)
}

func (h *Handlers) GetRFQ(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	rfq, err := h.db.GetRFQ(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if time.Now().Unix() > rfq.RevealDeadline {
		if err := h.db.ExpireCommittedBidCommits(r.Context(), rfq.ID); err != nil {
			slog.Error("failed to expire committed bid commits", "rfq_id", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to expire stale bid commits"})
			return
		}
	}

	bids, err := h.db.ListBidsByRFQ(r.Context(), id)
	if err != nil {
		slog.Error("failed to fetch bids for rfq", "rfq_id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch bids"})
		return
	}

	commits, err := h.db.ListBidCommitsByRFQ(r.Context(), id)
	if err != nil {
		slog.Error("failed to fetch bid commits for rfq", "rfq_id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch bid commits"})
		return
	}
	type publicBidCommit struct {
		ID            int64     `json:"id"`
		RFQID         int64     `json:"rfq_id"`
		Bidder        string    `json:"bidder"`
		Status        string    `json:"status"`
		RevealedBidID *int64    `json:"revealed_bid_id,omitempty"`
		CreatedAt     time.Time `json:"created_at"`
		UpdatedAt     time.Time `json:"updated_at"`
	}
	publicCommits := make([]publicBidCommit, 0, len(commits))
	for _, c := range commits {
		publicCommits = append(publicCommits, publicBidCommit{
			ID:            c.ID,
			RFQID:         c.RFQID,
			Bidder:        c.Bidder,
			Status:        c.Status,
			RevealedBidID: c.RevealedBidID,
			CreatedAt:     c.CreatedAt,
			UpdatedAt:     c.UpdatedAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"rfq":        rfq,
		"bids":       bids,
		"commits":    publicCommits,
		"now_unix":   time.Now().Unix(),
		"phase_hint": map[string]int64{"commit_deadline": rfq.CommitDeadline, "reveal_deadline": rfq.RevealDeadline},
	})
}

func (h *Handlers) CancelRFQ(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	rfq, err := h.db.GetRFQ(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if rfq.Status != "open" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("rfq is not open (status: %s)", rfq.Status)})
		return
	}

	if err := h.db.UpdateRFQStatus(r.Context(), id, "cancelled"); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := h.db.RejectPendingBids(r.Context(), id, 0); err != nil {
		slog.Error("failed to reject pending bids on cancel", "rfq_id", id, "error", err)
		if rollbackErr := h.db.UpdateRFQStatus(r.Context(), id, "open"); rollbackErr != nil {
			slog.Error("failed to rollback rfq status after RejectPendingBids failure", "rfq_id", id, "error", rollbackErr)
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("cancel failed: %v", err)})
		return
	}
	if err := h.db.RejectUnacceptedBidCommits(r.Context(), id, 0); err != nil {
		slog.Error("failed to reject bid commits on cancel", "rfq_id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("cancel failed: %v", err)})
		return
	}

	rfq, _ = h.db.GetRFQ(r.Context(), id)
	writeJSON(w, http.StatusOK, rfq)
}

type commitBidRequest struct {
	Bidder     string `json:"bidder"`
	Commitment string `json:"commitment"`
	Nonce      string `json:"nonce"`
}

type revealBidRequest struct {
	Bidder            string `json:"bidder"`
	Nonce             string `json:"nonce"`
	Salt              string `json:"salt"`
	Amount            string `json:"amount"`
	EstimatedDuration int64  `json:"estimated_duration,omitempty"`
	ReputationBond    string `json:"reputation_bond,omitempty"`
	MilestonesJSON    string `json:"milestones_json,omitempty"`
	Message           string `json:"message,omitempty"`
	ExpiresAt         string `json:"expires_at"`
	StakeMandateID    string `json:"stake_mandate_id,omitempty"`
	CredentialsJSON   string `json:"credentials_json,omitempty"`
}

func (h *Handlers) CommitBid(w http.ResponseWriter, r *http.Request) {
	rfqID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid rfq id"})
		return
	}

	var req commitBidRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	svc := h.biddingService()
	commit, err := svc.CommitBid(r.Context(), bidding.CommitBidParams{
		RFQID:      rfqID,
		Bidder:     req.Bidder,
		Commitment: req.Commitment,
		Nonce:      req.Nonce,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, commit)
}

func (h *Handlers) RevealBid(w http.ResponseWriter, r *http.Request) {
	rfqID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid rfq id"})
		return
	}

	var req revealBidRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	var expiresAt int64
	if req.ExpiresAt != "" {
		expiresAt, err = strconv.ParseInt(req.ExpiresAt, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid expires_at"})
			return
		}
	}

	svc := h.biddingService()
	bid, err := svc.RevealBid(r.Context(), bidding.RevealBidParams{
		RFQID:             rfqID,
		Bidder:            req.Bidder,
		Nonce:             req.Nonce,
		Salt:              req.Salt,
		Amount:            req.Amount,
		EstimatedDuration: req.EstimatedDuration,
		ReputationBond:    req.ReputationBond,
		MilestonesJSON:    req.MilestonesJSON,
		Message:           req.Message,
		ExpiresAt:         expiresAt,
		StakeMandateID:    req.StakeMandateID,
		CredentialsJSON:   req.CredentialsJSON,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, bid)
}

func (h *Handlers) ListBids(w http.ResponseWriter, r *http.Request) {
	rfqID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid rfq id"})
		return
	}
	rfq, err := h.db.GetRFQ(r.Context(), rfqID)
	if err == nil {
		if time.Now().Unix() > rfq.RevealDeadline {
			if expireErr := h.db.ExpireCommittedBidCommits(r.Context(), rfqID); expireErr != nil {
				slog.Error("failed to expire committed bid commits", "rfq_id", rfqID, "error", expireErr)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to expire stale bid commits"})
				return
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		slog.Error("failed to fetch rfq in list bids", "rfq_id", rfqID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch rfq"})
		return
	}

	bids, err := h.db.ListBidsByRFQ(r.Context(), rfqID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, bids)
}

type acceptBidRequest struct {
	BidID  int64  `json:"bid_id"`
	Caller string `json:"caller,omitempty"`
}

func (h *Handlers) AcceptBid(w http.ResponseWriter, r *http.Request) {
	rfqID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid rfq id"})
		return
	}

	var req acceptBidRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	svc := h.biddingService()
	result, err := svc.AcceptBid(r.Context(), bidding.AcceptBidParams{
		RFQID:  rfqID,
		BidID:  req.BidID,
		Caller: req.Caller,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"escrow_id":       result.Escrow.ID,
		"task_id":         result.Task.ID,
		"tx_hash":         result.TxHash,
		"escrow_address":  result.Escrow.EscrowAddress,
		"chain_escrow_id": result.Escrow.EscrowID,
		"bid_id":          result.Bid.ID,
		"bid_status":      result.Bid.Status,
	})
}

// --- Emergency response protocol handlers (paper §4.9) ---

func (h *Handlers) writeEmergencyRecordingError(
	w http.ResponseWriter, r *http.Request, txHash, operation string, err error,
) {
	slog.Error("emergency action recorded on-chain but local persistence failed",
		"operation", operation,
		"path", r.URL.Path,
		"method", r.Method,
		"tx_hash", txHash,
		"error", err,
	)
	writeJSON(w, http.StatusInternalServerError, map[string]any{
		"error":   fmt.Sprintf("on-chain transaction succeeded but local %s recording failed: %v", operation, err),
		"tx_hash": txHash,
	})
}

func (h *Handlers) FreezeAddress(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address string `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if !common.IsHexAddress(req.Address) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid address"})
		return
	}
	addr := common.HexToAddress(req.Address)
	factory := h.idx.FactoryAddress()

	tx, err := h.chain.FreezeAddress(r.Context(), factory, addr)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
		return
	}

	txHash := tx.Hash().Hex()
	if err := h.db.UpsertFrozenAddress(r.Context(), strings.ToLower(addr.Hex()), "", "api"); err != nil {
		h.writeEmergencyRecordingError(w, r, txHash, "freeze address", err)
		return
	}
	if err := h.db.CreateEmergencyAction(r.Context(), "freeze_address", strings.ToLower(addr.Hex()), "", "", txHash); err != nil {
		h.writeEmergencyRecordingError(w, r, txHash, "freeze address audit", err)
		return
	}
	if err := h.idx.RunOnce(r.Context()); err != nil {
		h.writeEmergencyRecordingError(w, r, txHash, "indexer sync", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": txHash, "address": addr.Hex()})
}

func (h *Handlers) UnfreezeAddress(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address string `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if !common.IsHexAddress(req.Address) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid address"})
		return
	}
	addr := common.HexToAddress(req.Address)
	factory := h.idx.FactoryAddress()

	tx, err := h.chain.UnfreezeAddress(r.Context(), factory, addr)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
		return
	}

	txHash := tx.Hash().Hex()
	if err := h.db.DeleteFrozenAddress(r.Context(), strings.ToLower(addr.Hex())); err != nil {
		h.writeEmergencyRecordingError(w, r, txHash, "unfreeze address", err)
		return
	}
	if err := h.db.CreateEmergencyAction(r.Context(), "unfreeze_address", strings.ToLower(addr.Hex()), "", "", txHash); err != nil {
		h.writeEmergencyRecordingError(w, r, txHash, "unfreeze address audit", err)
		return
	}
	if err := h.idx.RunOnce(r.Context()); err != nil {
		h.writeEmergencyRecordingError(w, r, txHash, "indexer sync", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": txHash, "address": addr.Hex()})
}

func (h *Handlers) FreezeEscrow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EscrowID int64 `json:"escrow_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	escrow, err := h.db.GetEscrow(r.Context(), req.EscrowID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "escrow not found"})
		return
	}

	factory := h.idx.FactoryAddress()
	tx, err := h.chain.FreezeEscrow(r.Context(), factory, big.NewInt(escrow.EscrowID))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
		return
	}

	txHash := tx.Hash().Hex()
	if err := h.db.RecordFreezeEscrowAndRevokeDCT(r.Context(), req.EscrowID, escrow.EscrowAddress, txHash); err != nil {
		h.writeEmergencyRecordingError(w, r, txHash, "freeze escrow local recording", err)
		return
	}
	if err := h.idx.RunOnce(r.Context()); err != nil {
		h.writeEmergencyRecordingError(w, r, txHash, "indexer sync", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tx_hash": txHash, "escrow_id": req.EscrowID})
}

func (h *Handlers) UnfreezeEscrow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EscrowID int64 `json:"escrow_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	escrow, err := h.db.GetEscrow(r.Context(), req.EscrowID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "escrow not found"})
		return
	}

	factory := h.idx.FactoryAddress()
	tx, err := h.chain.UnfreezeEscrow(r.Context(), factory, big.NewInt(escrow.EscrowID))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
		return
	}

	txHash := tx.Hash().Hex()
	if err := h.db.UpdateEscrowFrozen(r.Context(), req.EscrowID, false); err != nil {
		h.writeEmergencyRecordingError(w, r, txHash, "unfreeze escrow", err)
		return
	}
	if err := h.db.CreateEmergencyAction(r.Context(), "unfreeze_escrow", escrow.EscrowAddress, "", "", txHash); err != nil {
		h.writeEmergencyRecordingError(w, r, txHash, "unfreeze escrow audit", err)
		return
	}
	if err := h.idx.RunOnce(r.Context()); err != nil {
		h.writeEmergencyRecordingError(w, r, txHash, "indexer sync", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tx_hash": txHash, "escrow_id": req.EscrowID})
}

func (h *Handlers) EmergencyResolve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EscrowID       int64  `json:"escrow_id"`
		WorkerAwardBps uint16 `json:"worker_award_bps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.WorkerAwardBps > 10000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "worker_award_bps must be 0-10000"})
		return
	}

	escrow, err := h.db.GetEscrow(r.Context(), req.EscrowID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "escrow not found"})
		return
	}

	factory := h.idx.FactoryAddress()
	tx, err := h.chain.EmergencyResolve(r.Context(), factory, big.NewInt(escrow.EscrowID), req.WorkerAwardBps)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
		return
	}

	txHash := tx.Hash().Hex()
	if err := h.db.RecordEmergencyResolveAndRevokeDCT(r.Context(), req.EscrowID, escrow.EscrowAddress, req.WorkerAwardBps, txHash); err != nil {
		h.writeEmergencyRecordingError(w, r, txHash, "emergency resolve local recording", err)
		return
	}
	if err := h.idx.RunOnce(r.Context()); err != nil {
		h.writeEmergencyRecordingError(w, r, txHash, "indexer sync", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tx_hash":          txHash,
		"escrow_id":        req.EscrowID,
		"worker_award_bps": req.WorkerAwardBps,
	})
}

func (h *Handlers) ListFrozenAddresses(w http.ResponseWriter, r *http.Request) {
	addrs, err := h.db.ListFrozenAddresses(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("db: %v", err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"frozen_addresses": addrs, "count": len(addrs)})
}

func (h *Handlers) ListEmergencyActions(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	actions, err := h.db.ListEmergencyActions(r.Context(), limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("db: %v", err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": actions, "count": len(actions)})
}

// BazaarDiscovery returns Bazaar-compatible discovery metadata for credential schemas
// used in the RFQ/bid protocol (paper §4.6 Table 3, Bazaar discovery extensions).
func (h *Handlers) BazaarDiscovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"protocol":    "agent-escrow",
		"version":     "v1",
		"description": "Escrow-based delegation marketplace with verifiable bid credentials",
		"credential_schemas": map[string]any{
			"attestation-v1": map[string]any{
				"profile":     "attestation-v1",
				"description": "Signed capability attestation (secp256k1 over canonical message)",
				"fields": map[string]string{
					"profile":         "Must be 'attestation-v1'",
					"issuer_address":  "Ethereum address of the endorser",
					"issuer_did":      "Optional DID identifier for the issuer (forward-compatible)",
					"subject_address": "Ethereum address of the attested agent (must match bidder)",
					"domain":          "Capability domain (e.g. 'code-review', 'smart-contract-audit')",
					"capabilities":    "JSON array of specific capabilities within the domain",
					"issued_at":       "Unix timestamp when the attestation was created",
					"expires_at":      "Unix timestamp when the attestation expires",
					"nonce":           "Unique nonce to prevent replay",
					"signature":       "0x-prefixed secp256k1 signature (65 bytes) over the canonical message",
				},
				"canonical_message_format": "attestation-v1|issuer|subject|domain|cap1,cap2,...|issued_at|expires_at|nonce",
			},
		},
		"rfq_credential_requirement_schema": map[string]any{
			"description": "Buyer-specified credential filter attached to an RFQ",
			"fields": map[string]string{
				"domain":          "Required capability domain",
				"capabilities":    "JSON array of required capabilities",
				"trusted_issuers": "Optional array of trusted issuer addresses",
			},
		},
		"endpoints": map[string]string{
			"create_rfq_attested":          "POST /api/v1/rfqs (include required_credentials_json)",
			"reveal_bid_with_attestations": "POST /api/v1/rfqs/{id}/bids/reveal (include credentials_json)",
			"list_bids_attested":           "GET /api/v1/rfqs/{id}/bids",
			"bazaar_discovery":             "GET /api/v1/bazaar/discovery",
		},
	})
}

func isValidAddress(s string) bool {
	return common.IsHexAddress(s) && s != "0x0000000000000000000000000000000000000000"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON encode failed", "status", status, "error", err)
	}
}
