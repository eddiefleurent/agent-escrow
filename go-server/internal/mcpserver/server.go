package mcpserver

import (
	"context"
	"sync"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/decomposition"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/events"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	ucppkg "github.com/eddiefleurent/agent-escrow/go-server/internal/ucp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Server struct {
	db    *storage.DB
	chain chain.ChainClient
	idx   *indexer.Indexer
	cfg   *config.Config
	bus   *events.EventBus

	ucpOnce sync.Once
	ucpSvc  *ucppkg.Service
}

// Serve starts the MCP server over stdio. The bus parameter may be nil when
// event streaming is disabled.
func Serve(ctx context.Context, db *storage.DB, chainClient chain.ChainClient, idx *indexer.Indexer, cfg *config.Config, bus *events.EventBus) error {
	s := &Server{
		db:    db,
		chain: chainClient,
		idx:   idx,
		cfg:   cfg,
		bus:   bus,
	}

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "ai-delegation",
		Version: "1.0.0",
	}, nil)

	s.registerTools(srv)

	return srv.Run(ctx, &mcp.StdioTransport{})
}

func (s *Server) decompositionService() *decomposition.Service {
	return &decomposition.Service{
		DB:      s.db,
		Bidding: s.biddingService(),
	}
}
