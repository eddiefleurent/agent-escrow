package mcpserver

import (
	"context"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Server struct {
	db    *storage.DB
	chain chain.ChainClient
	idx   *indexer.Indexer
	cfg   *config.Config
}

func Serve(ctx context.Context, db *storage.DB, chainClient chain.ChainClient, idx *indexer.Indexer, cfg *config.Config) error {
	s := &Server{
		db:    db,
		chain: chainClient,
		idx:   idx,
		cfg:   cfg,
	}

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "ai-delegation",
		Version: "1.0.0",
	}, nil)

	s.registerTools(srv)

	return srv.Run(ctx, &mcp.StdioTransport{})
}
