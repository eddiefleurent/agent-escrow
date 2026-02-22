package indexer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
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

func TestWithMaxConsecutiveFailures_Override(t *testing.T) {
	idx, _ := setupTest(t, WithMaxConsecutiveFailures(42))
	if idx.maxConsecFailures != 42 {
		t.Fatalf("expected 42, got %d", idx.maxConsecFailures)
	}
}
