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

	// DefaultMaxConsecutiveFailures is the number of consecutive RunOnce failures
	// before the indexer considers itself fatally broken and signals via Err().
	DefaultMaxConsecutiveFailures = 5
)

// Status mappings from event names to escrow status strings.
// WorkerStakeDeposited does not change the escrow status (remains "funded").
// Milestone events don't change escrow-level status (escrow stays "funded" throughout).
var eventStatusMap = map[string]string{
	"EscrowFunded":             "funded",
	"SubmissionMade":           "submitted",
	"Approved":                 "approved",
	"Rejected":                 "disputed",
	"Disputed":                 "disputed",
	"SilenceEscalated":         "disputed",
	"DisputeResolved":          "resolved",
	"Settled":                  "settled",
	"Refunded":                 "refunded",
	"Cancelled":                "cancelled",
	"ArbitratorTimeoutClaimed": "refunded",
}

// milestoneStatusMap maps milestone event names to milestone-level status strings.
var milestoneStatusMap = map[string]string{
	"MilestoneSubmitted":        "submitted",
	"MilestoneApproved":         "approved",
	"MilestoneRejected":         "disputed",
	"MilestoneDisputed":         "disputed",
	"MilestoneSilenceEscalated": "disputed",
	"MilestoneDisputeResolved":  "resolved",
	"MilestoneSettled":          "settled",
	"MilestoneCancelled":        "cancelled",
}

// terminalStatuses are escrow-level statuses that must not be overwritten by
// generic event-based status mapping (e.g. the Settled event emitted by
// _settleWorkerStake after abortRemainingMilestones).
var terminalStatuses = map[string]bool{
	"settled":   true,
	"refunded":  true,
	"cancelled": true,
}

type Indexer struct {
	db             *storage.DB
	chain          chain.ChainClient
	factoryAddress common.Address
	chainID        int64

	pollInterval      time.Duration
	maxConsecFailures int
	fatalCh           chan error
	startBlock        uint64 // initial fromBlock when cursor is 0; overrides defaultLookback
}

// Option configures optional Indexer parameters.
type Option func(*Indexer)

// WithMaxConsecutiveFailures overrides the default consecutive failure threshold
// before the indexer signals a fatal error. Values <= 0 disable fatal signalling.
func WithMaxConsecutiveFailures(n int) Option {
	return func(idx *Indexer) {
		idx.maxConsecFailures = n
	}
}

// WithPollInterval overrides the default poll interval between indexer ticks.
func WithPollInterval(d time.Duration) Option {
	return func(idx *Indexer) {
		idx.pollInterval = d
	}
}

// WithStartBlock sets the block number to begin indexing from when no prior
// cursor exists. Use this to skip scanning blocks before contract deployment.
func WithStartBlock(block uint64) Option {
	return func(idx *Indexer) {
		idx.startBlock = block
	}
}

func New(db *storage.DB, chainClient chain.ChainClient, factoryAddress string, opts ...Option) *Indexer {
	var chainID int64
	if chainClient != nil {
		chainID = chainClient.ChainID().Int64()
	}
	idx := &Indexer{
		db:                db,
		chain:             chainClient,
		factoryAddress:    common.HexToAddress(factoryAddress),
		chainID:           chainID,
		pollInterval:      pollInterval,
		maxConsecFailures: DefaultMaxConsecutiveFailures,
		fatalCh:           make(chan error, 1),
	}
	for _, opt := range opts {
		opt(idx)
	}
	return idx
}

// Err returns a channel that receives a fatal error when the indexer has failed
// too many times consecutively. The channel is buffered (size 1) so the sender
// never blocks. Callers should select on this channel alongside context
// cancellation to detect unrecoverable indexer failures.
func (idx *Indexer) Err() <-chan error {
	return idx.fatalCh
}

func (idx *Indexer) Run(ctx context.Context) {
	ticker := time.NewTicker(idx.pollInterval)
	defer ticker.Stop()

	var consecutiveFailures int

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := idx.RunOnce(ctx); err != nil {
				consecutiveFailures++
				slog.Error("indexer poll failed",
					"error", err,
					"consecutive_failures", consecutiveFailures,
					"max_consecutive_failures", idx.maxConsecFailures,
				)
				if idx.maxConsecFailures > 0 && consecutiveFailures >= idx.maxConsecFailures {
					fatalErr := fmt.Errorf("indexer fatal: %d consecutive failures (last: %w)", consecutiveFailures, err)
					slog.Error("indexer signalling fatal error", "error", fatalErr)
					select {
					case idx.fatalCh <- fatalErr:
					default:
						// Channel already has an error queued; don't block.
					}
					return
				}
			} else {
				consecutiveFailures = 0
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
		if idx.startBlock > 0 {
			fromBlock = idx.startBlock
		} else if currentBlock > defaultLookback {
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
	// EscrowCreated(uint256 indexed escrowId, address indexed escrow, address indexed buyer, address worker, address verifier, address arbitrator, bytes32 taskSpecHash, address token)
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

	tokenAddr := common.Address{}
	if len(values) > 4 {
		if ta, ok := values[4].(common.Address); ok {
			tokenAddr = ta
		}
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
		Token:              tokenAddr.Hex(),
		Status:             "created",
		SubmissionDeadline: 0,
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

func (idx *Indexer) isTerminalStatus(dbEscrowID int64) bool {
	e, err := idx.db.GetEscrow(dbEscrowID)
	if err != nil {
		return false
	}
	return terminalStatuses[e.Status]
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

	// Update escrow status based on event (V1 single-milestone events only).
	// Skip if the escrow is already in a terminal state (e.g. "refunded" from
	// abortRemainingMilestones) to prevent the subsequent Settled event emitted
	// by _settleWorkerStake from incorrectly overwriting it to "settled".
	if newStatus, ok := eventStatusMap[event.Name]; ok {
		if !idx.isTerminalStatus(dbEscrowID) {
			if err := idx.db.UpdateEscrowStatus(dbEscrowID, newStatus); err != nil {
				return fmt.Errorf("update status to %s: %w", newStatus, err)
			}
		}
	}

	// Update milestone status for milestone-specific events
	if msStatus, ok := milestoneStatusMap[event.Name]; ok {
		msIdx, err := idx.extractMilestoneIndex(lg)
		if err != nil {
			slog.Warn("failed to extract milestone index", "event", event.Name, "error", err)
		} else {
			if err := idx.db.UpdateMilestoneStatus(dbEscrowID, msIdx, msStatus); err != nil {
				slog.Warn("failed to update milestone status", "escrow_id", dbEscrowID, "milestone", msIdx, "status", msStatus, "error", err)
			} else if msStatus == "approved" || msStatus == "resolved" || msStatus == "settled" || msStatus == "cancelled" {
				if err := idx.db.UpdateEscrowMilestoneProgress(dbEscrowID, msIdx+1); err != nil {
					slog.Warn("failed to advance current_milestone", "escrow_id", dbEscrowID, "next_milestone", msIdx+1, "error", err)
				}
			}
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
	case "MilestoneSubmitted":
		return idx.handleMilestoneSubmission(lg, dbEscrowID)
	case "MilestoneDisputed", "MilestoneRejected", "MilestoneSilenceEscalated":
		return idx.handleMilestoneDispute(lg, dbEscrowID, event.Name)
	case "MilestoneDisputeResolved":
		return idx.handleMilestoneDisputeResolved(lg, dbEscrowID)
	case "RemainingMilestonesAborted":
		return idx.handleRemainingMilestonesAborted(lg, dbEscrowID)
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

// extractMilestoneIndex reads the milestone index from the first indexed topic
// (topic[1]) of milestone events. All milestone events use uint8 indexed milestoneIndex.
func (idx *Indexer) extractMilestoneIndex(lg types.Log) (int, error) {
	if len(lg.Topics) < 2 {
		return 0, fmt.Errorf("insufficient topics for milestone index (got %d)", len(lg.Topics))
	}
	msIdx := new(big.Int).SetBytes(lg.Topics[1].Bytes())
	if !msIdx.IsInt64() || msIdx.Int64() > 255 {
		return 0, fmt.Errorf("milestone index out of range: %s", msIdx.String())
	}
	return int(msIdx.Int64()), nil
}

func (idx *Indexer) handleMilestoneSubmission(lg types.Log, dbEscrowID int64) error {
	msIdx, err := idx.extractMilestoneIndex(lg)
	if err != nil {
		return err
	}

	values, err := chain.EscrowABI.Events["MilestoneSubmitted"].Inputs.NonIndexed().Unpack(lg.Data)
	if err != nil {
		return fmt.Errorf("unpack MilestoneSubmitted: %w", err)
	}
	if len(values) < 2 {
		return fmt.Errorf("MilestoneSubmitted: expected 2 values, got %d", len(values))
	}

	hashBytes, ok := values[0].([32]byte)
	if !ok {
		return fmt.Errorf("MilestoneSubmitted: unexpected type for submissionHash: %T", values[0])
	}
	submissionURI, ok := values[1].(string)
	if !ok {
		return fmt.Errorf("MilestoneSubmitted: unexpected type for submissionURI: %T", values[1])
	}

	submissionHash := fmt.Sprintf("0x%x", hashBytes)
	return idx.db.UpdateMilestoneSubmission(dbEscrowID, msIdx, submissionHash, submissionURI)
}

func (idx *Indexer) handleMilestoneDispute(lg types.Log, dbEscrowID int64, eventName string) error {
	msIdx, err := idx.extractMilestoneIndex(lg)
	if err != nil {
		return err
	}

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

	raisedBy := ""
	if len(lg.Topics) >= 3 {
		raisedBy = common.BytesToAddress(lg.Topics[2].Bytes()).Hex()
	}

	slog.Info("milestone dispute event",
		"escrow_id", dbEscrowID, "milestone", msIdx,
		"event", eventName, "raised_by", raisedBy, "reason_uri", reasonURI)

	_, err = idx.db.CreateDispute(dbEscrowID, raisedBy, reasonURI)
	return err
}

func (idx *Indexer) handleMilestoneDisputeResolved(lg types.Log, dbEscrowID int64) error {
	msIdx, err := idx.extractMilestoneIndex(lg)
	if err != nil {
		return err
	}

	values, err := chain.EscrowABI.Events["MilestoneDisputeResolved"].Inputs.NonIndexed().Unpack(lg.Data)
	if err != nil {
		return fmt.Errorf("unpack MilestoneDisputeResolved: %w", err)
	}
	if len(values) < 2 {
		return fmt.Errorf("MilestoneDisputeResolved: expected 2 values, got %d", len(values))
	}

	workerAwardBps, ok := values[0].(uint16)
	if !ok {
		return fmt.Errorf("MilestoneDisputeResolved: unexpected type for workerAwardBps: %T", values[0])
	}
	resolutionURI, ok := values[1].(string)
	if !ok {
		return fmt.Errorf("MilestoneDisputeResolved: unexpected type for resolutionURI: %T", values[1])
	}

	slog.Info("milestone dispute resolved",
		"escrow_id", dbEscrowID, "milestone", msIdx,
		"worker_award_bps", workerAwardBps, "resolution_uri", resolutionURI)

	// Update the most recent dispute for this escrow
	dispute, err := idx.db.GetDisputeByEscrowID(dbEscrowID)
	if err != nil {
		_, createErr := idx.db.CreateDispute(dbEscrowID, "arbitrator", resolutionURI)
		if createErr != nil {
			return createErr
		}
		dispute, err = idx.db.GetDisputeByEscrowID(dbEscrowID)
		if err != nil {
			return fmt.Errorf("get newly created dispute: %w", err)
		}
	}

	return idx.db.UpdateDispute(dispute.ID, resolutionURI, int(workerAwardBps))
}

func (idx *Indexer) handleRemainingMilestonesAborted(lg types.Log, dbEscrowID int64) error {
	values, err := chain.EscrowABI.Events["RemainingMilestonesAborted"].Inputs.Unpack(lg.Data)
	if err != nil {
		return fmt.Errorf("unpack RemainingMilestonesAborted: %w", err)
	}
	if len(values) < 2 {
		return fmt.Errorf("RemainingMilestonesAborted: expected 2 values, got %d", len(values))
	}

	fromIndex, ok := values[0].(uint8)
	if !ok {
		return fmt.Errorf("RemainingMilestonesAborted: unexpected type for fromIndex: %T", values[0])
	}

	slog.Info("remaining milestones aborted", "escrow_id", dbEscrowID, "from_index", fromIndex)

	// Mark all milestones from fromIndex onward as cancelled
	milestones, err := idx.db.GetMilestonesByEscrow(dbEscrowID)
	if err != nil {
		return fmt.Errorf("get milestones: %w", err)
	}
	for _, ms := range milestones {
		if ms.MilestoneIndex >= int(fromIndex) {
			if err := idx.db.UpdateMilestoneStatus(dbEscrowID, ms.MilestoneIndex, "cancelled"); err != nil {
				slog.Warn("failed to cancel milestone", "escrow_id", dbEscrowID, "milestone", ms.MilestoneIndex, "error", err)
			}
		}
	}

	// The contract sets status = Status.Refunded after abort. Set this before
	// the subsequent Settled event (from _settleWorkerStake) is processed, so
	// the terminal-state guard in processEscrowLog prevents overwrite.
	if err := idx.db.UpdateEscrowStatus(dbEscrowID, "refunded"); err != nil {
		return fmt.Errorf("set escrow refunded after abort: %w", err)
	}

	return nil
}
