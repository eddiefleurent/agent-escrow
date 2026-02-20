package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/api"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/mcpserver"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Load() already runs Validate() and rejects fatal errors. This second call
	// is intentional: it surfaces non-fatal warnings (e.g. offline mode) via slog.
	if result := cfg.Validate(); len(result.Warnings) > 0 {
		for _, w := range result.Warnings {
			slog.Warn("config", "warning", w)
		}
	}

	db, err := storage.Open(cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to open storage", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	chainClient, err := chain.NewClient(cfg.RPCURL, cfg.PrivateKey, cfg.ChainID,
		chain.WithLogChunkSize(cfg.LogChunkSize),
	)
	if err != nil {
		slog.Error("failed to create chain client", "error", err)
		os.Exit(1)
	}

	idxOpts := []indexer.Option{}
	if cfg.StartBlock > 0 {
		idxOpts = append(idxOpts, indexer.WithStartBlock(cfg.StartBlock))
	}
	idx := indexer.New(db, chainClient, cfg.FactoryAddress, idxOpts...)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go idx.Run(ctx)

	if cfg.MCPTransport == "stdio" {
		go func() {
			if err := mcpserver.Serve(ctx, db, chainClient, idx, cfg); err != nil {
				slog.Error("mcp server exited", "error", err)
			}
		}()
	}

	router := api.NewRouter(db, chainClient, idx, cfg)
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	listenErrCh := make(chan error, 1)
	go func() {
		slog.Info("http server starting", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			listenErrCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutting down")
	case err := <-listenErrCh:
		slog.Error("http server failed", "error", err)
		cancel()
	case err := <-idx.Err():
		slog.Error("indexer fatal failure, initiating shutdown", "error", err)
		cancel()
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown error", "error", err)
	}
}
