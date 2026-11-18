package indexer

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
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

	triggerCh chan struct{}

	runMu   sync.Mutex
	current *runPass // pass in progress
	queued  *runPass // next pass; callers that arrive during current join it
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
		triggerCh:         make(chan struct{}, 1),
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

// Run polls the chain every poll interval, and straight away after Trigger.
func (idx *Indexer) Run(ctx context.Context) {
	ticker := time.NewTicker(idx.pollInterval)
	defer ticker.Stop()

	var consecutiveFailures int

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-idx.triggerCh:
		}
		err := idx.RunOnce(ctx)
		if err == nil {
			consecutiveFailures = 0
			continue
		}
		if ctx.Err() != nil {
			return
		}
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
	}
}

// Trigger asks Run to start a pass now instead of at the next tick. It never
// blocks; triggers that arrive while one is already pending merge into it.
// The pass applies the same confirmation depth as a timed poll.
func (idx *Indexer) Trigger() {
	select {
	case idx.triggerCh <- struct{}{}:
	default:
	}
}

// runPass is one indexing pass. Every RunOnce caller that joined the pass
// waits on done and returns err.
type runPass struct {
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	err     error
	waiters int // guarded by Indexer.runMu
}

func newRunPass() *runPass {
	ctx, cancel := context.WithCancel(context.Background())
	return &runPass{ctx: ctx, cancel: cancel, done: make(chan struct{})}
}

// RunOnce indexes blocks up to head-confirmations.
//
// Passes never overlap. A caller joins the pass that is queued behind the
// running one, so the pass it waits for reads the chain after the caller
// arrived: a transaction mined before RunOnce is in the database when it
// returns nil. Each caller gets the result of the pass it joined. A caller
// whose ctx ends returns ctx.Err() at once; a pass that every caller has left
// is cancelled.
//
// A handler error rolls back the failed block and leaves the cursor in front
// of that block; the next pass retries it. Run counts that error toward max
// consecutive failures.
func (idx *Indexer) RunOnce(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	idx.runMu.Lock()
	var p *runPass
	switch {
	case idx.current == nil:
		p = newRunPass()
		idx.current = p
		go idx.execPass(p)
	case idx.queued != nil:
		p = idx.queued
	default:
		p = newRunPass()
		idx.queued = p
	}
	p.waiters++
	idx.runMu.Unlock()

	select {
	case <-p.done:
		return p.err
	case <-ctx.Done():
		idx.leavePass(p)
		return ctx.Err()
	}
}

func (idx *Indexer) leavePass(p *runPass) {
	idx.runMu.Lock()
	defer idx.runMu.Unlock()
	p.waiters--
	if p.waiters > 0 {
		return
	}
	if idx.queued == p {
		// Not started and nobody needs it.
		idx.queued = nil
	}
	p.cancel()
}

// execPass runs p on its own goroutine and then starts the queued pass, if
// any. Deferred cleanup runs on panic too, so waiters are always released and
// a panic is reported as that pass's error instead of crashing the server.
func (idx *Indexer) execPass(p *runPass) {
	defer func() {
		if r := recover(); r != nil {
			p.err = fmt.Errorf("indexer pass panicked: %v", r)
			slog.Error("indexer pass panicked", "panic", r, "stack", string(debug.Stack()))
		}
		p.cancel()
		close(p.done)

		idx.runMu.Lock()
		defer idx.runMu.Unlock()
		idx.current = idx.queued
		idx.queued = nil
		if idx.current != nil {
			go idx.execPass(idx.current)
		}
	}()
	p.err = idx.runOnceBody(p.ctx)
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
	if safeHead > math.MaxInt64 {
		return fmt.Errorf("block number %d exceeds int64 max", safeHead)
	}

	fromBlock, err := idx.firstUnindexedBlock(ctx, safeHead)
	if err != nil {
		return err
	}
	if fromBlock > safeHead {
		return nil
	}
	return idx.applyRange(ctx, fromBlock, safeHead)
}

// firstUnindexedBlock returns the block after the stored cursor. With no
// stored cursor it picks the scan start (startBlock, else a lookback from
// safeHead) and stores the block before it as the cursor before anything is
// applied. Without that, a failure inside the first range would be retried
// from a later lookback that no longer covers the failed block. A stored
// cursor of -1 means block 0 has not been indexed yet.
func (idx *Indexer) firstUnindexedBlock(ctx context.Context, safeHead uint64) (uint64, error) {
	cursor, found, err := idx.db.LookupCursor(ctx, idx.chainID, cursorKey)
	if err != nil {
		return 0, fmt.Errorf("get cursor: %w", err)
	}
	if found {
		if cursor == -1 {
			return 0, nil
		}
		last, err := numconv.Int64ToUint64(cursor, "cursor")
		if err != nil {
			return 0, fmt.Errorf("invalid cursor value: %w", err)
		}
		return last + 1, nil
	}

	var start uint64
	if idx.startBlock > 0 {
		start = idx.startBlock
	} else if safeHead > defaultLookback {
		start = safeHead - defaultLookback
	}
	startCursor, err := numconv.Uint64ToInt64(start, "start block")
	if err != nil {
		return 0, err
	}
	if err := idx.storeCursor(ctx, startCursor-1); err != nil {
		return 0, fmt.Errorf("store initial cursor: %w", err)
	}
	return start, nil
}

// confirmedHead is the highest block that has confirmations blocks on top of
// it. ok is false when the chain is not yet that deep.
func (idx *Indexer) confirmedHead(head uint64) (uint64, bool) {
	if head < idx.confirmations {
		return 0, false
	}
	return head - idx.confirmations, true
}

// escrowLog is a log emitted by an escrow contract.
type escrowLog struct {
	addr string // escrows.escrow_address as stored, or checksummed for escrows created in this range
	id   int64  // escrows.id; 0 when the escrow row did not exist when the range was fetched
	log  types.Log
}

// rangeLogs holds every log in a block range, grouped by block once so
// applying the range is linear in the number of logs.
type rangeLogs struct {
	factory map[uint64][]types.Log
	escrow  map[uint64][]escrowLog
	blocks  []uint64 // ascending; each has at least one log

	// milestonesChecked records escrows whose milestone rows were already
	// checked during this pass.
	milestonesChecked map[string]bool
}

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

// createdEscrowAddress returns the escrow address of an EscrowCreated log.
func createdEscrowAddress(lg types.Log) (common.Address, bool) {
	if len(lg.Topics) < 3 || lg.Topics[0] != chain.FactoryABI.Events["EscrowCreated"].ID {
		return common.Address{}, false
	}
	return common.BytesToAddress(lg.Topics[2].Bytes()), true
}

// fetchRange reads every factory log in [from, to] and the logs of every
// escrow that exists or is created in that range. It runs outside any
// database transaction.
func (idx *Indexer) fetchRange(ctx context.Context, from, to uint64) (*rangeLogs, error) {
	factoryLogs, err := idx.chain.FilterLogs(ctx, []common.Address{idx.factoryAddress}, factoryLogTopics(), from, to)
	if err != nil {
		return nil, fmt.Errorf("filter factory logs: %w", err)
	}
	escrows, err := idx.db.ListEscrowsByChainID(ctx, idx.chainID)
	if err != nil {
		return nil, fmt.Errorf("list escrows: %w", err)
	}

	type escrowRef struct {
		addr string
		id   int64
	}
	refs := make(map[string]escrowRef, len(escrows))
	for _, e := range escrows {
		if !common.IsHexAddress(e.EscrowAddress) {
			continue // pending create: no contract address yet
		}
		refs[strings.ToLower(e.EscrowAddress)] = escrowRef{addr: e.EscrowAddress, id: e.ID}
	}
	for _, lg := range factoryLogs {
		addr, ok := createdEscrowAddress(lg)
		if !ok {
			continue
		}
		key := strings.ToLower(addr.Hex())
		if _, exists := refs[key]; !exists {
			refs[key] = escrowRef{addr: addr.Hex()}
		}
	}

	rl := &rangeLogs{
		factory:           logsByBlock(factoryLogs),
		escrow:            make(map[uint64][]escrowLog),
		milestonesChecked: make(map[string]bool),
	}
	for _, ref := range refs {
		logs, err := idx.chain.FilterLogs(ctx, []common.Address{common.HexToAddress(ref.addr)}, nil, from, to)
		if err != nil {
			return nil, fmt.Errorf("filter escrow logs %s: %w", ref.addr, err)
		}
		for _, lg := range logs {
			rl.escrow[lg.BlockNumber] = append(rl.escrow[lg.BlockNumber], escrowLog{addr: ref.addr, id: ref.id, log: lg})
		}
	}

	blocks := make([]uint64, 0, len(rl.factory)+len(rl.escrow))
	for b := range rl.factory {
		blocks = append(blocks, b)
	}
	for b, logs := range rl.escrow {
		// Log index orders logs within a block across every escrow.
		slices.SortStableFunc(logs, func(a, b escrowLog) int { return cmp.Compare(a.log.Index, b.log.Index) })
		if _, dup := rl.factory[b]; !dup {
			blocks = append(blocks, b)
		}
	}
	slices.Sort(blocks)
	rl.blocks = blocks
	return rl, nil
}

// applyRange applies logs from through to. Empty stretches advance the
// cursor in one transaction. A block that contains logs is its own transaction
// together with the cursor update for that block. Chain reads a block needs
// happen before its transaction opens. A handler error leaves the cursor on
// the last block that fully committed.
func (idx *Indexer) applyRange(ctx context.Context, from, to uint64) error {
	rl, err := idx.fetchRange(ctx, from, to)
	if err != nil {
		return err
	}

	next := from // first block not yet committed in this pass
	for _, block := range rl.blocks {
		if block > next {
			if err := idx.storeCursorAt(ctx, block-1); err != nil {
				return err
			}
		}
		chainData, err := idx.fetchBlockChainData(ctx, block, rl)
		if err != nil {
			return fmt.Errorf("prepare block %d: %w", block, err)
		}
		batch, err := idx.applyBlock(ctx, block, rl, chainData)
		if err != nil {
			return fmt.Errorf("apply block %d: %w", block, err)
		}
		idx.flushPublished(batch)
		next = block + 1
	}
	if next <= to {
		return idx.storeCursorAt(ctx, to)
	}
	return nil
}

func (idx *Indexer) storeCursorAt(ctx context.Context, block uint64) error {
	cursor, err := numconv.Uint64ToInt64(block, "block number")
	if err != nil {
		return err
	}
	if err := idx.storeCursor(ctx, cursor); err != nil {
		return fmt.Errorf("advance cursor to %d: %w", block, err)
	}
	return nil
}

func (idx *Indexer) storeCursor(ctx context.Context, cursor int64) error {
	return idx.db.RunInImmediateTx(ctx, func(txCtx context.Context) error {
		if err := idx.db.SetCursor(txCtx, idx.chainID, cursorKey, cursor); err != nil {
			return fmt.Errorf("set cursor: %w", err)
		}
		return nil
	})
}

// milestoneDetail is one on-chain milestone of a multi-milestone escrow.
type milestoneDetail struct {
	amount   string
	deadline int64
}

// escrowMilestones is an escrow's on-chain milestone layout. list is empty
// for a single-milestone escrow, which has no milestone rows.
type escrowMilestones struct {
	count int
	list  []milestoneDetail
}

// createdEscrowDetails is contract state an EscrowCreated event does not carry.
type createdEscrowDetails struct {
	verifierPanelJSON string
	primaryVerifier   string
	verifierStake     string
	milestones        escrowMilestones
}

// blockChainData is everything a block's handlers read from the chain,
// fetched before the block's write transaction opens.
type blockChainData struct {
	created  map[string]*createdEscrowDetails // lower-case escrow address
	backfill map[string]*escrowMilestones     // lower-case escrow address
}

var milestoneEventIDs = func() map[common.Hash]bool {
	ids := make(map[common.Hash]bool, len(milestoneStatusMap))
	for name := range milestoneStatusMap {
		ids[chain.EscrowABI.Events[name].ID] = true
	}
	return ids
}()

// fetchBlockChainData reads the contract state block needs: details of
// escrows created in it that have no row yet, and the milestone layout of
// existing escrows that emit milestone events but have no milestone rows
// (escrows indexed before milestone rows were created at indexing time).
func (idx *Indexer) fetchBlockChainData(ctx context.Context, block uint64, rl *rangeLogs) (*blockChainData, error) {
	data := &blockChainData{
		created:  make(map[string]*createdEscrowDetails),
		backfill: make(map[string]*escrowMilestones),
	}
	for _, lg := range rl.factory[block] {
		addr, ok := createdEscrowAddress(lg)
		if !ok {
			continue
		}
		if _, err := idx.db.GetEscrowByAddress(ctx, addr.Hex()); err == nil {
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("check existing escrow %s: %w", addr.Hex(), err)
		}
		details, err := idx.fetchCreatedEscrowDetails(ctx, addr)
		if err != nil {
			return nil, err
		}
		data.created[strings.ToLower(addr.Hex())] = details
	}

	for _, el := range rl.escrow[block] {
		key := strings.ToLower(el.addr)
		if el.id == 0 || rl.milestonesChecked[key] || len(el.log.Topics) == 0 || !milestoneEventIDs[el.log.Topics[0]] {
			continue
		}
		rl.milestonesChecked[key] = true
		existing, err := idx.db.GetMilestonesByEscrow(ctx, el.id)
		if err != nil {
			return nil, err
		}
		if len(existing) > 0 {
			continue
		}
		ms, err := idx.fetchMilestones(ctx, common.HexToAddress(el.addr))
		if err != nil {
			return nil, err
		}
		data.backfill[key] = ms
	}
	return data, nil
}

func (idx *Indexer) callEscrowView(ctx context.Context, addr common.Address, method string, args ...any) ([]any, error) {
	callData, err := chain.EscrowABI.Pack(method, args...)
	if err != nil {
		return nil, fmt.Errorf("pack %s: %w", method, err)
	}
	raw, err := idx.chain.CallContract(ctx, addr, callData)
	if err != nil {
		return nil, fmt.Errorf("call %s on %s: %w", method, addr.Hex(), err)
	}
	out, err := chain.EscrowABI.Unpack(method, raw)
	if err != nil {
		return nil, fmt.Errorf("unpack %s from %s: %w", method, addr.Hex(), err)
	}
	return out, nil
}

func (idx *Indexer) fetchCreatedEscrowDetails(ctx context.Context, addr common.Address) (*createdEscrowDetails, error) {
	details := &createdEscrowDetails{}
	// The quorum panel and verifier stake are informational; an escrow is still
	// indexed without them.
	if out, err := idx.callEscrowView(ctx, addr, "getQuorumPanel"); err == nil && len(out) == 1 {
		if panel, ok := out[0].([]common.Address); ok {
			lowerPanel := make([]string, len(panel))
			for i, a := range panel {
				lowerPanel[i] = strings.ToLower(a.Hex())
			}
			if len(lowerPanel) > 0 {
				details.primaryVerifier = lowerPanel[0]
			}
			if b, marshalErr := json.Marshal(lowerPanel); marshalErr == nil {
				details.verifierPanelJSON = string(b)
			}
		}
	}
	if details.verifierPanelJSON == "" {
		slog.Warn("failed to fetch quorum panel; escrow stored with empty panel", "escrow", addr.Hex())
	}
	if out, err := idx.callEscrowView(ctx, addr, "verifierStakePerVerifier"); err == nil && len(out) == 1 {
		if stake, ok := out[0].(*big.Int); ok {
			details.verifierStake = stake.String()
		}
	}

	// Milestone events update milestone rows, so the layout is required.
	ms, err := idx.fetchMilestones(ctx, addr)
	if err != nil {
		return nil, err
	}
	details.milestones = *ms
	return details, nil
}

func (idx *Indexer) fetchMilestones(ctx context.Context, addr common.Address) (*escrowMilestones, error) {
	out, err := idx.callEscrowView(ctx, addr, "milestoneCount")
	if err != nil {
		return nil, err
	}
	if len(out) != 1 {
		return nil, fmt.Errorf("milestoneCount from %s: expected 1 value, got %d", addr.Hex(), len(out))
	}
	count, ok := out[0].(uint8)
	if !ok {
		return nil, fmt.Errorf("milestoneCount from %s: unexpected type %T", addr.Hex(), out[0])
	}
	if count <= 1 {
		return &escrowMilestones{count: 1}, nil
	}
	ms := &escrowMilestones{count: int(count), list: make([]milestoneDetail, 0, count)}
	for i := range int64(count) {
		vals, err := idx.callEscrowView(ctx, addr, "milestones", big.NewInt(i))
		if err != nil {
			return nil, err
		}
		if len(vals) < 2 {
			return nil, fmt.Errorf("milestones(%d) from %s: expected at least 2 values, got %d", i, addr.Hex(), len(vals))
		}
		amount, ok := vals[0].(*big.Int)
		if !ok {
			return nil, fmt.Errorf("milestones(%d) from %s: unexpected amount type %T", i, addr.Hex(), vals[0])
		}
		deadlineRaw, ok := vals[1].(uint64)
		if !ok {
			return nil, fmt.Errorf("milestones(%d) from %s: unexpected deadline type %T", i, addr.Hex(), vals[1])
		}
		deadline, err := numconv.Uint64ToInt64(deadlineRaw, "milestone deadline")
		if err != nil {
			return nil, err
		}
		ms.list = append(ms.list, milestoneDetail{amount: amount.String(), deadline: deadline})
	}
	return ms, nil
}

// ensureMilestones creates an escrow's milestone rows when it has none.
func (idx *Indexer) ensureMilestones(ctx context.Context, escrowID int64, ms *escrowMilestones) error {
	if len(ms.list) == 0 {
		return nil
	}
	existing, err := idx.db.GetMilestonesByEscrow(ctx, escrowID)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}
	for i, m := range ms.list {
		if _, err := idx.db.CreateMilestone(ctx, &storage.MilestoneRecord{
			EscrowID:           escrowID,
			MilestoneIndex:     i,
			Amount:             m.amount,
			SubmissionDeadline: m.deadline,
			Status:             "pending",
		}); err != nil {
			return fmt.Errorf("create milestone %d: %w", i, err)
		}
	}
	return idx.db.SetEscrowMilestoneCount(ctx, escrowID, ms.count)
}

func (idx *Indexer) applyBlock(ctx context.Context, block uint64, rl *rangeLogs, data *blockChainData) (*publishBatch, error) {
	cursor, err := numconv.Uint64ToInt64(block, "block number")
	if err != nil {
		return nil, err
	}
	var batch *publishBatch
	err = idx.db.RunInImmediateTx(ctx, func(txCtx context.Context) error {
		batch = &publishBatch{}
		txCtx = withPublishBatch(txCtx, batch)
		for _, lg := range rl.factory[block] {
			if err := idx.processFactoryLog(txCtx, lg, data); err != nil {
				return fmt.Errorf("factory log %s:%d: %w", lg.TxHash.Hex(), lg.Index, err)
			}
		}

		ids := make(map[string]int64)
		for _, el := range rl.escrow[block] {
			key := strings.ToLower(el.addr)
			id := el.id
			if id == 0 {
				if id = ids[key]; id == 0 {
					e, err := idx.db.GetEscrowByAddress(txCtx, el.addr)
					if err != nil {
						return fmt.Errorf("escrow %s emitted logs but has no row: %w", el.addr, err)
					}
					id = e.ID
					ids[key] = id
				}
			}
			if ms, ok := data.backfill[key]; ok {
				if err := idx.ensureMilestones(txCtx, id, ms); err != nil {
					return fmt.Errorf("backfill milestones for escrow %s: %w", el.addr, err)
				}
			}
			if err := idx.processEscrowLog(txCtx, el.log, id); err != nil {
				return fmt.Errorf("escrow %s log %s:%d: %w", el.addr, el.log.TxHash.Hex(), el.log.Index, err)
			}
		}
		if err := idx.db.SetCursor(txCtx, idx.chainID, cursorKey, cursor); err != nil {
			return fmt.Errorf("set cursor: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return batch, nil
}

func (idx *Indexer) guardLog(lg types.Log) error {
	if idx.logHook == nil {
		return nil
	}
	return idx.logHook(lg)
}

// processFactoryLog deduplicates, stores, and dispatches a single factory
// event log. It runs inside the block's transaction and makes no chain calls;
// data holds the contract state fetched for the block beforehand.
func (idx *Indexer) processFactoryLog(ctx context.Context, lg types.Log, data *blockChainData) error {
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
		handlerErr = idx.handleEscrowCreated(ctx, lg, data)
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

func (idx *Indexer) handleEscrowCreated(ctx context.Context, lg types.Log, data *blockChainData) error {
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

	// Check if escrow already exists (e.g. created via API/MCP handler with on-chain fields already set)
	_, err = idx.db.GetEscrowByAddress(ctx, escrowAddr.Hex())
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check existing escrow %s: %w", escrowAddr.Hex(), err)
	}
	var details *createdEscrowDetails
	if data != nil {
		details = data.created[strings.ToLower(escrowAddr.Hex())]
	}
	if details == nil {
		// fetchBlockChainData reads these for every new escrow in the block.
		return fmt.Errorf("escrow %s: contract details were not fetched before the block transaction", escrowAddr.Hex())
	}

	task, err := idx.db.CreateTask(ctx, "Indexed task", "", fmt.Sprintf("0x%x", taskSpecHash))
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}

	created, err := idx.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  idx.chainID,
		FactoryAddress:           idx.factoryAddress.Hex(),
		EscrowAddress:            escrowAddr.Hex(),
		EscrowID:                 escrowID,
		Buyer:                    buyer.Hex(),
		Worker:                   worker.Hex(),
		Verifier:                 details.primaryVerifier,
		VerifierPanelJSON:        details.verifierPanelJSON,
		QuorumThreshold:          int(quorumThreshold),
		QuorumVerifierCount:      int(quorumVerifierCount),
		VerifierStakePerVerifier: details.verifierStake,
		Arbitrator:               arbitratorAddr.Hex(),
		Amount:                   "0",
		Token:                    tokenAddr.Hex(),
		Status:                   "created",
		SubmissionDeadline:       0,
		MilestoneCount:           details.milestones.count,
		ServiceTier:              serviceTier,
		ZKVerifier:               zkVerifierAddr.Hex(),
		CircuitID:                fmt.Sprintf("0x%x", circuitID),
	})
	if err != nil {
		return err
	}
	// Escrows created directly on-chain need milestone rows too, or their
	// first milestone event has no row to update.
	return idx.ensureMilestones(ctx, created.ID, &details.milestones)
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

func (idx *Indexer) isTerminalStatus(ctx context.Context, dbEscrowID int64) (bool, error) {
	e, err := idx.db.GetEscrow(ctx, dbEscrowID)
	if err != nil {
		return false, fmt.Errorf("load escrow status: %w", err)
	}
	return terminalStatuses[e.Status], nil
}

// processEscrowLog deduplicates, stores, and dispatches a single escrow event
// log inside the block's transaction. Every write error is returned so the
// block rolls back with its dedup row and cursor update.
func (idx *Indexer) processEscrowLog(ctx context.Context, lg types.Log, dbEscrowID int64) error {
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
		terminal, err := idx.isTerminalStatus(ctx, dbEscrowID)
		if err != nil {
			return err
		}
		if !terminal {
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
			return fmt.Errorf("%s: %w", event.Name, err)
		}
		if err := idx.db.UpdateMilestoneStatus(ctx, dbEscrowID, msIdx, msStatus); err != nil {
			return fmt.Errorf("%s: update milestone %d status to %s: %w", event.Name, msIdx, msStatus, err)
		}
		if msStatus == "approved" || msStatus == "resolved" || msStatus == "settled" || msStatus == "cancelled" {
			if err := idx.db.UpdateEscrowMilestoneProgress(ctx, dbEscrowID, msIdx+1); err != nil {
				return fmt.Errorf("%s: advance current milestone to %d: %w", event.Name, msIdx+1, err)
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
				return fmt.Errorf("cancel milestone %d: %w", ms.MilestoneIndex, err)
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
