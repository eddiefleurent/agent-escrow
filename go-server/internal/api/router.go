package api

import (
	"net/http"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/a2a"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/ap2"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/events"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/x402"
)

// NewRouter creates the HTTP handler with all API routes.
// The bus parameter may be nil when event streaming is disabled.
func NewRouter(db *storage.DB, chainClient chain.ChainClient, idx *indexer.Indexer, cfg *config.Config, bus *events.EventBus) http.Handler {
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
	mux.HandleFunc("POST /api/v1/escrows/{id}/deposit-stake", h.DepositStake)
	mux.HandleFunc("POST /api/v1/escrows/{id}/submit", h.SubmitWork)
	mux.HandleFunc("POST /api/v1/escrows/{id}/approve", h.ApproveWork)
	mux.HandleFunc("POST /api/v1/escrows/{id}/dispute", h.DisputeWork)
	mux.HandleFunc("POST /api/v1/escrows/{id}/resolve", h.ResolveDispute)
	mux.HandleFunc("POST /api/v1/escrows/{id}/abort-milestones", h.AbortRemainingMilestones)
	mux.HandleFunc("POST /api/v1/escrows/{id}/activate-backup", h.ActivateBackup)
	mux.HandleFunc("GET /api/v1/reputation/{address}", h.GetReputation)

	// RFQ bidding protocol endpoints (paper §6.1)
	mux.HandleFunc("POST /api/v1/rfqs", h.CreateRFQ)
	mux.HandleFunc("GET /api/v1/rfqs", h.ListRFQs)
	mux.HandleFunc("GET /api/v1/rfqs/{id}", h.GetRFQ)
	mux.HandleFunc("POST /api/v1/rfqs/{id}/cancel", h.CancelRFQ)
	mux.HandleFunc("POST /api/v1/rfqs/{id}/bids/commit", h.CommitBid)
	mux.HandleFunc("POST /api/v1/rfqs/{id}/bids/reveal", h.RevealBid)
	mux.HandleFunc("GET /api/v1/rfqs/{id}/bids", h.ListBids)
	mux.HandleFunc("POST /api/v1/rfqs/{id}/accept", h.AcceptBid)

	// Emergency response protocol endpoints (paper §4.9)
	if cfg.EmergencyEnabled {
		mux.HandleFunc("POST /api/v1/emergency/freeze-address", h.FreezeAddress)
		mux.HandleFunc("POST /api/v1/emergency/unfreeze-address", h.UnfreezeAddress)
		mux.HandleFunc("POST /api/v1/emergency/freeze-escrow", h.FreezeEscrow)
		mux.HandleFunc("POST /api/v1/emergency/unfreeze-escrow", h.UnfreezeEscrow)
		mux.HandleFunc("POST /api/v1/emergency/resolve", h.EmergencyResolve)
		mux.HandleFunc("GET /api/v1/emergency/frozen-addresses", h.ListFrozenAddresses)
		mux.HandleFunc("GET /api/v1/emergency/actions", h.ListEmergencyActions)
	}

	if cfg.WebhookMode() {
		wh := NewWebhookHandler(idx, cfg.CDPWebhookSecret, bus)
		mux.HandleFunc("POST /webhooks/cdp", wh.HandleCDPWebhook)
	}

	// AP2 mandate-to-escrow bridge (paper §6: AP2 stake-on-bid + conditional settlement)
	{
		var x402Client *x402.Client
		if cfg.X402Enabled {
			x402Client = x402.NewClient(cfg.X402FacilitatorURL)
		}
		ap2Svc := &ap2.Service{
			DB:    db,
			Chain: chainClient,
			Idx:   idx,
			Cfg:   cfg,
			X402:  x402Client,
		}
		ap2Handler := ap2.NewHandler(ap2Svc)
		mux.HandleFunc("POST /api/v1/ap2/fund", ap2Handler.FundViaMandate)
		mux.HandleFunc("POST /api/v1/ap2/validate", ap2Handler.ValidateMandate)
		mux.HandleFunc("GET /api/v1/ap2/mandates/{id}", ap2Handler.GetMandate)
	}

	// Real-time event subscriptions (paper §4.5: configurable granularity L0-L3)
	if bus != nil && cfg.EventsEnabled {
		sh := NewStreamHandler(bus, cfg.CORSOrigins)
		mux.HandleFunc("GET /api/v1/events", sh.HandleSSE)
		mux.HandleFunc("GET /api/v1/escrows/{id}/events", sh.HandleSSE)
		mux.HandleFunc("GET /api/v1/events/ws", sh.HandleWebSocket)
	}

	// A2A settlement adapter routes (paper §6: A2A Task object extension)
	if cfg.A2AEnabled {
		a2aSvc := &a2a.Service{
			DB:    db,
			Chain: chainClient,
			Idx:   idx,
			Cfg:   cfg,
		}
		a2aHandler := a2a.NewHandler(a2aSvc)
		mux.HandleFunc("GET /.well-known/agent.json", a2aHandler.ServeAgentCard)
		mux.HandleFunc("POST /a2a", a2aHandler.HandleJSONRPC)
	}

	var handler http.Handler = mux
	handler = timeoutMiddleware(cfg.RequestTimeout, cfg.TxTimeout, handler)
	handler = corsMiddleware(cfg.CORSOrigins, handler)
	handler = loggingMiddleware(handler)
	handler = recoveryMiddleware(handler)

	return handler
}
