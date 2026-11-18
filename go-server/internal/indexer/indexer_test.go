package indexer

import (
	"context"
	"errors"
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
	mock.Delay = 15 * time.Millisecond
	factory := idx.factoryAddress
	target := common.HexToAddress("0x5555555555555555555555555555555555555555")
	mock.Logs = []types.Log{
		frozenLog(factory, target, 10, 0, common.HexToHash("0x0a")),
	}

	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	idx.logHook = func(types.Log) error {
		n := inFlight.Add(1)
		for {
			old := maxInFlight.Load()
			if n <= old || maxInFlight.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
		return nil
	}

	const n = 8
	errCh := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- idx.RunOnce(context.Background())
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent RunOnce: %v", err)
		}
	}
	if got := maxInFlight.Load(); got != 1 {
		t.Fatalf("overlapping RunOnce calls: max in flight = %d", got)
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
	go func() {
		leaderErr <- idx.RunOnce(context.Background())
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("leader did not enter log processing")
	}

	mock.Lock()
	mock.BlockNum = 20
	mock.Logs = append(mock.Logs, frozenLog(factory, later, 15, 0, common.HexToHash("0x0f")))
	mock.Unlock()

	waiterErr := make(chan error, 1)
	go func() {
		waiterErr <- idx.RunOnce(context.Background())
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		idx.runMu.Lock()
		n := len(idx.runWaiters)
		idx.runMu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("waiter did not register during the in-flight run")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)

	if err := <-leaderErr; err != nil {
		t.Fatalf("leader: %v", err)
	}
	if err := <-waiterErr; err != nil {
		t.Fatalf("waiter: %v", err)
	}
	assertCursor(t, idx, 20)
	assertFrozen(t, idx, first, true)
	assertFrozen(t, idx, later, true)
}
