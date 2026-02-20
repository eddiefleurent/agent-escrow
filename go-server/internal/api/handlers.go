package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
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

type createEscrowRequest struct {
	Title                    string `json:"title"`
	Description              string `json:"description"`
	Buyer                    string `json:"buyer"`
	Worker                   string `json:"worker"`
	Verifier                 string `json:"verifier"`
	Arbitrator               string `json:"arbitrator"`
	Amount                   string `json:"amount"`
	SubmissionDeadline       string `json:"submission_deadline"`
	ReviewPeriodSeconds      string `json:"review_period_seconds"`
	DisputePeriodSeconds     string `json:"dispute_period_seconds"`
	ArbitratorTimeoutSeconds string `json:"arbitrator_timeout_seconds"`
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

	factory := common.HexToAddress(h.cfg.FactoryAddress)
	params := chain.CreateEscrowParams{
		Buyer:                    common.HexToAddress(req.Buyer),
		Worker:                   common.HexToAddress(req.Worker),
		Verifier:                 common.HexToAddress(req.Verifier),
		Arbitrator:               common.HexToAddress(req.Arbitrator),
		Amount:                   amount,
		SubmissionDeadline:       deadline,
		ReviewPeriodSeconds:      review,
		DisputePeriodSeconds:     dispute,
		TaskSpecHash:             specHash,
		ArbitratorTimeoutSeconds: arbTimeout,
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

	task, err := h.db.CreateTask(req.Title, req.Description, specHash.Hex())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("db: %v", err)})
		return
	}

	escrow, err := h.db.CreateEscrow(&storage.Escrow{
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
		Status:                   "created",
		SubmissionDeadline:       int64(deadline),
		ReviewPeriodSeconds:      int64(review),
		DisputePeriodSeconds:     int64(dispute),
		ArbitratorTimeoutSeconds: int64(arbTimeout),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("db: %v", err)})
		return
	}

	_ = h.idx.RunOnce(r.Context())

	writeJSON(w, http.StatusCreated, map[string]any{
		"escrow_id":      escrow.ID,
		"task_id":        task.ID,
		"tx_hash":        tx.Hash().Hex(),
		"escrow_address": result.EscrowAddress.Hex(),
		"chain_escrow_id": result.EscrowID,
	})
}

func (h *Handlers) GetEscrow(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	escrow, err := h.db.GetEscrow(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	writeJSON(w, http.StatusOK, escrow)
}

func (h *Handlers) ListEscrows(w http.ResponseWriter, r *http.Request) {
	role := r.URL.Query().Get("role")
	address := r.URL.Query().Get("address")
	status := r.URL.Query().Get("status")

	escrows, err := h.db.ListEscrows(role, address, status)
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

	escrow, err := h.db.GetEscrow(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	amount, ok := new(big.Int).SetString(escrow.Amount, 10)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "malformed escrow amount in database"})
		return
	}
	tx, err := h.chain.Fund(r.Context(), common.HexToAddress(escrow.EscrowAddress), amount)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
		return
	}

	_ = h.idx.RunOnce(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": tx.Hash().Hex()})
}

type submitRequest struct {
	SubmissionURI string `json:"submission_uri"`
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

	escrow, err := h.db.GetEscrow(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	hash := crypto.Keccak256Hash([]byte(req.SubmissionURI))
	var hashBytes [32]byte
	copy(hashBytes[:], hash.Bytes())

	tx, err := h.chain.Submit(r.Context(), common.HexToAddress(escrow.EscrowAddress), hashBytes, req.SubmissionURI)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
		return
	}

	_ = h.idx.RunOnce(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": tx.Hash().Hex()})
}

type approveRequest struct {
	Role string `json:"role"`
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

	escrow, err := h.db.GetEscrow(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	addr := common.HexToAddress(escrow.EscrowAddress)
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
	Role      string `json:"role"`
	ReasonURI string `json:"reason_uri"`
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

	escrow, err := h.db.GetEscrow(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	addr := common.HexToAddress(escrow.EscrowAddress)
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

	escrow, err := h.db.GetEscrow(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	tx, err := h.chain.ResolveDispute(r.Context(), common.HexToAddress(escrow.EscrowAddress), uint16(bps), req.ResolutionURI)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("chain: %v", err)})
		return
	}

	_ = h.idx.RunOnce(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"tx_hash": tx.Hash().Hex()})
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
