package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenInMemoryDoesNotClampPoolToSingleConnection(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if got := db.SQLDB().Stats().MaxOpenConnections; got != 0 {
		t.Fatalf("expected max open connections to be 0 (unlimited) for shared in-memory sqlite, got %d", got)
	}
}

func TestOpenSharedMemoryURISharesStateAcrossHandles(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := Open(dsn)
	if err != nil {
		t.Fatalf("open shared in-memory db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	second, err := Open(dsn)
	if err != nil {
		t.Fatalf("open second shared-cache handle: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	ctx := context.Background()
	if _, err := db.SQLDB().ExecContext(ctx, "CREATE TABLE visibility_check (id INTEGER PRIMARY KEY, value TEXT NOT NULL)"); err != nil {
		t.Fatalf("create visibility_check table: %v", err)
	}
	if _, err := db.SQLDB().ExecContext(ctx, "INSERT INTO visibility_check (value) VALUES (?)", "shared"); err != nil {
		t.Fatalf("insert visibility_check row: %v", err)
	}

	var value string
	if err := second.SQLDB().QueryRowContext(ctx, "SELECT value FROM visibility_check WHERE id = 1").Scan(&value); err != nil {
		t.Fatalf("read visibility_check row from second handle: %v", err)
	}
	if value != "shared" {
		t.Fatalf("expected shared row visibility across in-memory handles, got %q", value)
	}
}

// A commit whose context is cancelled never runs COMMIT. The connection must
// not go back to the pool still inside BEGIN IMMEDIATE, or it keeps SQLite's
// write lock and every later writer gets SQLITE_BUSY.
func TestImmediateTxCancelledCommitReleasesWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// One pooled connection: the next caller reuses the connection Commit released.
	db.SQLDB().SetMaxOpenConns(1)

	cancelled, cancel := context.WithCancel(context.Background())
	err = db.RunInImmediateTx(cancelled, func(txCtx context.Context) error {
		if err := db.SetCursor(txCtx, 1, "probe", 7); err != nil {
			return err
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("commit with cancelled ctx: got %v, want context.Canceled", err)
	}

	// Same handle, same pooled connection.
	if err := db.RunInImmediateTx(context.Background(), func(txCtx context.Context) error {
		return db.SetCursor(txCtx, 1, "probe", 8)
	}); err != nil {
		t.Fatalf("writer on the released connection: %v", err)
	}

	// A separate handle on the same file must get the write lock straight away.
	other, err := Open(path)
	if err != nil {
		t.Fatalf("open second handle: %v", err)
	}
	t.Cleanup(func() { _ = other.Close() })
	if _, err := other.SQLDB().ExecContext(context.Background(), "PRAGMA busy_timeout = 0"); err != nil {
		t.Fatalf("set busy timeout: %v", err)
	}
	other.SQLDB().SetMaxOpenConns(1)
	if err := other.RunInImmediateTx(context.Background(), func(txCtx context.Context) error {
		return other.SetCursor(txCtx, 1, "probe", 9)
	}); err != nil {
		t.Fatalf("second writer: %v", err)
	}
	got, err := db.GetCursor(context.Background(), 1, "probe")
	if err != nil {
		t.Fatalf("get cursor: %v", err)
	}
	if got != 9 {
		t.Fatalf("cursor = %d, want 9 (cancelled write must have rolled back)", got)
	}
}
