package mcpserver

import (
	"testing"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
)

func TestDecompositionServiceWiring(t *testing.T) {
	t.Parallel()

	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	s := &Server{db: db}
	svc := s.decompositionService()
	if svc == nil {
		t.Fatal("expected decomposition service")
	} else {
		if svc.DB != db {
			t.Fatal("expected decomposition service to use server DB dependency")
		}
		if svc.Bidding == nil {
			t.Fatal("expected decomposition service to wire bidding dependency")
		}
	}
}
