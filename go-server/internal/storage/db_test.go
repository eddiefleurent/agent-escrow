package storage

import "testing"

func TestOpenInMemoryDoesNotClampPoolToSingleConnection(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if got := db.SQLDB().Stats().MaxOpenConnections; got != 0 {
		t.Fatalf("expected default max open connections for shared in-memory sqlite, got %d", got)
	}
}
