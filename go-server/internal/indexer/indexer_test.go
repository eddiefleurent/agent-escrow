package indexer

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/events"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

const testPollInterval = 10 * time.Millisecond

func setupTest(t *testing.T, opts ...Option) (*Indexer, *chain.MockClient) {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mock := chain.NewMockClient()
	mock.BlockNum = 100

	// Prepend fast poll interval so callers can still override if needed
	opts = append([]Option{WithPollInterval(testPollInterval)}, opts...)
	idx := New(db, mock, "0x0000000000000000000000000000000000000000", opts...)
	return idx, mock
}

func TestRunOnce_NilChain(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer db.Close()

	idx := New(db, nil, "0x0000000000000000000000000000000000000000")
	if err := idx.RunOnce(context.Background()); err != nil {
		t.Fatalf("expected nil error for nil chain, got: %v", err)
	}
}

func TestRunOnce_BlockNumberError(t *testing.T) {
	idx, mock := setupTest(t)
	mock.BlockNumErr = errors.New("rpc down")

	err := idx.RunOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "get block number") {
		t.Fatalf("expected block number error, got: %v", err)
	}
}

func TestRunOnce_Success(t *testing.T) {
	idx, _ := setupTest(t)
	if err := idx.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestErr_ReturnsFatalAfterConsecutiveFailures(t *testing.T) {
	threshold := 3
	idx, mock := setupTest(t, WithMaxConsecutiveFailures(threshold))
	mock.BlockNumErr = errors.New("persistent rpc failure")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go idx.Run(ctx)

	select {
	case err := <-idx.Err():
		if err == nil {
			t.Fatal("expected non-nil fatal error")
		}
		if !strings.Contains(err.Error(), "consecutive failures") {
			t.Fatalf("expected 'consecutive failures' in error, got: %v", err)
		}
		if !strings.Contains(err.Error(), "persistent rpc failure") {
			t.Fatalf("expected original error wrapped, got: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for fatal error from indexer")
	}
}

func TestErr_ResetsOnSuccess(t *testing.T) {
	threshold := 3
	idx, mock := setupTest(t, WithMaxConsecutiveFailures(threshold))

	// Fail twice, then succeed -- should not trigger fatal
	callCount := 0
	origBlockNum := mock.BlockNum

	// We can't easily alternate mock behavior per-call with the current MockClient,
	// so we test RunOnce directly to verify the counter logic.
	mock.BlockNumErr = errors.New("temporary failure")

	for range threshold - 1 {
		if err := idx.RunOnce(context.Background()); err == nil {
			t.Fatal("expected error from RunOnce")
		}
		callCount++
	}

	// Now succeed
	mock.BlockNumErr = nil
	mock.BlockNum = origBlockNum
	if err := idx.RunOnce(context.Background()); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}

	// Fail again -- counter should have reset, so threshold-1 more failures should not trigger fatal
	mock.BlockNumErr = errors.New("another failure")
	for range threshold - 1 {
		if err := idx.RunOnce(context.Background()); err == nil {
			t.Fatal("expected error from RunOnce")
		}
	}

	// The error channel should be empty (no fatal sent)
	select {
	case err := <-idx.Err():
		t.Fatalf("did not expect fatal error, got: %v", err)
	default:
		// Expected: no fatal error
	}
}

func TestErr_RunExitsAfterFatal(t *testing.T) {
	threshold := 2
	idx, mock := setupTest(t, WithMaxConsecutiveFailures(threshold))
	mock.BlockNumErr = errors.New("dead")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		idx.Run(ctx)
		close(done)
	}()

	// Drain the fatal error
	select {
	case <-idx.Err():
	case <-ctx.Done():
		t.Fatal("timed out waiting for fatal error")
	}

	// Run should have returned after sending the fatal error
	select {
	case <-done:
		// Run exited as expected
	case <-ctx.Done():
		t.Fatal("timed out waiting for Run to exit after fatal")
	}
}

func TestErr_DisabledWithZeroThreshold(t *testing.T) {
	idx, mock := setupTest(t, WithMaxConsecutiveFailures(0))
	mock.BlockNumErr = errors.New("always failing")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	go idx.Run(ctx)

	// With threshold=0, fatal signalling is disabled; Run should continue until ctx expires
	select {
	case err := <-idx.Err():
		t.Fatalf("expected no fatal error with threshold=0, got: %v", err)
	case <-ctx.Done():
		// Expected: ran until timeout without fatal
	}
}

func TestErr_ChannelBuffered_NeverBlocks(t *testing.T) {
	threshold := 1
	idx, mock := setupTest(t, WithMaxConsecutiveFailures(threshold))
	mock.BlockNumErr = errors.New("fail")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run should exit even if no one reads from Err() because the channel is buffered
	done := make(chan struct{})
	go func() {
		idx.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
		// Good: Run returned without blocking
	case <-ctx.Done():
		t.Fatal("Run blocked; fatal channel send should be non-blocking")
	}

	// The error should still be available to read
	select {
	case err := <-idx.Err():
		if err == nil {
			t.Fatal("expected non-nil error")
		}
	default:
		t.Fatal("expected error in channel after Run returned")
	}
}

func TestErr_ContextCancellation_NoFatal(t *testing.T) {
	idx, mock := setupTest(t, WithMaxConsecutiveFailures(3))
	mock.BlockNum = 100

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		idx.Run(ctx)
		close(done)
	}()

	// Cancel immediately -- no polls should happen
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after context cancellation")
	}

	select {
	case err := <-idx.Err():
		t.Fatalf("expected no fatal error on clean shutdown, got: %v", err)
	default:
		// Expected
	}
}

func TestDefaultMaxConsecutiveFailures(t *testing.T) {
	idx, _ := setupTest(t)
	if idx.maxConsecFailures != DefaultMaxConsecutiveFailures {
		t.Fatalf("expected default %d, got %d", DefaultMaxConsecutiveFailures, idx.maxConsecFailures)
	}
}

func frozenLog(factory, target common.Address, block uint64, index uint, txHash common.Hash) types.Log {
	return types.Log{
		Address: factory,
		Topics: []common.Hash{
			chain.FactoryABI.Events["AddressFrozen"].ID,
			common.BytesToHash(target.Bytes()),
		},
		BlockNumber: block,
		Index:       index,
		TxHash:      txHash,
	}
}

func assertFrozen(t *testing.T, idx *Indexer, addr common.Address, want bool) {
	t.Helper()
	got, err := idx.db.IsFrozenAddress(context.Background(), strings.ToLower(addr.Hex()))
	if err != nil {
		t.Fatalf("frozen lookup %s: %v", addr.Hex(), err)
	}
	if got != want {
		t.Fatalf("frozen(%s) = %v, want %v", addr.Hex(), got, want)
	}
}

func assertCursor(t *testing.T, idx *Indexer, want int64) {
	t.Helper()
	got, err := idx.db.GetCursor(context.Background(), idx.chainID, cursorKey)
	if err != nil {
		t.Fatalf("get cursor: %v", err)
	}
	if got != want {
		t.Fatalf("cursor = %d, want %d", got, want)
	}
}

func TestRunOnce_HandlerFailureRollsBackAndRetries(t *testing.T) {
	bus := events.NewEventBus(8)
	idx, mock := setupTest(t, WithStartBlock(10), WithEventBus(bus))
	mock.BlockNum = 12

	factory := idx.factoryAddress
	addrA := common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	addrB := common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	addrC := common.HexToAddress("0xcccccccccccccccccccccccccccccccccccccccc")
	addrD := common.HexToAddress("0xdddddddddddddddddddddddddddddddddddddddd")
	mock.Logs = []types.Log{
		frozenLog(factory, addrA, 10, 0, common.HexToHash("0x10")),
		frozenLog(factory, addrB, 11, 0, common.HexToHash("0x11")),
		frozenLog(factory, addrC, 11, 1, common.HexToHash("0x12")),
		frozenLog(factory, addrD, 12, 0, common.HexToHash("0x13")),
	}

	var calls atomic.Int32
	idx.logHook = func(types.Log) error {
		if calls.Add(1) == 3 {
			return errors.New("transient handler failure")
		}
		return nil
	}

	err := idx.RunOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "transient handler failure") {
		t.Fatalf("expected handler failure, got: %v", err)
	}
	assertCursor(t, idx, 10)
	assertFrozen(t, idx, addrA, true)
	assertFrozen(t, idx, addrB, false)
	assertFrozen(t, idx, addrC, false)
	assertFrozen(t, idx, addrD, false)
	exists, err := idx.db.ChainLogExists(context.Background(), common.HexToHash("0x11").Hex(), 0)
	if err != nil {
		t.Fatalf("chain log lookup: %v", err)
	}
	if exists {
		t.Fatal("rolled-back block 11 log was persisted")
	}
	if got := len(bus.RecentEvents("", events.L3, "", 20)); got != 1 {
		t.Fatalf("published events after rollback = %d, want 1", got)
	}

	if err := idx.RunOnce(context.Background()); err != nil {
		t.Fatalf("retry: %v", err)
	}
	assertCursor(t, idx, 12)
	assertFrozen(t, idx, addrA, true)
	assertFrozen(t, idx, addrB, true)
	assertFrozen(t, idx, addrC, true)
	assertFrozen(t, idx, addrD, true)
	if got := len(bus.RecentEvents("", events.L3, "", 20)); got != 4 {
		t.Fatalf("published events after retry = %d, want 4", got)
	}
}

func TestRunOnce_ConfirmationsBoundProcessedRange(t *testing.T) {
	idx, mock := setupTest(t, WithStartBlock(80), WithConfirmations(12))
	mock.BlockNum = 100

	factory := idx.factoryAddress
	confirmed := common.HexToAddress("0x1111111111111111111111111111111111111111")
	boundary := common.HexToAddress("0x2222222222222222222222222222222222222222")
	unconfirmed := common.HexToAddress("0x3333333333333333333333333333333333333333")
	head := common.HexToAddress("0x4444444444444444444444444444444444444444")
	mock.Logs = []types.Log{
		frozenLog(factory, confirmed, 80, 0, common.HexToHash("0x50")),
		frozenLog(factory, boundary, 88, 0, common.HexToHash("0x58")),
		frozenLog(factory, unconfirmed, 89, 0, common.HexToHash("0x59")),
		frozenLog(factory, head, 100, 0, common.HexToHash("0x64")),
	}

	if err := idx.RunOnce(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertCursor(t, idx, 88)
	assertFrozen(t, idx, confirmed, true)
	assertFrozen(t, idx, boundary, true)
	assertFrozen(t, idx, unconfirmed, false)
	assertFrozen(t, idx, head, false)

	shallow, shallowMock := setupTest(t, WithStartBlock(1), WithConfirmations(50))
	shallowMock.BlockNum = 40
	shallowMock.Logs = []types.Log{
		frozenLog(shallow.factoryAddress, confirmed, 10, 0, common.HexToHash("0x0a")),
	}
	if err := shallow.RunOnce(context.Background()); err != nil {
		t.Fatalf("shallow run: %v", err)
	}
	assertCursor(t, shallow, 0)
	assertFrozen(t, shallow, confirmed, false)
}

func TestRunOnce_ConcurrentSingleFlight(t *testing.T) {
	idx, mock := setupTest(t, WithStartBlock(1))
	mock.BlockNum = 20
	factory := idx.factoryAddress
	target := common.HexToAddress("0x5555555555555555555555555555555555555555")
	mock.Logs = []types.Log{
		frozenLog(factory, target, 10, 0, common.HexToHash("0x0a")),
	}

	// Gate the head read itself: it is the first chain call of every pass and
	// runs outside any SQLite transaction, so overlap is visible here.
	const n = 8
	release := make(chan struct{})
	var calls, inFlight, maxInFlight atomic.Int32
	mock.BlockNumHook = func(context.Context) {
		calls.Add(1)
		cur := inFlight.Add(1)
		for {
			old := maxInFlight.Load()
			if cur <= old || maxInFlight.CompareAndSwap(old, cur) {
				break
			}
		}
		<-release
		inFlight.Add(-1)
	}

	errCh := make(chan error, n)
	for range n {
		go func() { errCh <- idx.RunOnce(context.Background()) }()
	}
	// Hold the first pass until every other caller has queued behind it.
	waitFor(t, "callers to queue", func() bool {
		idx.runMu.Lock()
		defer idx.runMu.Unlock()
		return idx.current != nil && idx.queued != nil && idx.queued.waiters == n-1
	})
	close(release)
	for range n {
		if err := <-errCh; err != nil {
			t.Fatalf("concurrent RunOnce: %v", err)
		}
	}
	if got := maxInFlight.Load(); got != 1 {
		t.Fatalf("overlapping passes: max head reads in flight = %d", got)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("passes = %d, want 2 (the first pass and one shared follow-up)", got)
	}
	assertCursor(t, idx, 20)
	assertFrozen(t, idx, target, true)
}

func TestRunOnce_WaiterObservesHeadAfterArrival(t *testing.T) {
	idx, mock := setupTest(t, WithStartBlock(10))
	mock.BlockNum = 10
	factory := idx.factoryAddress
	first := common.HexToAddress("0x6666666666666666666666666666666666666666")
	later := common.HexToAddress("0x7777777777777777777777777777777777777777")
	mock.Logs = []types.Log{
		frozenLog(factory, first, 10, 0, common.HexToHash("0x01")),
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	idx.logHook = func(types.Log) error {
		once.Do(func() {
			close(started)
			<-release
		})
		return nil
	}

	leaderErr := make(chan error, 1)
	go func() { leaderErr <- idx.RunOnce(context.Background()) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("leader did not enter log processing")
	}

	mock.Lock()
	mock.BlockNum = 20
	mock.Logs = append(mock.Logs, frozenLog(factory, later, 15, 0, common.HexToHash("0x0f")))
	mock.Unlock()

	// Record the state at the moment the waiter's own RunOnce returns, not
	// after the leader finishes too.
	type observed struct {
		err    error
		cursor int64
		later  bool
	}
	waiterDone := make(chan observed, 1)
	go func() {
		err := idx.RunOnce(context.Background())
		cursor, _ := idx.db.GetCursor(context.Background(), idx.chainID, cursorKey)
		frozen, _ := idx.db.IsFrozenAddress(context.Background(), strings.ToLower(later.Hex()))
		waiterDone <- observed{err: err, cursor: cursor, later: frozen}
	}()
	waitFor(t, "waiter to queue", func() bool {
		idx.runMu.Lock()
		defer idx.runMu.Unlock()
		return idx.queued != nil && idx.queued.waiters == 1
	})
	select {
	case got := <-waiterDone:
		t.Fatalf("waiter returned before the in-flight pass finished: %+v", got)
	default:
	}
	close(release)

	got := <-waiterDone
	if got.err != nil {
		t.Fatalf("waiter: %v", got.err)
	}
	if got.cursor != 20 || !got.later {
		t.Fatalf("at waiter return: cursor = %d, later frozen = %v; want 20, true", got.cursor, got.later)
	}
	if err := <-leaderErr; err != nil {
		t.Fatalf("leader: %v", err)
	}
	assertFrozen(t, idx, first, true)
}

// The caller whose pass fails gets that failure, even when a queued
// follow-up pass then succeeds; a successful pass must not hide it.
func TestRunOnce_EachCallerGetsItsOwnPassResult(t *testing.T) {
	idx, mock := setupTest(t, WithStartBlock(10))
	mock.BlockNum = 10
	target := common.HexToAddress("0x8888888888888888888888888888888888888888")
	mock.Logs = []types.Log{frozenLog(idx.factoryAddress, target, 10, 0, common.HexToHash("0x01"))}

	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	idx.logHook = func(types.Log) error {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return errors.New("first pass fails")
		}
		return nil
	}

	leaderErr := make(chan error, 1)
	go func() { leaderErr <- idx.RunOnce(context.Background()) }()
	<-started
	waiterErr := make(chan error, 1)
	go func() { waiterErr <- idx.RunOnce(context.Background()) }()
	waitFor(t, "waiter to queue", func() bool {
		idx.runMu.Lock()
		defer idx.runMu.Unlock()
		return idx.queued != nil
	})
	close(release)

	if err := <-leaderErr; err == nil || !strings.Contains(err.Error(), "first pass fails") {
		t.Fatalf("leader: got %v, want its own pass's failure", err)
	}
	if err := <-waiterErr; err != nil {
		t.Fatalf("waiter: got %v, want its follow-up pass's success", err)
	}
	assertFrozen(t, idx, target, true)
}

// A caller whose context ends returns ctx.Err() at once, even while its pass
// (and passes queued behind it) are still running.
func TestRunOnce_CancelledCallerReturnsPromptly(t *testing.T) {
	idx, mock := setupTest(t, WithStartBlock(10))
	mock.BlockNum = 10
	mock.Logs = []types.Log{frozenLog(idx.factoryAddress, common.HexToAddress("0x99"), 10, 0, common.HexToHash("0x01"))}

	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var once sync.Once
	idx.logHook = func(types.Log) error {
		once.Do(func() { close(started) })
		<-release
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	leaderErr := make(chan error, 1)
	go func() { leaderErr <- idx.RunOnce(ctx) }()
	<-started
	waiterCtx, waiterCancel := context.WithCancel(context.Background())
	defer waiterCancel()
	go func() { _ = idx.RunOnce(waiterCtx) }()
	waitFor(t, "waiter to queue", func() bool {
		idx.runMu.Lock()
		defer idx.runMu.Unlock()
		return idx.queued != nil
	})

	cancel()
	select {
	case err := <-leaderErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled caller: got %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled caller did not return while its pass was blocked")
	}
}

// A panicking pass reports an error to its callers and still starts the
// queued pass, so neither its callers nor later ones are stranded.
func TestRunOnce_PanicReleasesQueuedCallers(t *testing.T) {
	idx, mock := setupTest(t, WithStartBlock(10))
	mock.BlockNum = 10
	target := common.HexToAddress("0xabababababababababababababababababababab")
	mock.Logs = []types.Log{frozenLog(idx.factoryAddress, target, 10, 0, common.HexToHash("0x01"))}

	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	idx.logHook = func(types.Log) error {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			panic("handler bug")
		}
		return nil
	}

	runRecovered := func(ch chan<- error) {
		defer func() {
			if r := recover(); r != nil {
				ch <- fmt.Errorf("panic escaped RunOnce: %v", r)
			}
		}()
		ch <- idx.RunOnce(context.Background())
	}
	leaderErr := make(chan error, 1)
	go runRecovered(leaderErr)
	<-started
	waiterErr := make(chan error, 1)
	go runRecovered(waiterErr)
	waitFor(t, "waiter to queue", func() bool {
		idx.runMu.Lock()
		defer idx.runMu.Unlock()
		return idx.queued != nil
	})
	close(release)

	select {
	case err := <-leaderErr:
		if err == nil || !strings.Contains(err.Error(), "indexer pass panicked") {
			t.Fatalf("leader: got %v, want a pass panic error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("leader stranded after panic")
	}
	select {
	case err := <-waiterErr:
		if err != nil {
			t.Fatalf("waiter: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued caller stranded after panic")
	}
	assertFrozen(t, idx, target, true)
	if err := idx.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce after panic: %v", err)
	}
}

// With no stored cursor, a failure inside the first lookback range must be
// retried even after the head moves on and the lookback start moves past it.
func TestRunOnce_FirstPassFailureStaysInScanRange(t *testing.T) {
	idx, mock := setupTest(t)
	mock.BlockNum = 300 // lookback starts at 50
	target := common.HexToAddress("0xcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd")
	mock.Logs = []types.Log{frozenLog(idx.factoryAddress, target, 50, 0, common.HexToHash("0x32"))}

	var calls atomic.Int32
	idx.logHook = func(types.Log) error {
		if calls.Add(1) == 1 {
			return errors.New("transient failure at block 50")
		}
		return nil
	}
	if err := idx.RunOnce(context.Background()); err == nil {
		t.Fatal("expected the first pass to fail")
	}
	assertCursor(t, idx, 49)

	mock.Lock()
	mock.BlockNum = 301
	mock.Unlock()
	if err := idx.RunOnce(context.Background()); err != nil {
		t.Fatalf("retry: %v", err)
	}
	assertFrozen(t, idx, target, true)
	assertCursor(t, idx, 301)
}

// A stored cursor of 0 means block 0 is indexed; it must not restart the
// lookback scan as if no cursor existed.
func TestRunOnce_CursorAtBlockZeroIsCommitted(t *testing.T) {
	idx, mock := setupTest(t)
	mock.BlockNum = 0
	if err := idx.RunOnce(context.Background()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	assertCursor(t, idx, 0)

	target := common.HexToAddress("0xefefefefefefefefefefefefefefefefefefefef")
	mock.Lock()
	mock.BlockNum = 1
	mock.Logs = []types.Log{frozenLog(idx.factoryAddress, target, 0, 0, common.HexToHash("0x00"))}
	mock.Unlock()
	if err := idx.RunOnce(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	// Block 0 was committed before this log was visible; it is not re-applied.
	assertFrozen(t, idx, target, false)
	assertCursor(t, idx, 1)
}

func milestoneLog(escrow common.Address, event string, msIdx uint8, block uint64, index uint, txHash common.Hash, args ...any) types.Log {
	ev := chain.EscrowABI.Events[event]
	data, err := ev.Inputs.NonIndexed().Pack(args...)
	if err != nil {
		panic(fmt.Sprintf("pack %s: %v", event, err))
	}
	topics := []common.Hash{ev.ID, common.BigToHash(big.NewInt(int64(msIdx)))}
	if event == "MilestoneApproved" {
		topics = append(topics, common.BytesToHash(common.HexToAddress("0x2222222222222222222222222222222222222222").Bytes()))
	}
	return types.Log{Address: escrow, Topics: topics, Data: data, BlockNumber: block, Index: index, TxHash: txHash}
}

func twoMilestoneViews() chain.MockEscrowViews {
	return chain.MockEscrowViews{
		QuorumPanel: []common.Address{common.HexToAddress("0x3333333333333333333333333333333333333333")},
		Milestones: []chain.MockMilestone{
			{Amount: big.NewInt(400), SubmissionDeadline: 1_900_000_000},
			{Amount: big.NewInt(600), SubmissionDeadline: 1_900_100_000},
		},
	}
}

// An escrow created directly on-chain (no API row) gets its milestone rows
// from the chain when it is indexed, so its milestone events apply.
func TestRunOnce_OnChainEscrowGetsMilestoneRows(t *testing.T) {
	idx, mock := setupTest(t, WithStartBlock(10))
	mock.BlockNum = 11
	escrowAddr := common.HexToAddress("0x1234123412341234123412341234123412341234")
	created := chain.MakeEscrowCreatedLog(idx.factoryAddress, 5, escrowAddr, common.HexToAddress("0x1111111111111111111111111111111111111111"))
	created.BlockNumber, created.Index, created.TxHash = 10, 0, common.HexToHash("0xc0")
	mock.EscrowViews = map[common.Address]chain.MockEscrowViews{escrowAddr: twoMilestoneViews()}
	mock.Logs = []types.Log{
		created,
		milestoneLog(escrowAddr, "MilestoneSubmitted", 0, 11, 0, common.HexToHash("0xc1"),
			[32]byte{0xaa}, "ipfs://m0", [32]byte{}),
	}

	if err := idx.RunOnce(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	e, err := idx.db.GetEscrowByAddress(context.Background(), escrowAddr.Hex())
	if err != nil {
		t.Fatalf("indexed escrow: %v", err)
	}
	if e.MilestoneCount != 2 {
		t.Fatalf("milestone_count = %d, want 2", e.MilestoneCount)
	}
	ms, err := idx.db.GetMilestonesByEscrow(context.Background(), e.ID)
	if err != nil {
		t.Fatalf("milestones: %v", err)
	}
	if len(ms) != 2 {
		t.Fatalf("milestone rows = %d, want 2", len(ms))
	}
	if ms[0].Status != "submitted" || ms[0].SubmissionURI != "ipfs://m0" || ms[0].Amount != "400" {
		t.Fatalf("milestone 0 = %+v", ms[0])
	}
	if ms[1].Status != "pending" || ms[1].Amount != "600" || ms[1].SubmissionDeadline != 1_900_100_000 {
		t.Fatalf("milestone 1 = %+v", ms[1])
	}
	assertCursor(t, idx, 11)
}

func createIndexedEscrow(t *testing.T, idx *Indexer, addr common.Address) *storage.Escrow {
	t.Helper()
	ctx := context.Background()
	task, err := idx.db.CreateTask(ctx, "t", "", "0x01")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	e, err := idx.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID: task.ID, ChainID: idx.chainID, FactoryAddress: idx.factoryAddress.Hex(),
		EscrowAddress: addr.Hex(), EscrowID: 9, Buyer: "0xB", Worker: "0xW", Arbitrator: "0xA",
		Amount: "1000", Status: "funded",
	})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}
	return e
}

// An escrow row that predates milestone indexing has no milestone rows. Its
// rows are backfilled from the chain instead of the event failing forever.
func TestRunOnce_BackfillsMissingMilestoneRows(t *testing.T) {
	idx, mock := setupTest(t, WithStartBlock(10))
	mock.BlockNum = 10
	escrowAddr := common.HexToAddress("0x5656565656565656565656565656565656565656")
	e := createIndexedEscrow(t, idx, escrowAddr)
	mock.EscrowViews = map[common.Address]chain.MockEscrowViews{escrowAddr: twoMilestoneViews()}
	mock.Logs = []types.Log{
		milestoneLog(escrowAddr, "MilestoneApproved", 0, 10, 0, common.HexToHash("0xd1"), uint64(1_800_000_000)),
	}

	if err := idx.RunOnce(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	ms, err := idx.db.GetMilestonesByEscrow(context.Background(), e.ID)
	if err != nil {
		t.Fatalf("milestones: %v", err)
	}
	if len(ms) != 2 || ms[0].Status != "approved" || ms[1].Status != "pending" {
		t.Fatalf("milestones after backfill = %+v", ms)
	}
	got, err := idx.db.GetEscrow(context.Background(), e.ID)
	if err != nil {
		t.Fatalf("get escrow: %v", err)
	}
	if got.MilestoneCount != 2 || got.CurrentMilestone != 1 {
		t.Fatalf("milestone_count = %d, current = %d; want 2, 1", got.MilestoneCount, got.CurrentMilestone)
	}
}

// A milestone write that fails rolls back the block, including the log's
// dedup row and the cursor, instead of being logged and committed.
func TestRunOnce_MilestoneWriteErrorRollsBackBlock(t *testing.T) {
	idx, mock := setupTest(t, WithStartBlock(10))
	mock.BlockNum = 10
	escrowAddr := common.HexToAddress("0x7878787878787878787878787878787878787878")
	e := createIndexedEscrow(t, idx, escrowAddr)
	if _, err := idx.db.CreateMilestone(context.Background(), &storage.MilestoneRecord{
		EscrowID: e.ID, MilestoneIndex: 0, Amount: "1000", Status: "pending",
	}); err != nil {
		t.Fatalf("create milestone: %v", err)
	}
	txHash := common.HexToHash("0xe1")
	// Milestone 3 has no row, so the status update fails.
	mock.Logs = []types.Log{
		milestoneLog(escrowAddr, "MilestoneApproved", 3, 10, 0, txHash, uint64(1_800_000_000)),
	}

	err := idx.RunOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "update milestone 3") {
		t.Fatalf("expected the milestone write error, got %v", err)
	}
	assertCursor(t, idx, 9)
	exists, err := idx.db.ChainLogExists(context.Background(), txHash.Hex(), 0)
	if err != nil {
		t.Fatalf("chain log lookup: %v", err)
	}
	if exists {
		t.Fatal("dedup row for the failed log was committed")
	}
}

// Chain reads that a block needs happen before its write transaction opens,
// so a slow RPC never holds SQLite's write lock.
func TestRunOnce_NoChainCallsInsideWriteTx(t *testing.T) {
	idx, mock := setupTest(t, WithStartBlock(10))
	mock.BlockNum = 10
	escrowAddr := common.HexToAddress("0x9a9a9a9a9a9a9a9a9a9a9a9a9a9a9a9a9a9a9a9a")
	created := chain.MakeEscrowCreatedLog(idx.factoryAddress, 6, escrowAddr, common.HexToAddress("0x1111111111111111111111111111111111111111"))
	created.BlockNumber, created.Index, created.TxHash = 10, 0, common.HexToHash("0xf0")
	mock.Logs = []types.Log{created}
	mock.EscrowViews = map[common.Address]chain.MockEscrowViews{escrowAddr: twoMilestoneViews()}
	var callsInTx, calls atomic.Int32
	mock.CallHook = func(ctx context.Context, _ common.Address) {
		calls.Add(1)
		if storage.InTx(ctx) {
			callsInTx.Add(1)
		}
	}

	if err := idx.RunOnce(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if calls.Load() == 0 {
		t.Fatal("expected contract reads for the new escrow")
	}
	if got := callsInTx.Load(); got != 0 {
		t.Fatalf("%d contract calls ran inside the block's write transaction", got)
	}
	if _, err := idx.db.GetEscrowByAddress(context.Background(), escrowAddr.Hex()); err != nil {
		t.Fatalf("indexed escrow: %v", err)
	}
}

// A failed contract read for a new escrow fails the block before its
// transaction, and the next pass retries it.
func TestRunOnce_EscrowDetailReadFailureRetries(t *testing.T) {
	idx, mock := setupTest(t, WithStartBlock(10))
	mock.BlockNum = 10
	escrowAddr := common.HexToAddress("0xbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbc")
	created := chain.MakeEscrowCreatedLog(idx.factoryAddress, 7, escrowAddr, common.HexToAddress("0x1111111111111111111111111111111111111111"))
	created.BlockNumber, created.Index, created.TxHash = 10, 0, common.HexToHash("0xf1")
	mock.Logs = []types.Log{created}
	mock.EscrowViews = map[common.Address]chain.MockEscrowViews{escrowAddr: {Err: errors.New("rpc down")}}

	if err := idx.RunOnce(context.Background()); err == nil || !strings.Contains(err.Error(), "rpc down") {
		t.Fatalf("expected the read failure, got %v", err)
	}
	assertCursor(t, idx, 9)

	mock.Lock()
	mock.EscrowViews = map[common.Address]chain.MockEscrowViews{escrowAddr: twoMilestoneViews()}
	mock.Unlock()
	if err := idx.RunOnce(context.Background()); err != nil {
		t.Fatalf("retry: %v", err)
	}
	e, err := idx.db.GetEscrowByAddress(context.Background(), escrowAddr.Hex())
	if err != nil {
		t.Fatalf("indexed escrow: %v", err)
	}
	ms, err := idx.db.GetMilestonesByEscrow(context.Background(), e.ID)
	if err != nil || len(ms) != 2 {
		t.Fatalf("milestones = %d (%v), want 2", len(ms), err)
	}
}

// Trigger starts a pass without waiting for the next poll tick.
func TestRun_TriggerStartsPass(t *testing.T) {
	idx, mock := setupTest(t, WithPollInterval(time.Hour), WithStartBlock(10))
	mock.BlockNum = 10
	target := common.HexToAddress("0x1313131313131313131313131313131313131313")
	mock.Logs = []types.Log{frozenLog(idx.factoryAddress, target, 10, 0, common.HexToHash("0x01"))}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go idx.Run(ctx)
	idx.Trigger()
	waitFor(t, "triggered pass", func() bool {
		frozen, err := idx.db.IsFrozenAddress(context.Background(), strings.ToLower(target.Hex()))
		return err == nil && frozen
	})
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}
