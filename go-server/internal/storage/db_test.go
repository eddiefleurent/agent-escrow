package storage

import (
	"context"
	"fmt"
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
