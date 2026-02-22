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
	"github.com/eddiefleurent/agent-escrow/go-server/internal/events"
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

	// Event bus for real-time event streaming (paper §4.5: configurable granularity L0-L3).
	var bus *events.EventBus
	if cfg.EventsEnabled {
		bus = events.NewEventBus(cfg.EventsBufferSize)
		slog.Info("event bus enabled",
			"buffer_size", cfg.EventsBufferSize,
			"heartbeat_interval", cfg.EventsHeartbeatInterval,
		)
	}

	idxOpts := []indexer.Option{}
	if cfg.StartBlock > 0 {
		idxOpts = append(idxOpts, indexer.WithStartBlock(cfg.StartBlock))
	}
	if bus != nil {
		idxOpts = append(idxOpts, indexer.WithEventBus(bus))
	}
	idx := indexer.New(db, chainClient, cfg.FactoryAddress, idxOpts...)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// The polling indexer always runs for escrow-level events (each TaskEscrow
	// is a separate contract). When CDP_WEBHOOK_SECRET is set, factory events
	// additionally arrive in real-time via POST /webhooks/cdp.
	go idx.Run(ctx)
	if cfg.WebhookMode() {
		slog.Info("CDP webhook mode enabled for factory events")
	}

	if bus != nil {
		go bus.RunHeartbeat(ctx, cfg.EventsHeartbeatInterval)
	}

	if cfg.MCPTransport == "stdio" {
		go func() {
			if err := mcpserver.Serve(ctx, db, chainClient, idx, cfg, bus); err != nil {
				slog.Error("mcp server exited", "error", err)
			}
		}()
	}

	router := api.NewRouter(db, chainClient, idx, cfg, bus)
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
