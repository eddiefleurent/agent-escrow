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
	"sync"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/authz"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/bidding"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/decomposition"
	escrowservice "github.com/eddiefleurent/agent-escrow/go-server/internal/escrow"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/ethereum/go-ethereum/common"
)

type Handlers struct {
	db    *storage.DB
	chain chain.ChainClient
	idx   *indexer.Indexer
	cfg   *config.Config

	escrowOnce sync.Once
	escrowSvc  *escrowservice.Service
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

func (h *Handlers) CreateEscrow(w http.ResponseWriter, r *http.Request) {
	var req escrowservice.CreateEscrowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	input, err := escrowservice.ParseCreateRequest(r.Context(), req, h.db, h.cfg)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result, err := h.escrowService().CreateEscrow(r.Context(), input)
	if err != nil {
		if escrowservice.IsValidation(err) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": validationMessage(err)})
			return
		}
		slog.ErrorContext(r.Context(), "create escrow failed", "method", r.Method, "path", r.URL.Path, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"escrow_id":       result.EscrowID,
		"task_id":         result.TaskID,
		"tx_hash":         result.TxHash,
		"escrow_address":  result.EscrowAddress,
		"chain_escrow_id": result.ChainEscrowID,
		"milestone_count": result.MilestoneCount,
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
			slog.ErrorContext(r.Context(), "failed to fetch milestones", "escrow_id", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch milestones"})
			return
		}
		result["milestones"] = milestones
	}

	// Include attestation chain summary if present.
	chains, chainErr := h.db.GetAttestationChainsByEscrow(r.Context(), id)
	if chainErr != nil {
		slog.ErrorContext(r.Context(), "failed to fetch optional attestation_chains for escrow response", "escrow_id", id, "result_key", "attestation_chains", "error", chainErr)
	} else if len(chains) > 0 {
		result["attestation_chains"] = chains
	}

	// Include child escrow count for delegation visibility.
	children, childErr := h.db.ListChildEscrows(r.Context(), id)
	if childErr != nil {
		slog.ErrorContext(r.Context(), "failed to fetch optional child_escrow_ids for escrow response", "escrow_id", id, "result_key", "child_escrow_ids", "error", childErr)
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
			slog.ErrorContext(r.Context(), "failed to fetch attestation links", "chain_id", ac.ID, "error", linkErr)
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
		slog.ErrorContext(r.Context(), "list escrows failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
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

	txHash, err := h.escrowService().FundEscrow(r.Context(), escrow)
	if err != nil {
		handleEscrowServiceActionError(w, r, "fund_escrow", id, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": txHash})
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
	txHash, err := h.escrowService().DepositWorkerStake(r.Context(), escrow)
	if err != nil {
		handleEscrowServiceActionError(w, r, "deposit_worker_stake", id, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": txHash})
}

func (h *Handlers) DepositVerifierStake(w http.ResponseWriter, r *http.Request) {
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
	txHash, err := h.escrowService().DepositVerifierStake(r.Context(), escrow)
	if err != nil {
		handleEscrowServiceActionError(w, r, "deposit_verifier_stake", id, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": txHash})
}

// WithdrawStake claims verifier stake owed to the caller after quorum settlement or refund.
func (h *Handlers) WithdrawStake(w http.ResponseWriter, r *http.Request) {
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
	txHash, err := h.escrowService().WithdrawStake(r.Context(), escrow)
	if err != nil {
		handleEscrowServiceActionError(w, r, "withdraw_stake", id, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": txHash})
}

type submitRequest struct {
	SubmissionURI        string `json:"submission_uri"`
	ProofHash            string `json:"proof_hash,omitempty"`
	MilestoneIndex       *int   `json:"milestone_index,omitempty"`
	AttestationChainJSON string `json:"attestation_chain_json,omitempty"`
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
	txHash, err := h.escrowService().SubmitWork(r.Context(), escrow, escrowservice.SubmitRequest{
		SubmissionURI:        req.SubmissionURI,
		ProofHash:            req.ProofHash,
		MilestoneIndex:       req.MilestoneIndex,
		AttestationChainJSON: req.AttestationChainJSON,
	})
	if err != nil {
		handleEscrowServiceActionError(w, r, "submit_work", id, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": txHash})
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
	txHash, err := h.escrowService().ApproveWork(r.Context(), escrow, req.Role, req.MilestoneIndex)
	if err != nil {
		handleEscrowServiceActionError(w, r, "approve_work", id, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": txHash})
}

type verifyApproveRequest struct {
	Proof          string `json:"proof"`
	MilestoneIndex *int   `json:"milestone_index,omitempty"`
}

func (h *Handlers) VerifyAndApprove(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	var req verifyApproveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	proofBytes, err := escrowservice.ParseProofHexBytes(req.Proof)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid proof: %v", err)})
		return
	}

	escrow, err := h.db.GetEscrow(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	txHash, err := h.escrowService().VerifyAndApprove(r.Context(), escrow, proofBytes, req.MilestoneIndex)
	if err != nil {
		handleEscrowServiceActionError(w, r, "verify_and_approve", id, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": txHash})
}

type quorumVoteRequest struct {
	Approve        *bool  `json:"approve"`
	ReasonURI      string `json:"reason_uri,omitempty"`
	MilestoneIndex *int   `json:"milestone_index,omitempty"`
}

func (h *Handlers) CastVerifierVote(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	var req quorumVoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.Approve == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "approve is required"})
		return
	}

	escrow, err := h.db.GetEscrow(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	txHash, err := h.escrowService().CastVerifierVote(r.Context(), escrow, *req.Approve, req.ReasonURI, req.MilestoneIndex)
	if err != nil {
		handleEscrowServiceActionError(w, r, "cast_verifier_vote", id, err)
		return
	}
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
	txHash, err := h.escrowService().DisputeWork(r.Context(), escrow, req.Role, req.ReasonURI, req.MilestoneIndex)
	if err != nil {
		handleEscrowServiceActionError(w, r, "dispute_work", id, err)
		return
	}
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
	txHash, err := h.escrowService().ResolveDispute(r.Context(), escrow, uint16(bps), req.ResolutionURI, req.MilestoneIndex)
	if err != nil {
		handleEscrowServiceActionError(w, r, "resolve_dispute", id, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": txHash})
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
	txHash, err := h.escrowService().AbortRemainingMilestones(r.Context(), escrow)
	if err != nil {
		handleEscrowServiceActionError(w, r, "abort_remaining_milestones", id, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": txHash})
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
	txHash, err := h.escrowService().ActivateBackup(r.Context(), escrow)
	if err != nil {
		handleEscrowServiceActionError(w, r, "activate_backup", id, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": txHash})
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
		view, err := h.db.GetReputationView(r.Context(), addr, role, h.cfg.ReputationDampingFactor)
		if err != nil {
			slog.ErrorContext(r.Context(), "GetReputation DB error", "address", addr, "role", role, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, http.StatusOK, view)
		return
	}

	views, err := h.db.GetReputationViewsByAddress(r.Context(), addr, h.cfg.ReputationDampingFactor)
	if err != nil {
		slog.ErrorContext(r.Context(), "GetReputationByAddress DB error", "address", addr, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"address":        addr,
		"damping_factor": h.cfg.ReputationDampingFactor,
		"roles":          views,
	})
}

func (h *Handlers) escrowService() *escrowservice.Service {
	h.escrowOnce.Do(func() {
		h.escrowSvc = escrowservice.NewService(h.db, h.chain, h.idx, h.cfg)
	})
	return h.escrowSvc
}

func (h *Handlers) biddingService() *bidding.Service {
	return &bidding.Service{
		DB:    h.db,
		Chain: h.chain,
		Idx:   h.idx,
		Cfg:   h.cfg,
	}
}

func (h *Handlers) decompositionService() *decomposition.Service {
	return &decomposition.Service{
		DB:      h.db,
		Bidding: h.biddingService(),
	}
}

// Decomposition Handlers

type createDecompositionRequest struct {
	Title       string                       `json:"title"`
	Description string                       `json:"description"`
	Buyer       string                       `json:"buyer"`
	SpecHash    string                       `json:"spec_hash,omitempty"`
	SubTasks    []decomposition.SubTaskInput `json:"sub_tasks"`
}

func (h *Handlers) CreateDecomposition(w http.ResponseWriter, r *http.Request) {
	var req createDecompositionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	result, err := h.decompositionService().CreateDecomposition(r.Context(), decomposition.CreateDecompositionParams{
		Buyer:       req.Buyer,
		Title:       req.Title,
		Description: req.Description,
		SpecHash:    req.SpecHash,
		SubTasks:    req.SubTasks,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	nextSteps := "Fix structural issues and resubmit."
	if result.Valid {
		nextSteps = "Decomposition is valid. Call finalize to create RFQs."
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"decomposition_id": result.Decomposition.ID,
		"status":           result.Decomposition.Status,
		"valid":            result.Valid,
		"nodes":            result.Nodes,
		"issues":           result.Issues,
		"market_context":   result.MarketContext,
		"next_steps":       nextSteps,
	})
}

func (h *Handlers) ListDecompositions(w http.ResponseWriter, r *http.Request) {
	buyer := r.URL.Query().Get("buyer")
	status := r.URL.Query().Get("status")
	items, err := h.decompositionService().ListDecompositions(r.Context(), buyer, status)
	if err != nil {
		slog.ErrorContext(r.Context(), "list decompositions failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	if items == nil {
		items = []*storage.Decomposition{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *Handlers) GetDecomposition(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	decomp, nodes, err := h.decompositionService().GetDecomposition(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		} else {
			slog.ErrorContext(r.Context(), "get decomposition failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		}
		return
	}

	var issues []decomposition.StructuralIssue
	if decomp.ValidationErrorsJSON != "" && json.Valid([]byte(decomp.ValidationErrorsJSON)) {
		if unmarshalErr := json.Unmarshal([]byte(decomp.ValidationErrorsJSON), &issues); unmarshalErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to decode validation errors"})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"decomposition": decomp,
		"nodes":         nodes,
		"suggestions": map[string]any{
			"structural_issues": issues,
		},
	})
}

type finalizeDecompositionRequest struct {
	Buyer                    string   `json:"buyer"`
	Token                    string   `json:"token,omitempty"`
	Deadline                 string   `json:"deadline"`
	ReviewPeriodSeconds      string   `json:"review_period_seconds"`
	DisputePeriodSeconds     string   `json:"dispute_period_seconds"`
	ArbitratorTimeoutSeconds string   `json:"arbitrator_timeout_seconds"`
	Arbitrator               string   `json:"arbitrator,omitempty"`
	VerifierPanel            []string `json:"verifier_panel,omitempty"`
	QuorumCount              string   `json:"quorum_count,omitempty"`
	BudgetMin                string   `json:"budget_min"`
	BudgetMax                string   `json:"budget_max"`
	CommitDeadline           string   `json:"commit_deadline"`
	RevealDeadline           string   `json:"reveal_deadline"`
	ExpiresAt                string   `json:"expires_at"`
}

func (h *Handlers) FinalizeDecomposition(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	var req finalizeDecompositionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	deadline, err := strconv.ParseInt(req.Deadline, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid deadline"})
		return
	}
	reviewPeriodSeconds, err := strconv.ParseInt(req.ReviewPeriodSeconds, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid review_period_seconds"})
		return
	}
	disputePeriodSeconds, err := strconv.ParseInt(req.DisputePeriodSeconds, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid dispute_period_seconds"})
		return
	}
	arbitratorTimeoutSeconds, err := strconv.ParseInt(req.ArbitratorTimeoutSeconds, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid arbitrator_timeout_seconds"})
		return
	}
	commitDeadline, err := strconv.ParseInt(req.CommitDeadline, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid commit_deadline"})
		return
	}
	revealDeadline, err := strconv.ParseInt(req.RevealDeadline, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid reveal_deadline"})
		return
	}
	expiresAt, err := strconv.ParseInt(req.ExpiresAt, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid expires_at"})
		return
	}
	quorumCount := 0
	if strings.TrimSpace(req.QuorumCount) != "" {
		quorumCount, err = strconv.Atoi(req.QuorumCount)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid quorum_count"})
			return
		}
	}

	decomp, rfqIDs, err := h.decompositionService().FinalizeDecomposition(r.Context(), decomposition.FinalizeParams{
		DecompositionID:          id,
		Buyer:                    req.Buyer,
		Token:                    req.Token,
		Deadline:                 deadline,
		ReviewPeriodSeconds:      reviewPeriodSeconds,
		DisputePeriodSeconds:     disputePeriodSeconds,
		ArbitratorTimeoutSeconds: arbitratorTimeoutSeconds,
		Arbitrator:               req.Arbitrator,
		VerifierPanel:            req.VerifierPanel,
		QuorumCount:              quorumCount,
		BudgetMin:                req.BudgetMin,
		BudgetMax:                req.BudgetMax,
		CommitDeadline:           commitDeadline,
		RevealDeadline:           revealDeadline,
		ExpiresAt:                expiresAt,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"decomposition_id": decomp.ID,
		"status":           decomp.Status,
		"rfq_ids":          rfqIDs,
		"rfq_count":        len(rfqIDs),
		"next_steps":       "Workers can now discover and bid on generated RFQs.",
	})
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
	RequiredProofProtocol    string `json:"required_proof_protocol,omitempty"`
	RequiredCredentialsJSON  string `json:"required_credentials_json,omitempty"`
	CommitDeadline           string `json:"commit_deadline,omitempty"`
	RevealDeadline           string `json:"reveal_deadline,omitempty"`
	ServiceTier              int    `json:"service_tier,omitempty"`
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

	if req.ServiceTier < 0 || req.ServiceTier > 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid service_tier: must be 0 or 1"})
		return
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
		RequiredProofProtocol:    req.RequiredProofProtocol,
		RequiredCredentialsJSON:  req.RequiredCredentialsJSON,
		ServiceTier:              req.ServiceTier,
		CommitDeadline:           commitDeadline,
		RevealDeadline:           revealDeadline,
		ExpiresAt:                expiresAt,
		ParentEscrowID:           req.ParentEscrowID,
	})
	if err != nil {
		var cooldownErr *bidding.RebidCooldownError
		if errors.As(err, &cooldownErr) {
			retrySecs := cooldownErr.RetryAfterSeconds()
			w.Header().Set("Retry-After", strconv.FormatInt(retrySecs, 10))
			writeJSON(w, http.StatusTooManyRequests, map[string]any{
				"error":               cooldownErr.Error(),
				"retry_after_seconds": retrySecs,
				"retry_at":            cooldownErr.RetryAt.Unix(),
			})
			return
		}
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
		slog.ErrorContext(r.Context(), "list rfqs failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, h.biddingService().NormalizeRFQs(rfqs, time.Now()))
}

func (h *Handlers) GetRFQ(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	rfq, err := h.db.GetRFQ(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		slog.ErrorContext(r.Context(), "failed to fetch rfq", "rfq_id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch rfq"})
		return
	}
	if rfq.BiddingMode == "sealed" && time.Now().Unix() > rfq.RevealDeadline {
		if _, err := h.biddingService().FinalizeSealedBidding(r.Context(), rfq.ID); err != nil {
			slog.ErrorContext(r.Context(), "failed to finalize sealed bidding", "rfq_id", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to finalize sealed bidding"})
			return
		}
		rfq, err = h.db.GetRFQ(r.Context(), id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
				return
			}
			slog.ErrorContext(r.Context(), "failed to refetch rfq after sealed bidding finalization", "rfq_id", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch rfq"})
			return
		}
	}
	rfq = h.biddingService().NormalizeRFQ(rfq, time.Now())

	bids, err := h.db.ListBidsByRFQ(r.Context(), id)
	if err != nil {
		slog.ErrorContext(r.Context(), "failed to fetch bids for rfq", "rfq_id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch bids"})
		return
	}

	commits, err := h.db.ListBidCommitsByRFQ(r.Context(), id)
	if err != nil {
		slog.ErrorContext(r.Context(), "failed to fetch bid commits for rfq", "rfq_id", id, "error", err)
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
		slog.ErrorContext(r.Context(), "cancel rfq: update status failed", "rfq_id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	if err := h.db.RejectPendingBids(r.Context(), id, 0); err != nil {
		slog.ErrorContext(r.Context(), "failed to reject pending bids on cancel", "rfq_id", id, "error", err)
		if rollbackErr := h.db.UpdateRFQStatus(r.Context(), id, "open"); rollbackErr != nil {
			slog.ErrorContext(r.Context(), "failed to rollback rfq status after RejectPendingBids failure", "rfq_id", id, "error", rollbackErr)
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("cancel failed: %v", err)})
		return
	}
	if err := h.db.RejectUnacceptedBidCommits(r.Context(), id, 0); err != nil {
		slog.ErrorContext(r.Context(), "failed to reject bid commits on cancel", "rfq_id", id, "error", err)
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
		var cooldownErr *bidding.SealedBidCooldownError
		if errors.As(err, &cooldownErr) {
			w.Header().Set("Retry-After", strconv.FormatInt(cooldownErr.RetryAfterSeconds(), 10))
			writeJSON(w, http.StatusTooManyRequests, map[string]any{
				"error":               cooldownErr.Error(),
				"retry_after_seconds": cooldownErr.RetryAfterSeconds(),
				"retry_at":            cooldownErr.RetryAt.Unix(),
				"strike_count":        cooldownErr.StrikeCount,
			})
			return
		}
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
		if rfq.BiddingMode == "sealed" && time.Now().Unix() > rfq.RevealDeadline {
			if _, finalizeErr := h.biddingService().FinalizeSealedBidding(r.Context(), rfqID); finalizeErr != nil {
				slog.ErrorContext(r.Context(), "failed to finalize sealed bidding", "rfq_id", rfqID, "error", finalizeErr)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to finalize sealed bidding"})
				return
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		slog.ErrorContext(r.Context(), "failed to fetch rfq in list bids", "rfq_id", rfqID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch rfq"})
		return
	}

	bids, err := h.db.ListBidsByRFQ(r.Context(), rfqID)
	if err != nil {
		slog.ErrorContext(r.Context(), "list bids failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
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
	slog.ErrorContext(r.Context(), "emergency action recorded on-chain but local persistence failed",
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

// Checkpoint handlers (paper §6.1: checkpoint artifacts for mid-task agent swaps)

type commitCheckpointRequest struct {
	StateSnapshotURI string `json:"state_snapshot_uri"`
	SnapshotHash     string `json:"snapshot_hash,omitempty"`
	SchemaVersion    string `json:"schema_version,omitempty"`
	CommittedBy      string `json:"committed_by"`
	MilestoneIndex   *int   `json:"milestone_index,omitempty"`
	CompletionPct    *int   `json:"completion_pct,omitempty"`
	MetadataJSON     string `json:"metadata_json,omitempty"`
}

func (h *Handlers) CommitCheckpoint(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	var req commitCheckpointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	if req.StateSnapshotURI == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "state_snapshot_uri is required"})
		return
	}
	if req.CommittedBy == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "committed_by is required"})
		return
	}

	escrow, err := h.db.GetEscrow(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "escrow not found"})
		} else {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch escrow"})
		}
		return
	}

	if !strings.EqualFold(req.CommittedBy, escrow.ActiveWorker) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only the active worker can commit checkpoints"})
		return
	}

	if req.MilestoneIndex != nil {
		msIdx := *req.MilestoneIndex
		if msIdx < 0 || msIdx >= escrow.MilestoneCount {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("milestone_index %d out of range [0, %d)", msIdx, escrow.MilestoneCount)})
			return
		}
	}

	if req.CompletionPct != nil {
		if *req.CompletionPct < 0 || *req.CompletionPct > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "completion_pct must be 0-100"})
			return
		}
	}
	if req.MetadataJSON != "" && !json.Valid([]byte(req.MetadataJSON)) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "metadata_json must be valid JSON"})
		return
	}

	cp, err := h.db.CreateCheckpoint(r.Context(), &storage.Checkpoint{
		EscrowID:         id,
		MilestoneIndex:   req.MilestoneIndex,
		StateSnapshotURI: req.StateSnapshotURI,
		SnapshotHash:     req.SnapshotHash,
		SchemaVersion:    req.SchemaVersion,
		CommittedBy:      req.CommittedBy,
		CompletionPct:    req.CompletionPct,
		MetadataJSON:     req.MetadataJSON,
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "failed to create checkpoint", "escrow_id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create checkpoint"})
		return
	}

	writeJSON(w, http.StatusCreated, cp)
}

func (h *Handlers) ListCheckpoints(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	escrow, err := h.db.GetEscrow(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "escrow not found"})
		} else {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch escrow"})
		}
		return
	}

	var milestoneIndex *int
	if msStr := r.URL.Query().Get("milestone_index"); msStr != "" {
		v, err := strconv.Atoi(msStr)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid milestone_index"})
			return
		}
		if v < 0 || v >= escrow.MilestoneCount {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("milestone_index %d out of range [0, %d)", v, escrow.MilestoneCount)})
			return
		}
		milestoneIndex = &v
	}

	checkpoints, err := h.db.ListCheckpointsByEscrow(r.Context(), id, milestoneIndex)
	if err != nil {
		slog.ErrorContext(r.Context(), "failed to list checkpoints", "escrow_id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list checkpoints"})
		return
	}

	if checkpoints == nil {
		checkpoints = []*storage.Checkpoint{}
	}
	writeJSON(w, http.StatusOK, checkpoints)
}

func (h *Handlers) GetLatestCheckpoint(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	escrow, err := h.db.GetEscrow(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "escrow not found"})
		} else {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch escrow"})
		}
		return
	}

	var milestoneIndex *int
	if msStr := r.URL.Query().Get("milestone_index"); msStr != "" {
		v, err := strconv.Atoi(msStr)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid milestone_index"})
			return
		}
		if v < 0 || v >= escrow.MilestoneCount {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("milestone_index %d out of range [0, %d)", v, escrow.MilestoneCount)})
			return
		}
		milestoneIndex = &v
	}

	cp, err := h.db.GetLatestCheckpoint(r.Context(), id, milestoneIndex)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no checkpoints found"})
		} else {
			slog.ErrorContext(r.Context(), "failed to get latest checkpoint", "escrow_id", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get latest checkpoint"})
		}
		return
	}

	writeJSON(w, http.StatusOK, cp)
}

func validationMessage(err error) string {
	msg := strings.TrimSpace(err.Error())
	prefix := escrowservice.ErrValidation.Error() + ":"
	msg = strings.TrimSpace(strings.TrimPrefix(msg, prefix))
	if msg == "" {
		return "invalid request"
	}
	return msg
}

func handleEscrowServiceActionError(w http.ResponseWriter, r *http.Request, action string, escrowID int64, err error) {
	if escrowservice.IsValidation(err) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": validationMessage(err)})
		return
	}
	slog.ErrorContext(r.Context(),
		"escrow action failed",
		"action", action,
		"escrow_id", escrowID,
		"method", r.Method,
		"path", r.URL.Path,
		"error", err,
	)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	if status >= 400 {
		v = authz.WithRequestIDField(w.Header(), v)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON encode failed",
			"status", status,
			"error", err,
			"request_id", w.Header().Get("X-Request-ID"),
		)
	}
}
