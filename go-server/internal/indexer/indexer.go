package indexer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/events"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/numconv"
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
	"EmergencyResolved":        "resolved",
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
	confirmations     uint64 // blocks to trail the head; 0 indexes the head
	bus               *events.EventBus

	// logHook, when set, runs after dedup and before a log is applied.
	// Tests use it to inject a one-shot handler failure. Production leaves it nil.
	logHook func(types.Log) error

	runMu      sync.Mutex
	running    bool
	runWaiters []runWaiter
}

// runWaiter is a RunOnce caller that arrived while a pass was already running.
// It waits for a follow-up pass that starts after it arrived.
type runWaiter struct {
	ctx context.Context
	ch  chan error
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

// WithEventBus attaches an event bus to the indexer so that processed on-chain
// events are published for real-time streaming to connected clients.
func WithEventBus(bus *events.EventBus) Option {
	return func(idx *Indexer) {
		idx.bus = bus
	}
}

// WithConfirmations sets how many blocks behind the chain head the indexer
// stays. Zero, the default, indexes the head. A reorg of a block at or below
// the cursor cannot happen once confirmations is deep enough for the chain;
// 12 is the recommended value on Base mainnet.
func WithConfirmations(n uint64) Option {
	return func(idx *Indexer) {
		idx.confirmations = n
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

// DB returns the indexer's storage handle, allowing other components (e.g. the
// webhook handler) to query escrow state when routing incoming events.
func (idx *Indexer) DB() *storage.DB {
	return idx.db
}

// FactoryAddress returns the factory contract address the indexer monitors.
func (idx *Indexer) FactoryAddress() common.Address {
	return idx.factoryAddress
}

// ChainID returns the chain ID the indexer is configured for.
func (idx *Indexer) ChainID() int64 {
	return idx.chainID
}

// Bus returns the indexer's event bus, if one was configured.
func (idx *Indexer) Bus() *events.EventBus {
	return idx.bus
}

type publishBatch struct {
	events []events.Event
}

type publishCtxKey struct{}

func withPublishBatch(ctx context.Context, batch *publishBatch) context.Context {
	return context.WithValue(ctx, publishCtxKey{}, batch)
}

func publishBatchFrom(ctx context.Context) *publishBatch {
	batch, ok := ctx.Value(publishCtxKey{}).(*publishBatch)
	if !ok {
		return nil
	}
	return batch
}

// publishEvent publishes a lifecycle event to the event bus if configured.
// Events produced inside a block transaction are held until that transaction
// commits, so a rollback does not announce state that was never stored.
func (idx *Indexer) publishEvent(ctx context.Context, onChainName string, escrowAddr string, lg types.Log) {
	if idx.bus == nil {
		return
	}
	streamName, ok := events.OnChainEventName[onChainName]
	if !ok {
		return
	}
	ev := events.Event{
		Name:      streamName,
		Escrow:    escrowAddr,
		Level:     events.L1,
		Block:     lg.BlockNumber,
		Timestamp: time.Now().UTC(),
		ID:        fmt.Sprintf("%s-%d", lg.TxHash.Hex(), lg.Index),
	}
	if batch := publishBatchFrom(ctx); batch != nil {
		batch.events = append(batch.events, ev)
		return
	}
	idx.bus.Publish(ev)
}

func (idx *Indexer) flushPublished(batch *publishBatch) {
	if idx.bus == nil || batch == nil {
		return
	}
	for _, ev := range batch.events {
		idx.bus.Publish(ev)
	}
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

// RunOnce indexes blocks up to head-confirmations.
//
// Concurrent calls never overlap. A caller that arrives during a pass waits
// for a follow-up pass that reads the chain after the caller arrived, so a
// transaction mined before RunOnce is visible in the database when it returns.
// A handler error rolls back the failed block and leaves the cursor in front
// of that block; the next pass retries it. Run counts that error toward
// max consecutive failures.
func (idx *Indexer) RunOnce(ctx context.Context) error {
	idx.runMu.Lock()
	if idx.running {
		ch := make(chan error, 1)
		idx.runWaiters = append(idx.runWaiters, runWaiter{ctx: ctx, ch: ch})
		idx.runMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-ch:
			return err
		}
	}
	idx.running = true
	idx.runMu.Unlock()

	return idx.finishRun(ctx, idx.runOnceBody(ctx))
}

func (idx *Indexer) finishRun(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	for {
		idx.runMu.Lock()
		waiters := idx.runWaiters
		idx.runWaiters = nil
		if len(waiters) == 0 {
			idx.running = false
			idx.runMu.Unlock()
			return err
		}
		idx.runMu.Unlock()

		followCtx, cancel := contextFromWaiters(waiters)
		followErr := idx.runOnceBody(followCtx)
		if followErr == nil && followCtx.Err() != nil {
			followErr = followCtx.Err()
		}
		cancel()
		for _, w := range waiters {
			w.ch <- followErr
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		} else {
			err = followErr
		}
	}
}

func contextFromWaiters(waiters []runWaiter) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	var alive atomic.Int64
	alive.Store(int64(len(waiters)))
	for _, w := range waiters {
		go func(wctx context.Context) {
			select {
			case <-wctx.Done():
				if alive.Add(-1) == 0 {
					cancel()
				}
			case <-ctx.Done():
			}
		}(w.ctx)
	}
	return ctx, cancel
}

func (idx *Indexer) runOnceBody(ctx context.Context) error {
	if idx.chain == nil {
		return nil
	}

	head, err := idx.chain.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("get block number: %w", err)
	}
	safeHead, ok := idx.confirmedHead(head)
	if !ok {
		return nil
	}

	cursor, err := idx.db.GetCursor(ctx, idx.chainID, cursorKey)
	if err != nil {
		return fmt.Errorf("get cursor: %w", err)
	}

	fromBlock, err := numconv.Int64ToUint64(cursor, "cursor")
	if err != nil {
		return fmt.Errorf("invalid cursor value: %w", err)
	}
	if fromBlock == 0 {
		if idx.startBlock > 0 {
			fromBlock = idx.startBlock
		} else if safeHead > defaultLookback {
			fromBlock = safeHead - defaultLookback
		}
	} else {
		fromBlock++ // Start after last processed block
	}

	if fromBlock > safeHead {
		return nil
	}
	if safeHead > math.MaxInt64 {
		return fmt.Errorf("block number %d exceeds int64 max", safeHead)
	}

	return idx.applyRange(ctx, fromBlock, safeHead)
}

// confirmedHead is the highest block that has confirmations blocks on top of
// it. ok is false when the chain is not yet that deep.
func (idx *Indexer) confirmedHead(head uint64) (uint64, bool) {
	if head < idx.confirmations {
		return 0, false
	}
	return head - idx.confirmations, true
}

// errEscrowLogsNotLoaded asks applyRange to fetch logs for escrows created in
// the current block and retry that block. The open transaction is rolled back.
var errEscrowLogsNotLoaded = errors.New("escrow logs not yet loaded")

type escrowLogIndex map[string]map[uint64][]types.Log

func logsByBlock(logs []types.Log) map[uint64][]types.Log {
	out := make(map[uint64][]types.Log)
	for _, lg := range logs {
		out[lg.BlockNumber] = append(out[lg.BlockNumber], lg)
	}
	return out
}

func factoryLogTopics() [][]common.Hash {
	names := []string{
		"EscrowCreated",
		"OutcomeRecorded",
		"MarketStabilityFeeApplied",
		"AddressFrozen",
		"AddressUnfrozen",
		"EscrowFrozen",
		"EscrowUnfrozen",
		"EmergencyResolved",
	}
	ids := make([]common.Hash, 0, len(names))
	for _, name := range names {
		ids = append(ids, chain.FactoryABI.Events[name].ID)
	}
	return [][]common.Hash{ids}
}

func (idx *Indexer) prefetchLogs(ctx context.Context, from, to uint64) (map[uint64][]types.Log, escrowLogIndex, error) {
	factoryLogs, err := idx.chain.FilterLogs(ctx, []common.Address{idx.factoryAddress}, factoryLogTopics(), from, to)
	if err != nil {
		return nil, nil, fmt.Errorf("filter factory logs: %w", err)
	}
	escrows, err := idx.db.ListEscrowsByChainID(ctx, idx.chainID)
	if err != nil {
		return nil, nil, fmt.Errorf("list escrows: %w", err)
	}
	byEscrow := make(escrowLogIndex, len(escrows))
	for _, e := range escrows {
		if err := idx.loadEscrowLogs(ctx, e.EscrowAddress, from, to, byEscrow); err != nil {
			return nil, nil, err
		}
	}
	return logsByBlock(factoryLogs), byEscrow, nil
}

func (idx *Indexer) loadEscrowLogs(ctx context.Context, addr string, from, to uint64, dest escrowLogIndex) error {
	logs, err := idx.chain.FilterLogs(ctx, []common.Address{common.HexToAddress(addr)}, nil, from, to)
	if err != nil {
		return fmt.Errorf("filter escrow logs %s: %w", addr, err)
	}
	dest[strings.ToLower(addr)] = logsByBlock(logs)
	return nil
}

func nextLogBlock(from uint64, factory map[uint64][]types.Log, escrows escrowLogIndex) (uint64, bool) {
	var next uint64
	found := false
	consider := func(block uint64, n int) {
		if n == 0 || block < from {
			return
		}
		if !found || block < next {
			next = block
			found = true
		}
	}
	for block, logs := range factory {
		consider(block, len(logs))
	}
	for _, byBlock := range escrows {
		for block, logs := range byBlock {
			consider(block, len(logs))
		}
	}
	return next, found
}

// applyRange applies logs from through to. Empty stretches advance the
// cursor in one transaction. A block that contains logs is its own transaction
// together with the cursor update for that block. A handler error leaves the
// cursor on the last block that fully committed.
func (idx *Indexer) applyRange(ctx context.Context, from, to uint64) error {
	factoryByBlock, escrowLogs, err := idx.prefetchLogs(ctx, from, to)
	if err != nil {
		return err
	}

	block := from
	refetch := 0
	for block <= to {
		next, ok := nextLogBlock(block, factoryByBlock, escrowLogs)
		if !ok || next > to {
			return idx.advanceCursor(ctx, to)
		}
		if next > block {
			if err := idx.advanceCursor(ctx, next-1); err != nil {
				return err
			}
			block = next
		}

		missing, batch, err := idx.applyBlock(ctx, block, factoryByBlock, escrowLogs)
		if err != nil {
			return fmt.Errorf("apply block %d: %w", block, err)
		}
		if len(missing) > 0 {
			refetch++
			if refetch > 3 {
				return fmt.Errorf("apply block %d: escrow log prefetch did not settle", block)
			}
			for _, addr := range missing {
				if err := idx.loadEscrowLogs(ctx, addr, block, to, escrowLogs); err != nil {
					return err
				}
			}
			continue
		}
		refetch = 0
		idx.flushPublished(batch)
		if block == to {
			return nil
		}
		block++
	}
	return nil
}

func (idx *Indexer) advanceCursor(ctx context.Context, block uint64) error {
	if block > math.MaxInt64 {
		return fmt.Errorf("block number %d exceeds int64 max", block)
	}
	cursor := int64(block)
	err := idx.db.RunInImmediateTx(ctx, func(txCtx context.Context) error {
		if err := idx.db.SetCursor(txCtx, idx.chainID, cursorKey, cursor); err != nil {
			return fmt.Errorf("set cursor: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("advance cursor to %d: %w", block, err)
	}
	return nil
}

func (idx *Indexer) applyBlock(
	ctx context.Context,
	block uint64,
	factoryByBlock map[uint64][]types.Log,
	escrowLogs escrowLogIndex,
) ([]string, *publishBatch, error) {
	if block > math.MaxInt64 {
		return nil, nil, fmt.Errorf("block number %d exceeds int64 max", block)
	}
	cursor := int64(block)
	var missing []string
	var batch *publishBatch
	err := idx.db.RunInImmediateTx(ctx, func(txCtx context.Context) error {
		batch = &publishBatch{}
		txCtx = withPublishBatch(txCtx, batch)
		for _, lg := range factoryByBlock[block] {
			if err := idx.ProcessFactoryLog(txCtx, lg); err != nil {
				return fmt.Errorf("factory log %s:%d: %w", lg.TxHash.Hex(), lg.Index, err)
			}
		}

		escrows, err := idx.db.ListEscrowsByChainID(txCtx, idx.chainID)
		if err != nil {
			return fmt.Errorf("list escrows: %w", err)
		}
		var notLoaded []string
		for _, e := range escrows {
			if _, ok := escrowLogs[strings.ToLower(e.EscrowAddress)]; !ok {
				notLoaded = append(notLoaded, e.EscrowAddress)
			}
		}
		if len(notLoaded) > 0 {
			missing = notLoaded
			return errEscrowLogsNotLoaded
		}

		for _, e := range escrows {
			key := strings.ToLower(e.EscrowAddress)
			for _, lg := range escrowLogs[key][block] {
				if err := idx.ProcessEscrowLog(txCtx, lg, e.ID); err != nil {
					return fmt.Errorf("escrow %s log %s:%d: %w", e.EscrowAddress, lg.TxHash.Hex(), lg.Index, err)
				}
			}
		}
		if err := idx.db.SetCursor(txCtx, idx.chainID, cursorKey, cursor); err != nil {
			return fmt.Errorf("set cursor: %w", err)
		}
		return nil
	})
	if errors.Is(err, errEscrowLogsNotLoaded) {
		return missing, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return nil, batch, nil
}

func (idx *Indexer) guardLog(lg types.Log) error {
	if idx.logHook == nil {
		return nil
	}
	return idx.logHook(lg)
}

// ProcessFactoryLog deduplicates, stores, and dispatches a single factory event log.
// Exported so the CDP webhook handler can reuse the same processing pipeline.
func (idx *Indexer) ProcessFactoryLog(ctx context.Context, lg types.Log) error {
	logIndex, err := numconv.UintToInt(lg.Index, "log index")
	if err != nil {
		return err
	}
	exists, err := idx.db.ChainLogExists(ctx, lg.TxHash.Hex(), logIndex)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if err := idx.guardLog(lg); err != nil {
		return err
	}

	event, err := chain.FactoryABI.EventByID(lg.Topics[0])
	if err != nil {
		return fmt.Errorf("unknown event: %w", err)
	}

	rawData, err := json.Marshal(lg)
	if err != nil {
		return fmt.Errorf("marshal factory log: %w", err)
	}
	blockNumber, err := numconv.Uint64ToInt64(lg.BlockNumber, "block number")
	if err != nil {
		return err
	}
	if err := idx.db.CreateChainLog(ctx, lg.TxHash.Hex(), logIndex, blockNumber, event.Name, lg.Address.Hex(), string(rawData)); err != nil {
		return err
	}

	var handlerErr error
	switch event.Name {
	case "EscrowCreated":
		handlerErr = idx.handleEscrowCreated(ctx, lg)
	case "OutcomeRecorded":
		handlerErr = idx.handleOutcomeRecorded(ctx, lg)
	case "AddressFrozen":
		handlerErr = idx.handleAddressFrozen(ctx, lg, true)
	case "AddressUnfrozen":
		handlerErr = idx.handleAddressFrozen(ctx, lg, false)
	case "EscrowFrozen":
		handlerErr = idx.handleEscrowFrozenFactory(ctx, lg, true)
	case "EscrowUnfrozen":
		handlerErr = idx.handleEscrowFrozenFactory(ctx, lg, false)
	case "EmergencyResolved":
		handlerErr = idx.handleEmergencyResolvedFactory(ctx, lg)
	}
	if handlerErr == nil {
		escrowAddr := ""
		if event.Name == "EscrowCreated" && len(lg.Topics) >= 3 {
			escrowAddr = common.BytesToAddress(lg.Topics[2].Bytes()).Hex()
		}
		idx.publishEvent(ctx, event.Name, escrowAddr, lg)
	}
	return handlerErr
}

func (idx *Indexer) handleEscrowCreated(ctx context.Context, lg types.Log) error {
	// EscrowCreated(
	//   uint256 indexed escrowId, address indexed escrow, address indexed buyer,
	//   address worker, uint8 quorumThreshold, uint8 quorumVerifierCount, address arbitrator,
	//   bytes32 taskSpecHash, address token, uint8 serviceTier, address zkVerifier, bytes32 circuitId
	// )
	if len(lg.Topics) < 4 {
		return errors.New("insufficient topics")
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
	if len(values) < 9 {
		return fmt.Errorf("EscrowCreated: expected 9 non-indexed values, got %d", len(values))
	}

	worker, ok := values[0].(common.Address)
	if !ok {
		return fmt.Errorf("unexpected type for worker: %T", values[0])
	}
	quorumThreshold, ok := values[1].(uint8)
	if !ok {
		return fmt.Errorf("unexpected type for quorumThreshold: %T", values[1])
	}
	quorumVerifierCount, ok := values[2].(uint8)
	if !ok {
		return fmt.Errorf("unexpected type for quorumVerifierCount: %T", values[2])
	}
	arbitratorAddr, ok := values[3].(common.Address)
	if !ok {
		return fmt.Errorf("unexpected type for arbitrator: %T", values[3])
	}
	taskSpecHash, ok := values[4].([32]byte)
	if !ok {
		return fmt.Errorf("unexpected type for taskSpecHash: %T", values[4])
	}
	tokenAddr, ok := values[5].(common.Address)
	if !ok {
		return fmt.Errorf("unexpected type for token: %T", values[5])
	}
	serviceTierRaw, ok := values[6].(uint8)
	if !ok {
		return fmt.Errorf("unexpected type for serviceTier: %T", values[6])
	}
	serviceTier := int(serviceTierRaw)
	zkVerifierAddr, ok := values[7].(common.Address)
	if !ok {
		return fmt.Errorf("unexpected type for zkVerifier: %T", values[7])
	}
	circuitID, ok := values[8].([32]byte)
	if !ok {
		return fmt.Errorf("unexpected type for circuitId: %T", values[8])
	}

	buyer := common.BytesToAddress(lg.Topics[3].Bytes())
	verifierPanelJSON := ""
	primaryVerifier := ""
	verifierStakeStr := ""

	panelCallData, packErr := chain.EscrowABI.Pack("getQuorumPanel")
	if packErr == nil {
		if rawPanel, callErr := idx.chain.CallContract(ctx, escrowAddr, panelCallData); callErr == nil {
			if unpacked, unpackErr := chain.EscrowABI.Unpack("getQuorumPanel", rawPanel); unpackErr == nil && len(unpacked) == 1 {
				if panel, ok := unpacked[0].([]common.Address); ok {
					lowerPanel := make([]string, len(panel))
					for i, addr := range panel {
						lowerPanel[i] = strings.ToLower(addr.Hex())
					}
					if len(lowerPanel) > 0 {
						primaryVerifier = lowerPanel[0]
					}
					if b, marshalErr := json.Marshal(lowerPanel); marshalErr == nil {
						verifierPanelJSON = string(b)
					}
				}
			}
		}
	}
	if stakeCallData, packErr := chain.EscrowABI.Pack("verifierStakePerVerifier"); packErr == nil {
		if rawStake, callErr := idx.chain.CallContract(ctx, escrowAddr, stakeCallData); callErr == nil {
			if unpacked, unpackErr := chain.EscrowABI.Unpack("verifierStakePerVerifier", rawStake); unpackErr == nil && len(unpacked) == 1 {
				if stake, ok := unpacked[0].(*big.Int); ok {
					verifierStakeStr = stake.String()
				}
			}
		}
	}
	if verifierPanelJSON == "" {
		slog.Warn("failed to fetch quorum panel; escrow stored with empty panel", "escrow", escrowAddr.Hex())
	}

	// Check if escrow already exists (e.g. created via API/MCP handler with on-chain fields already set)
	_, err = idx.db.GetEscrowByAddress(ctx, escrowAddr.Hex())
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check existing escrow %s: %w", escrowAddr.Hex(), err)
	}

	task, err := idx.db.CreateTask(ctx, "Indexed task", "", fmt.Sprintf("0x%x", taskSpecHash))
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}

	_, err = idx.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  idx.chainID,
		FactoryAddress:           idx.factoryAddress.Hex(),
		EscrowAddress:            escrowAddr.Hex(),
		EscrowID:                 escrowID,
		Buyer:                    buyer.Hex(),
		Worker:                   worker.Hex(),
		Verifier:                 primaryVerifier,
		VerifierPanelJSON:        verifierPanelJSON,
		QuorumThreshold:          int(quorumThreshold),
		QuorumVerifierCount:      int(quorumVerifierCount),
		VerifierStakePerVerifier: verifierStakeStr,
		Arbitrator:               arbitratorAddr.Hex(),
		Amount:                   "0",
		Token:                    tokenAddr.Hex(),
		Status:                   "created",
		SubmissionDeadline:       0,
		ServiceTier:              serviceTier,
		ZKVerifier:               zkVerifierAddr.Hex(),
		CircuitID:                fmt.Sprintf("0x%x", circuitID),
	})
	return err
}

func (idx *Indexer) handleOutcomeRecorded(ctx context.Context, lg types.Log) error {
	// OutcomeRecorded(uint256 indexed escrowId, address indexed participant, string role, string outcome)
	if len(lg.Topics) < 3 {
		return fmt.Errorf("OutcomeRecorded: insufficient topics (got %d, need >= 3)", len(lg.Topics))
	}

	participant := common.BytesToAddress(lg.Topics[2].Bytes())

	values, err := chain.FactoryABI.Events["OutcomeRecorded"].Inputs.NonIndexed().Unpack(lg.Data)
	if err != nil {
		return fmt.Errorf("unpack OutcomeRecorded: %w", err)
	}
	if len(values) < 2 {
		return fmt.Errorf("OutcomeRecorded: expected 2 non-indexed values, got %d", len(values))
	}

	role, ok := values[0].(string)
	if !ok {
		return fmt.Errorf("OutcomeRecorded: unexpected type for role: %T", values[0])
	}
	outcome, ok := values[1].(string)
	if !ok {
		return fmt.Errorf("OutcomeRecorded: unexpected type for outcome: %T", values[1])
	}

	slog.Info("outcome recorded",
		"participant", participant.Hex(),
		"role", role,
		"outcome", outcome,
	)

	logIndex, err := numconv.UintToInt(lg.Index, "log index")
	if err != nil {
		return err
	}
	blockNumber, err := numconv.Uint64ToInt64(lg.BlockNumber, "block number")
	if err != nil {
		return err
	}
	return idx.db.RecordReputationOutcome(ctx, &storage.ReputationEvent{
		Address:     strings.ToLower(participant.Hex()),
		Role:        role,
		Outcome:     outcome,
		TxHash:      lg.TxHash.Hex(),
		LogIndex:    logIndex,
		BlockNumber: blockNumber,
	})
}

func (idx *Indexer) isTerminalStatus(ctx context.Context, dbEscrowID int64) bool {
	e, err := idx.db.GetEscrow(ctx, dbEscrowID)
	if err != nil {
		return false
	}
	return terminalStatuses[e.Status]
}

// ProcessEscrowLog deduplicates, stores, and dispatches a single escrow event log.
// Exported so the CDP webhook handler can reuse the same processing pipeline.
func (idx *Indexer) ProcessEscrowLog(ctx context.Context, lg types.Log, dbEscrowID int64) error {
	logIndex, err := numconv.UintToInt(lg.Index, "log index")
	if err != nil {
		return err
	}
	exists, err := idx.db.ChainLogExists(ctx, lg.TxHash.Hex(), logIndex)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if err := idx.guardLog(lg); err != nil {
		return err
	}

	event, err := chain.EscrowABI.EventByID(lg.Topics[0])
	if err != nil {
		return nil //nolint:nilerr // Unknown ABI event ID -- not an error, just skip
	}

	rawData, err := json.Marshal(lg)
	if err != nil {
		return fmt.Errorf("marshal escrow log: %w", err)
	}
	blockNumber, err := numconv.Uint64ToInt64(lg.BlockNumber, "block number")
	if err != nil {
		return err
	}
	if err := idx.db.CreateChainLog(ctx, lg.TxHash.Hex(), logIndex, blockNumber, event.Name, lg.Address.Hex(), string(rawData)); err != nil {
		return err
	}

	// Update escrow status based on event (V1 single-milestone events only).
	// Skip if the escrow is already in a terminal state (e.g. "refunded" from
	// abortRemainingMilestones) to prevent the subsequent Settled event emitted
	// by _settleWorkerStake from incorrectly overwriting it to "settled".
	if newStatus, ok := eventStatusMap[event.Name]; ok {
		if !idx.isTerminalStatus(ctx, dbEscrowID) {
			if err := idx.db.UpdateEscrowStatus(ctx, dbEscrowID, newStatus); err != nil {
				return fmt.Errorf("update status to %s: %w", newStatus, err)
			}
			if terminalStatuses[newStatus] {
				if _, err := idx.db.RevokeDCTTokensByEscrow(ctx, dbEscrowID, "escrow_terminal_state:"+newStatus, "indexer"); err != nil {
					return fmt.Errorf("revoke dct tokens on status %s: %w", newStatus, err)
				}
			}
		}
	}

	// Update milestone status for milestone-specific events
	if msStatus, ok := milestoneStatusMap[event.Name]; ok {
		msIdx, err := idx.extractMilestoneIndex(lg)
		if err != nil {
			slog.Warn("failed to extract milestone index", "event", event.Name, "error", err)
		} else {
			if err := idx.db.UpdateMilestoneStatus(ctx, dbEscrowID, msIdx, msStatus); err != nil {
				slog.Warn("failed to update milestone status", "escrow_id", dbEscrowID, "milestone", msIdx, "status", msStatus, "error", err)
			} else if msStatus == "approved" || msStatus == "resolved" || msStatus == "settled" || msStatus == "cancelled" {
				if err := idx.db.UpdateEscrowMilestoneProgress(ctx, dbEscrowID, msIdx+1); err != nil {
					slog.Warn("failed to advance current_milestone", "escrow_id", dbEscrowID, "next_milestone", msIdx+1, "error", err)
				}
			}
		}
	}

	// Handle specific events
	var handlerErr error
	switch event.Name {
	case "SubmissionMade":
		handlerErr = idx.handleSubmission(ctx, lg, dbEscrowID)
	case "Disputed", "Rejected", "SilenceEscalated":
		handlerErr = idx.handleDispute(ctx, lg, dbEscrowID, event.Name)
	case "DisputeResolved":
		handlerErr = idx.handleDisputeResolved(ctx, lg, dbEscrowID)
	case "MilestoneSubmitted":
		handlerErr = idx.handleMilestoneSubmission(ctx, lg, dbEscrowID)
	case "MilestoneDisputed", "MilestoneRejected", "MilestoneSilenceEscalated":
		handlerErr = idx.handleMilestoneDispute(ctx, lg, dbEscrowID, event.Name)
	case "MilestoneDisputeResolved":
		handlerErr = idx.handleMilestoneDisputeResolved(ctx, lg, dbEscrowID)
	case "RemainingMilestonesAborted":
		handlerErr = idx.handleRemainingMilestonesAborted(ctx, lg, dbEscrowID)
	case "BackupActivated":
		handlerErr = idx.handleBackupActivated(ctx, lg, dbEscrowID)
	case "EmergencyFrozen":
		handlerErr = idx.handleEscrowFrozenEscrow(ctx, dbEscrowID, true)
	case "EmergencyUnfrozen":
		handlerErr = idx.handleEscrowFrozenEscrow(ctx, dbEscrowID, false)
	case "EmergencyResolved":
		handlerErr = idx.handleEmergencyResolvedEscrow(ctx, lg, dbEscrowID)
	}

	if handlerErr == nil {
		idx.publishEvent(ctx, event.Name, lg.Address.Hex(), lg)
	}

	return handlerErr
}

func (idx *Indexer) handleSubmission(ctx context.Context, lg types.Log, dbEscrowID int64) error {
	values, err := chain.EscrowABI.Events["SubmissionMade"].Inputs.NonIndexed().Unpack(lg.Data)
	if err != nil {
		return fmt.Errorf("unpack SubmissionMade: %w", err)
	}

	if len(values) < 3 {
		return fmt.Errorf("SubmissionMade: expected 3 values, got %d", len(values))
	}
	hashBytes, ok := values[0].([32]byte)
	if !ok {
		return fmt.Errorf("SubmissionMade: unexpected type for submissionHash: %T", values[0])
	}
	submissionURI, ok := values[1].(string)
	if !ok {
		return fmt.Errorf("SubmissionMade: unexpected type for submissionURI: %T", values[1])
	}
	proofHashBytes, ok := values[2].([32]byte)
	if !ok {
		return fmt.Errorf("SubmissionMade: unexpected type for proofHash: %T", values[2])
	}

	submissionHash := fmt.Sprintf("0x%x", hashBytes)
	proofHash := fmt.Sprintf("0x%x", proofHashBytes)
	_, err = idx.db.CreateSubmission(ctx, dbEscrowID, submissionHash, submissionURI, proofHash)
	return err
}

func (idx *Indexer) handleDispute(ctx context.Context, lg types.Log, dbEscrowID int64, eventName string) error {
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

	_, err := idx.db.CreateDispute(ctx, dbEscrowID, raisedBy, reasonURI)
	return err
}

func (idx *Indexer) handleDisputeResolved(ctx context.Context, lg types.Log, dbEscrowID int64) error {
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

	dispute, err := idx.db.GetDisputeByEscrowID(ctx, dbEscrowID)
	if err != nil {
		slog.Warn("no existing dispute found for resolution, creating new record",
			"escrow_id", dbEscrowID, "error", err)
		_, createErr := idx.db.CreateDispute(ctx, dbEscrowID, "arbitrator", resolutionURI)
		if createErr != nil {
			return createErr
		}
		// Re-fetch to get the ID for the update call
		dispute, err = idx.db.GetDisputeByEscrowID(ctx, dbEscrowID)
		if err != nil {
			return fmt.Errorf("get newly created dispute: %w", err)
		}
	}

	return idx.db.UpdateDispute(ctx, dispute.ID, resolutionURI, int(workerAwardBps))
}

func (idx *Indexer) handleBackupActivated(ctx context.Context, lg types.Log, dbEscrowID int64) error {
	// BackupActivated(address indexed previousWorker, address indexed newWorker, uint64 newDeadline)
	if len(lg.Topics) < 3 {
		return fmt.Errorf("BackupActivated: insufficient topics (got %d, need >= 3)", len(lg.Topics))
	}

	newWorker := common.BytesToAddress(lg.Topics[2].Bytes())

	vals, err := chain.EscrowABI.Events["BackupActivated"].Inputs.NonIndexed().Unpack(lg.Data)
	if err != nil {
		return fmt.Errorf("BackupActivated: unpack non-indexed args: %w", err)
	}
	if len(vals) == 0 {
		return errors.New("BackupActivated: missing non-indexed args")
	}
	newDeadline, ok := vals[0].(uint64)
	if !ok {
		return fmt.Errorf("BackupActivated: unexpected type for newDeadline: %T", vals[0])
	}

	slog.Info("backup worker activated",
		"escrow_id", dbEscrowID,
		"previous_worker", common.BytesToAddress(lg.Topics[1].Bytes()).Hex(),
		"new_worker", newWorker.Hex(),
		"new_deadline", newDeadline,
	)

	return idx.db.UpdateEscrowBackupActivated(ctx, dbEscrowID, newWorker.Hex(), newDeadline)
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

func (idx *Indexer) handleMilestoneSubmission(ctx context.Context, lg types.Log, dbEscrowID int64) error {
	msIdx, err := idx.extractMilestoneIndex(lg)
	if err != nil {
		return err
	}

	values, err := chain.EscrowABI.Events["MilestoneSubmitted"].Inputs.NonIndexed().Unpack(lg.Data)
	if err != nil {
		return fmt.Errorf("unpack MilestoneSubmitted: %w", err)
	}
	if len(values) < 3 {
		return fmt.Errorf("MilestoneSubmitted: expected 3 values, got %d", len(values))
	}

	hashBytes, ok := values[0].([32]byte)
	if !ok {
		return fmt.Errorf("MilestoneSubmitted: unexpected type for submissionHash: %T", values[0])
	}
	submissionURI, ok := values[1].(string)
	if !ok {
		return fmt.Errorf("MilestoneSubmitted: unexpected type for submissionURI: %T", values[1])
	}
	proofHashBytes, ok := values[2].([32]byte)
	if !ok {
		return fmt.Errorf("MilestoneSubmitted: unexpected type for proofHash: %T", values[2])
	}

	submissionHash := fmt.Sprintf("0x%x", hashBytes)
	proofHash := fmt.Sprintf("0x%x", proofHashBytes)
	return idx.db.UpdateMilestoneSubmission(ctx, dbEscrowID, msIdx, submissionHash, submissionURI, proofHash)
}

func (idx *Indexer) handleMilestoneDispute(ctx context.Context, lg types.Log, dbEscrowID int64, eventName string) error {
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

	_, err = idx.db.CreateDispute(ctx, dbEscrowID, raisedBy, reasonURI)
	return err
}

func (idx *Indexer) handleMilestoneDisputeResolved(ctx context.Context, lg types.Log, dbEscrowID int64) error {
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
	dispute, err := idx.db.GetDisputeByEscrowID(ctx, dbEscrowID)
	if err != nil {
		_, createErr := idx.db.CreateDispute(ctx, dbEscrowID, "arbitrator", resolutionURI)
		if createErr != nil {
			return createErr
		}
		dispute, err = idx.db.GetDisputeByEscrowID(ctx, dbEscrowID)
		if err != nil {
			return fmt.Errorf("get newly created dispute: %w", err)
		}
	}

	return idx.db.UpdateDispute(ctx, dispute.ID, resolutionURI, int(workerAwardBps))
}

// --- Emergency response handlers ---

func (idx *Indexer) handleAddressFrozen(ctx context.Context, lg types.Log, frozen bool) error {
	// AddressFrozen(address indexed target) / AddressUnfrozen(address indexed target)
	if len(lg.Topics) < 2 {
		return errors.New("AddressFrozen/Unfrozen: insufficient topics")
	}
	target := common.BytesToAddress(lg.Topics[1].Bytes())
	addr := strings.ToLower(target.Hex())

	if frozen {
		slog.Info("address frozen", "target", addr)
		if err := idx.db.UpsertFrozenAddress(ctx, addr, "", "indexer"); err != nil {
			return err
		}
		return idx.db.CreateEmergencyAction(ctx, "freeze_address", addr, "", "", lg.TxHash.Hex())
	}
	slog.Info("address unfrozen", "target", addr)
	if err := idx.db.DeleteFrozenAddress(ctx, addr); err != nil {
		return err
	}
	return idx.db.CreateEmergencyAction(ctx, "unfreeze_address", addr, "", "", lg.TxHash.Hex())
}

func (idx *Indexer) handleEscrowFrozenFactory(ctx context.Context, lg types.Log, frozen bool) error {
	// EscrowFrozen(uint256 indexed escrowId) / EscrowUnfrozen(uint256 indexed escrowId)
	if len(lg.Topics) < 2 {
		return errors.New("EscrowFrozen/Unfrozen: insufficient topics")
	}
	escrowIDBig := new(big.Int).SetBytes(lg.Topics[1].Bytes())
	if !escrowIDBig.IsInt64() {
		return fmt.Errorf("escrowID overflows int64: %s", escrowIDBig.String())
	}

	e, err := idx.db.GetEscrowByOnChainID(ctx, idx.chainID, escrowIDBig.Int64())
	if err != nil {
		return fmt.Errorf("lookup escrow for freeze: %w", err)
	}

	action := "freeze_escrow"
	if !frozen {
		action = "unfreeze_escrow"
	}
	slog.Info("escrow emergency "+action, "escrow_id", e.ID, "on_chain_id", escrowIDBig.Int64())

	if err := idx.db.UpdateEscrowFrozen(ctx, e.ID, frozen); err != nil {
		return err
	}
	return idx.db.CreateEmergencyAction(ctx, action, e.EscrowAddress, "", "", lg.TxHash.Hex())
}

func (idx *Indexer) handleEmergencyResolvedFactory(ctx context.Context, lg types.Log) error {
	// EmergencyResolved(uint256 indexed escrowId, uint16 workerAwardBps)
	if len(lg.Topics) < 2 {
		return errors.New("EmergencyResolved: insufficient topics")
	}
	escrowIDBig := new(big.Int).SetBytes(lg.Topics[1].Bytes())
	if !escrowIDBig.IsInt64() {
		return fmt.Errorf("escrowID overflows int64: %s", escrowIDBig.String())
	}

	values, err := chain.FactoryABI.Events["EmergencyResolved"].Inputs.NonIndexed().Unpack(lg.Data)
	if err != nil {
		return fmt.Errorf("unpack EmergencyResolved: %w", err)
	}
	if len(values) < 1 {
		return errors.New("EmergencyResolved: missing workerAwardBps")
	}
	bps, ok := values[0].(uint16)
	if !ok {
		return fmt.Errorf("EmergencyResolved: unexpected type for workerAwardBps: %T", values[0])
	}

	e, err := idx.db.GetEscrowByOnChainID(ctx, idx.chainID, escrowIDBig.Int64())
	if err != nil {
		return fmt.Errorf("lookup escrow for emergency resolve: %w", err)
	}

	slog.Info("emergency resolved (factory)", "escrow_id", e.ID, "worker_award_bps", bps)

	if err := idx.db.UpdateEscrowStatus(ctx, e.ID, "resolved"); err != nil {
		return err
	}
	if _, err := idx.db.RevokeDCTTokensByEscrow(ctx, e.ID, "emergency_resolved", "indexer"); err != nil {
		return fmt.Errorf("revoke dct tokens on emergency resolve: %w", err)
	}
	return idx.db.CreateEmergencyAction(ctx, "emergency_resolve", e.EscrowAddress, "",
		fmt.Sprintf("workerAwardBps=%d", bps), lg.TxHash.Hex())
}

func (idx *Indexer) handleEscrowFrozenEscrow(ctx context.Context, dbEscrowID int64, frozen bool) error {
	action := "freeze_escrow"
	if !frozen {
		action = "unfreeze_escrow"
	}
	slog.Info("escrow emergency "+action+" (escrow event)", "escrow_id", dbEscrowID)
	return idx.db.UpdateEscrowFrozen(ctx, dbEscrowID, frozen)
}

func (idx *Indexer) handleEmergencyResolvedEscrow(ctx context.Context, lg types.Log, dbEscrowID int64) error {
	// EmergencyResolved(uint16 workerAwardBps) -- escrow-level event
	values, err := chain.EscrowABI.Events["EmergencyResolved"].Inputs.NonIndexed().Unpack(lg.Data)
	if err != nil {
		return fmt.Errorf("unpack escrow EmergencyResolved: %w", err)
	}
	if len(values) < 1 {
		return errors.New("escrow EmergencyResolved: missing workerAwardBps")
	}
	bps, ok := values[0].(uint16)
	if !ok {
		return fmt.Errorf("escrow EmergencyResolved: unexpected type for workerAwardBps: %T", values[0])
	}

	slog.Info("emergency resolved (escrow event)", "escrow_id", dbEscrowID, "worker_award_bps", bps)
	if err := idx.db.UpdateEscrowStatus(ctx, dbEscrowID, "resolved"); err != nil {
		return err
	}
	if _, err := idx.db.RevokeDCTTokensByEscrow(ctx, dbEscrowID, "emergency_resolved", "indexer"); err != nil {
		return fmt.Errorf("revoke dct tokens on emergency resolve: %w", err)
	}
	return nil
}

func (idx *Indexer) handleRemainingMilestonesAborted(ctx context.Context, lg types.Log, dbEscrowID int64) error {
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
	milestones, err := idx.db.GetMilestonesByEscrow(ctx, dbEscrowID)
	if err != nil {
		return fmt.Errorf("get milestones: %w", err)
	}
	for _, ms := range milestones {
		if ms.MilestoneIndex >= int(fromIndex) {
			if err := idx.db.UpdateMilestoneStatus(ctx, dbEscrowID, ms.MilestoneIndex, "cancelled"); err != nil {
				slog.Warn("failed to cancel milestone", "escrow_id", dbEscrowID, "milestone", ms.MilestoneIndex, "error", err)
			}
		}
	}

	// The contract sets status = Status.Refunded after abort. Set this before
	// the subsequent Settled event (from _settleWorkerStake) is processed, so
	// the terminal-state guard in processEscrowLog prevents overwrite.
	if err := idx.db.UpdateEscrowStatus(ctx, dbEscrowID, "refunded"); err != nil {
		return fmt.Errorf("set escrow refunded after abort: %w", err)
	}

	return nil
}
