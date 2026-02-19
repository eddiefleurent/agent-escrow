package api

import (
	"net/http"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
)

func NewRouter(db *storage.DB, chainClient chain.ChainClient, idx *indexer.Indexer, cfg *config.Config) http.Handler {
	h := &Handlers{
		db:    db,
		chain: chainClient,
		idx:   idx,
		cfg:   cfg,
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health", h.Health)
	mux.HandleFunc("POST /api/v1/escrows", h.CreateEscrow)
	mux.HandleFunc("GET /api/v1/escrows", h.ListEscrows)
	mux.HandleFunc("GET /api/v1/escrows/{id}", h.GetEscrow)
	mux.HandleFunc("POST /api/v1/escrows/{id}/fund", h.FundEscrow)
	mux.HandleFunc("POST /api/v1/escrows/{id}/submit", h.SubmitWork)
	mux.HandleFunc("POST /api/v1/escrows/{id}/approve", h.ApproveWork)
	mux.HandleFunc("POST /api/v1/escrows/{id}/dispute", h.DisputeWork)
	mux.HandleFunc("POST /api/v1/escrows/{id}/resolve", h.ResolveDispute)

	var handler http.Handler = mux
	handler = corsMiddleware(handler)
	handler = loggingMiddleware(handler)
	handler = recoveryMiddleware(handler)

	return handler
}
