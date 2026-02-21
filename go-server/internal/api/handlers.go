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

	task, err := h.db.CreateTask(req.Title, req.Description, specHash.Hex())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("db: %v", err)})
		return
	}

	milestoneCount := 1
	if len(milestones) > 0 {
		milestoneCount = len(milestones)
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
		WorkerStake:              workerStakeVal.String(),
		Token:                    tokenAddr.Hex(),
		Status:                   "created",
		SubmissionDeadline:       int64(deadline),
		ReviewPeriodSeconds:      int64(review),
		DisputePeriodSeconds:     int64(dispute),
		ArbitratorTimeoutSeconds: int64(arbTimeout),
		MilestoneCount:           milestoneCount,
		CurrentMilestone:         0,
		BackupWorker:             backupWorkerAddr.Hex(),
		BackupDeadlineExtension:  int64(backupDeadlineExt),
		ActiveWorker:             req.Worker,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("db: %v", err)})
		return
	}

	for i, m := range milestones {
		_, err := h.db.CreateMilestone(&storage.MilestoneRecord{
			EscrowID:           escrow.ID,
			MilestoneIndex:     i,
			Amount:             m.Amount.String(),
			SubmissionDeadline: int64(m.SubmissionDeadline),
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

	escrow, err := h.db.GetEscrow(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	result := map[string]any{"escrow": escrow}

	if escrow.MilestoneCount > 1 {
		milestones, err := h.db.GetMilestonesByEscrow(id)
		if err != nil {
			slog.Error("failed to fetch milestones", "escrow_id", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch milestones"})
			return
		}
		result["milestones"] = milestones
	}

	writeJSON(w, http.StatusOK, result)
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

	escrow, err := h.db.GetEscrow(id)
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
	SubmissionURI  string `json:"submission_uri"`
	MilestoneIndex *int   `json:"milestone_index,omitempty"`
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

	addr := common.HexToAddress(escrow.EscrowAddress)

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
		tx, err := h.chain.SubmitMilestone(r.Context(), addr, uint8(msIdx), hashBytes, req.SubmissionURI)
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

	escrow, err := h.db.GetEscrow(id)
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
		msIdx := uint8(msIdxVal)
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

	escrow, err := h.db.GetEscrow(id)
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
		msIdx := uint8(msIdxVal)
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

	escrow, err := h.db.GetEscrow(id)
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
		tx, err := h.chain.ResolveMilestoneDispute(r.Context(), addr, uint8(msIdxVal), uint16(bps), req.ResolutionURI)
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

	escrow, err := h.db.GetEscrow(id)
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

	escrow, err := h.db.GetEscrow(id)
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
		rep, err := h.db.GetReputation(addr, role)
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

	reps, err := h.db.GetReputationByAddress(addr)
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
