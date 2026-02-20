package indexer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

const (
	cursorKey       = "indexer"
	defaultLookback = 250
	pollInterval    = 15 * time.Second
)

// Status mappings from event names to escrow status strings
var eventStatusMap = map[string]string{
	"EscrowFunded":            "funded",
	"SubmissionMade":          "submitted",
	"Approved":                "approved",
	"Rejected":                "disputed",
	"Disputed":                "disputed",
	"SilenceEscalated":        "disputed",
	"DisputeResolved":         "resolved",
	"Settled":                 "settled",
	"Refunded":                "refunded",
	"Cancelled":               "cancelled",
	"ArbitratorTimeoutClaimed": "refunded",
}

type Indexer struct {
	db             *storage.DB
	chain          chain.ChainClient
	factoryAddress common.Address
	chainID        int64
}

func New(db *storage.DB, chainClient chain.ChainClient, factoryAddress string) *Indexer {
	var chainID int64
	if chainClient != nil {
		chainID = chainClient.ChainID().Int64()
	}
	return &Indexer{
		db:             db,
		chain:          chainClient,
		factoryAddress: common.HexToAddress(factoryAddress),
		chainID:        chainID,
	}
}

func (idx *Indexer) Run(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := idx.RunOnce(ctx); err != nil {
				slog.Error("indexer poll failed", "error", err)
			}
		}
	}
}

func (idx *Indexer) RunOnce(ctx context.Context) error {
	if idx.chain == nil {
		return nil
	}

	currentBlock, err := idx.chain.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("get block number: %w", err)
	}

	cursor, err := idx.db.GetCursor(idx.chainID, cursorKey)
	if err != nil {
		return fmt.Errorf("get cursor: %w", err)
	}

	fromBlock := uint64(cursor)
	if fromBlock == 0 {
		if currentBlock > defaultLookback {
			fromBlock = currentBlock - defaultLookback
		}
	} else {
		fromBlock++ // Start after last processed block
	}

	if fromBlock > currentBlock {
		return nil
	}

	// Index factory EscrowCreated events
	if err := idx.indexFactoryEvents(ctx, fromBlock, currentBlock); err != nil {
		return fmt.Errorf("index factory events: %w", err)
	}

	// Index escrow events for escrows on this chain only
	escrows, err := idx.db.ListEscrowsByChainID(idx.chainID)
	if err != nil {
		return fmt.Errorf("list escrows: %w", err)
	}

	for _, e := range escrows {
		if err := idx.indexEscrowEvents(ctx, common.HexToAddress(e.EscrowAddress), e.ID, fromBlock, currentBlock); err != nil {
			slog.Warn("escrow event indexing failed", "escrow_address", e.EscrowAddress, "error", err)
		}
	}

	// Update cursor
	if err := idx.db.SetCursor(idx.chainID, cursorKey, int64(currentBlock)); err != nil {
		return fmt.Errorf("set cursor: %w", err)
	}

	return nil
}

func (idx *Indexer) indexFactoryEvents(ctx context.Context, from, to uint64) error {
	eventID := chain.FactoryABI.Events["EscrowCreated"].ID
	logs, err := idx.chain.FilterLogs(ctx, []common.Address{idx.factoryAddress}, [][]common.Hash{{eventID}}, from, to)
	if err != nil {
		return fmt.Errorf("filter factory logs: %w", err)
	}

	for _, lg := range logs {
		if err := idx.processFactoryLog(lg); err != nil {
			slog.Warn("factory log processing failed", "tx_hash", lg.TxHash.Hex(), "log_index", lg.Index, "error", err)
		}
	}
	return nil
}

func (idx *Indexer) processFactoryLog(lg types.Log) error {
	exists, err := idx.db.ChainLogExists(lg.TxHash.Hex(), int(lg.Index))
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	event, err := chain.FactoryABI.EventByID(lg.Topics[0])
	if err != nil {
		return fmt.Errorf("unknown event: %w", err)
	}

	rawData, err := json.Marshal(lg)
	if err != nil {
		return fmt.Errorf("marshal factory log: %w", err)
	}
	if err := idx.db.CreateChainLog(lg.TxHash.Hex(), int(lg.Index), int64(lg.BlockNumber), event.Name, lg.Address.Hex(), string(rawData)); err != nil {
		return err
	}

	if event.Name == "EscrowCreated" {
		return idx.handleEscrowCreated(lg)
	}
	return nil
}

func (idx *Indexer) handleEscrowCreated(lg types.Log) error {
	// EscrowCreated(uint256 indexed escrowId, address indexed escrow, address indexed buyer, address worker, address verifier, address arbitrator, bytes32 taskSpecHash)
	if len(lg.Topics) < 4 {
		return fmt.Errorf("insufficient topics")
	}

	escrowIDBig := new(big.Int).SetBytes(lg.Topics[1].Bytes())
	if !escrowIDBig.IsInt64() {
		return fmt.Errorf("escrowID overflows int64: %s", escrowIDBig.String())
	}
	escrowID := escrowIDBig.Int64()
	escrowAddr := common.BytesToAddress(lg.Topics[2].Bytes())

	// Non-indexed params are in data
	values, err := chain.FactoryABI.Events["EscrowCreated"].Inputs.NonIndexed().Unpack(lg.Data)
	if err != nil {
		return fmt.Errorf("unpack EscrowCreated: %w", err)
	}

	worker, ok := values[0].(common.Address)
	if !ok {
		return fmt.Errorf("unexpected type for worker: %T", values[0])
	}
	verifierAddr, ok := values[1].(common.Address)
	if !ok {
		return fmt.Errorf("unexpected type for verifier: %T", values[1])
	}
	arbitratorAddr, ok := values[2].(common.Address)
	if !ok {
		return fmt.Errorf("unexpected type for arbitrator: %T", values[2])
	}
	taskSpecHash, ok := values[3].([32]byte)
	if !ok {
		return fmt.Errorf("unexpected type for taskSpecHash: %T", values[3])
	}

	buyer := common.BytesToAddress(lg.Topics[3].Bytes())

	// Check if escrow already exists (e.g. created via API/MCP handler with on-chain fields already set)
	_, err = idx.db.GetEscrowByAddress(escrowAddr.Hex())
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check existing escrow %s: %w", escrowAddr.Hex(), err)
	}

	// Escrow was created externally (not via our handlers) -- index it from the event.
	// Amount is stored as "0" because the EscrowCreated event does not include the funded
	// amount and the ChainClient interface does not expose BalanceAt. The correct amount
	// will be set when the indexer later processes the EscrowFunded event for this escrow.
	task, err := idx.db.CreateTask("Indexed task", "", fmt.Sprintf("0x%x", taskSpecHash))
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}

	_, err = idx.db.CreateEscrow(&storage.Escrow{
		TaskID:             task.ID,
		ChainID:            idx.chainID,
		FactoryAddress:     idx.factoryAddress.Hex(),
		EscrowAddress:      escrowAddr.Hex(),
		EscrowID:           escrowID,
		Buyer:              buyer.Hex(),
		Worker:             worker.Hex(),
		Verifier:           verifierAddr.Hex(),
		Arbitrator:         arbitratorAddr.Hex(),
		Amount:             "0",
		Status:             "created",
		SubmissionDeadline: "0",
	})
	return err
}

func (idx *Indexer) indexEscrowEvents(ctx context.Context, escrowAddr common.Address, dbEscrowID int64, from, to uint64) error {
	logs, err := idx.chain.FilterLogs(ctx, []common.Address{escrowAddr}, nil, from, to)
	if err != nil {
		return fmt.Errorf("filter escrow logs: %w", err)
	}

	for _, lg := range logs {
		if err := idx.processEscrowLog(lg, dbEscrowID); err != nil {
			slog.Warn("escrow log processing failed", "tx_hash", lg.TxHash.Hex(), "log_index", lg.Index, "error", err)
		}
	}
	return nil
}

func (idx *Indexer) processEscrowLog(lg types.Log, dbEscrowID int64) error {
	exists, err := idx.db.ChainLogExists(lg.TxHash.Hex(), int(lg.Index))
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	event, err := chain.EscrowABI.EventByID(lg.Topics[0])
	if err != nil {
		return nil // Unknown event, skip
	}

	rawData, err := json.Marshal(lg)
	if err != nil {
		return fmt.Errorf("marshal escrow log: %w", err)
	}
	if err := idx.db.CreateChainLog(lg.TxHash.Hex(), int(lg.Index), int64(lg.BlockNumber), event.Name, lg.Address.Hex(), string(rawData)); err != nil {
		return err
	}

	// Update escrow status based on event
	if newStatus, ok := eventStatusMap[event.Name]; ok {
		if err := idx.db.UpdateEscrowStatus(dbEscrowID, newStatus); err != nil {
			return fmt.Errorf("update status to %s: %w", newStatus, err)
		}
	}

	// Handle specific events
	switch event.Name {
	case "SubmissionMade":
		return idx.handleSubmission(lg, dbEscrowID)
	case "Disputed", "Rejected", "SilenceEscalated":
		return idx.handleDispute(lg, dbEscrowID, event.Name)
	case "DisputeResolved":
		return idx.handleDisputeResolved(lg, dbEscrowID)
	}

	return nil
}

func (idx *Indexer) handleSubmission(lg types.Log, dbEscrowID int64) error {
	values, err := chain.EscrowABI.Events["SubmissionMade"].Inputs.NonIndexed().Unpack(lg.Data)
	if err != nil {
		return fmt.Errorf("unpack SubmissionMade: %w", err)
	}

	if len(values) < 2 {
		return fmt.Errorf("SubmissionMade: expected 2 values, got %d", len(values))
	}
	hashBytes, ok := values[0].([32]byte)
	if !ok {
		return fmt.Errorf("SubmissionMade: unexpected type for submissionHash: %T", values[0])
	}
	submissionURI, ok := values[1].(string)
	if !ok {
		return fmt.Errorf("SubmissionMade: unexpected type for submissionURI: %T", values[1])
	}

	submissionHash := fmt.Sprintf("0x%x", hashBytes)
	_, err = idx.db.CreateSubmission(dbEscrowID, submissionHash, submissionURI)
	return err
}

func (idx *Indexer) handleDispute(lg types.Log, dbEscrowID int64, eventName string) error {
	if len(lg.Topics) < 2 {
		return fmt.Errorf("handleDispute: insufficient topics (got %d, need >= 2)", len(lg.Topics))
	}
	raisedBy := common.BytesToAddress(lg.Topics[1].Bytes()).Hex()
	reasonURI := ""

	eventDef := chain.EscrowABI.Events[eventName]
	nonIndexed := eventDef.Inputs.NonIndexed()
	if len(nonIndexed) > 0 {
		values, err := nonIndexed.Unpack(lg.Data)
		if err == nil {
			for i, input := range nonIndexed {
				if strings.Contains(strings.ToLower(input.Name), "uri") || strings.Contains(strings.ToLower(input.Name), "reason") {
					if s, ok := values[i].(string); ok {
						reasonURI = s
						break
					}
				}
			}
		}
	}

	_, err := idx.db.CreateDispute(dbEscrowID, raisedBy, reasonURI)
	return err
}

func (idx *Indexer) handleDisputeResolved(lg types.Log, dbEscrowID int64) error {
	values, err := chain.EscrowABI.Events["DisputeResolved"].Inputs.NonIndexed().Unpack(lg.Data)
	if err != nil {
		return fmt.Errorf("unpack DisputeResolved: %w", err)
	}

	if len(values) < 2 {
		return fmt.Errorf("DisputeResolved: expected 2 values, got %d", len(values))
	}
	workerAwardBps, ok := values[0].(uint16)
	if !ok {
		return fmt.Errorf("DisputeResolved: unexpected type for workerAwardBps: %T", values[0])
	}
	resolutionURI, ok := values[1].(string)
	if !ok {
		return fmt.Errorf("DisputeResolved: unexpected type for resolutionURI: %T", values[1])
	}

	dispute, err := idx.db.GetDisputeByEscrowID(dbEscrowID)
	if err != nil {
		slog.Warn("no existing dispute found for resolution, creating new record",
			"escrow_id", dbEscrowID, "error", err)
		_, createErr := idx.db.CreateDispute(dbEscrowID, "arbitrator", resolutionURI)
		if createErr != nil {
			return createErr
		}
		// Re-fetch to get the ID for the update call
		dispute, err = idx.db.GetDisputeByEscrowID(dbEscrowID)
		if err != nil {
			return fmt.Errorf("get newly created dispute: %w", err)
		}
	}

	return idx.db.UpdateDispute(dispute.ID, resolutionURI, int(workerAwardBps))
}
