package storage

import (
	"context"
	"fmt"
	"testing"
)

func TestOpenInMemoryDoesNotClampPoolToSingleConnection(t *testing.T) {
	before := inMemoryDBCounter.Load()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if got := db.SQLDB().Stats().MaxOpenConnections; got != 0 {
		t.Fatalf("expected default max open connections for shared in-memory sqlite, got %d", got)
	}

	id := inMemoryDBCounter.Load()
	if id <= before {
		t.Fatalf("expected in-memory db counter to advance, before=%d after=%d", before, id)
	}

	second, err := Open(fmt.Sprintf("file:agent_escrow_mem_%d?mode=memory&cache=shared", id))
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
