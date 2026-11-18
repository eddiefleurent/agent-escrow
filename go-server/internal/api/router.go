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
	ucppkg "github.com/eddiefleurent/agent-escrow/go-server/internal/ucp"
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

	// The edge policy is applied per route at registration, so the limiter
	// and timeout a request gets follow the route ServeMux actually matched
	// (including HEAD-to-GET fallback and escaped slashes inside {id}),
	// and each request is charged against exactly one limiter.
	//
	// Admin routes get their own pre-auth limiter on every method, so token
	// guessing against admin GETs is throttled like any write. It is separate
	// from the public write limiter so a client that exhausts its public
	// budget can still reach an emergency admin route, and admin probes
	// cannot drain the public budget.
	edge := routePolicy{
		requestTimeout: cfg.RequestTimeout,
		txTimeout:      cfg.TxTimeout,
		maxBody:        cfg.MaxBodyBytes,
		trusted:        cfg.TrustedProxies,
		publicLimiter:  newRateLimiter("public", cfg.RateLimitRPS, cfg.RateLimitBurst),
		adminLimiter:   newRateLimiter("admin", cfg.RateLimitRPS, cfg.RateLimitBurst),
	}
	mux := http.NewServeMux()
	handle := func(pattern string, handler http.HandlerFunc) {
		mux.Handle(pattern, edge.public(handler))
	}
	handleAdmin := func(pattern string, handler http.HandlerFunc) {
		mux.Handle(pattern, edge.admin(requireAdmin(cfg.AdminToken, cfg.OwnerAddress, handler)))
	}
	handleStream := func(pattern string, handler http.HandlerFunc) {
		mux.Handle(pattern, edge.stream(handler))
	}

	handle("GET /api/v1/health", h.Health)
	handle("POST /api/v1/escrows", h.CreateEscrow)
	handle("GET /api/v1/escrows", h.ListEscrows)
	handle("GET /api/v1/escrows/{id}", h.GetEscrow)
	handle("POST /api/v1/escrows/{id}/fund", h.FundEscrow)
	handle("POST /api/v1/escrows/{id}/deposit-stake", h.DepositStake)
	handle("POST /api/v1/escrows/{id}/deposit-verifier-stake", h.DepositVerifierStake)
	handle("POST /api/v1/escrows/{id}/withdraw-stake", h.WithdrawStake)
	handle("POST /api/v1/escrows/{id}/submit", h.SubmitWork)
	handle("POST /api/v1/escrows/{id}/approve", h.ApproveWork)
	handle("POST /api/v1/escrows/{id}/verify-approve", h.VerifyAndApprove)
	handle("POST /api/v1/escrows/{id}/quorum-vote", h.CastVerifierVote)
	handle("POST /api/v1/escrows/{id}/dispute", h.DisputeWork)
	handle("POST /api/v1/escrows/{id}/resolve", h.ResolveDispute)
	handle("POST /api/v1/escrows/{id}/abort-milestones", h.AbortRemainingMilestones)
	handle("POST /api/v1/escrows/{id}/activate-backup", h.ActivateBackup)
	handle("GET /api/v1/escrows/{id}/attestation-chain", h.GetAttestationChain)
	handle("GET /api/v1/escrows/{id}/children", h.ListChildEscrows)
	handle("POST /api/v1/escrows/{id}/checkpoints", h.CommitCheckpoint)
	handle("GET /api/v1/escrows/{id}/checkpoints", h.ListCheckpoints)
	handle("GET /api/v1/escrows/{id}/checkpoints/latest", h.GetLatestCheckpoint)
	handle("GET /api/v1/reputation/{address}", h.GetReputation)
	handle("POST /api/v1/dcts/mint", h.MintDCT)
	handle("POST /api/v1/dcts/delegate", h.DelegateDCT)
	handle("POST /api/v1/dcts/introspect", h.IntrospectDCT)
	handle("POST /api/v1/dcts/revoke", h.RevokeDCT)
	handleAdmin("GET /api/v1/dcts/audit", h.ListDCTAudit)
	handle("GET /api/v1/escrows/{id}/dcts", h.ListEscrowDCTs)

	// Bazaar-compatible discovery metadata (paper §4.6 Table 3, Bazaar extensions)
	handle("GET /api/v1/bazaar/discovery", h.BazaarDiscovery)

	// RFQ bidding protocol endpoints (paper §6.1)
	handle("POST /api/v1/rfqs", h.CreateRFQ)
	handle("GET /api/v1/rfqs", h.ListRFQs)
	handle("GET /api/v1/rfqs/{id}", h.GetRFQ)
	handle("POST /api/v1/rfqs/{id}/cancel", h.CancelRFQ)
	handle("POST /api/v1/rfqs/{id}/bids/commit", h.CommitBid)
	handle("POST /api/v1/rfqs/{id}/bids/reveal", h.RevealBid)
	handle("GET /api/v1/rfqs/{id}/bids", h.ListBids)
	handle("POST /api/v1/rfqs/{id}/accept", h.AcceptBid)

	// Contract-first decomposition endpoints (paper §4.1)
	handle("POST /api/v1/decompositions", h.CreateDecomposition)
	handle("GET /api/v1/decompositions", h.ListDecompositions)
	handle("GET /api/v1/decompositions/{id}", h.GetDecomposition)
	handle("POST /api/v1/decompositions/{id}/finalize", h.FinalizeDecomposition)

	// Emergency response protocol endpoints (paper §4.9)
	if cfg.EmergencyEnabled {
		handleAdmin("POST /api/v1/dcts/emergency-override", h.EmergencyOverrideDCT)
		handleAdmin("POST /api/v1/emergency/freeze-address", h.FreezeAddress)
		handleAdmin("POST /api/v1/emergency/unfreeze-address", h.UnfreezeAddress)
		handleAdmin("POST /api/v1/emergency/freeze-escrow", h.FreezeEscrow)
		handleAdmin("POST /api/v1/emergency/unfreeze-escrow", h.UnfreezeEscrow)
		handleAdmin("POST /api/v1/emergency/resolve", h.EmergencyResolve)
		handleAdmin("GET /api/v1/emergency/frozen-addresses", h.ListFrozenAddresses)
		handleAdmin("GET /api/v1/emergency/actions", h.ListEmergencyActions)
	}

	if cfg.WebhookMode() {
		wh := NewWebhookHandler(idx, cfg.CDPWebhookSecret, bus)
		handle("POST /webhooks/cdp", wh.HandleCDPWebhook)
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
		handle("POST /api/v1/ap2/fund", ap2Handler.FundViaMandate)
		handle("POST /api/v1/ap2/validate", ap2Handler.ValidateMandate)
		handle("GET /api/v1/ap2/mandates/{id}", ap2Handler.GetMandate)
	}

	// Real-time event subscriptions (paper §4.5: configurable granularity L0-L3)
	if bus != nil && cfg.EventsEnabled {
		sh := NewStreamHandler(bus, cfg.CORSOrigins)
		handleStream("GET /api/v1/events", sh.HandleSSE)
		handleStream("GET /api/v1/escrows/{id}/events", sh.HandleSSE)
		handleStream("GET /api/v1/events/ws", sh.HandleWebSocket)
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
		handle("GET /.well-known/agent.json", a2aHandler.ServeAgentCard)
		handle("POST /a2a", a2aHandler.HandleJSONRPC)
	}

	// UCP fulfillment adapter routes (roadmap item 22).
	if cfg.UCPEnabled {
		escrowSvc := h.escrowService()
		ucpSvc := ucppkg.NewService(db, escrowSvc, cfg.UCPProviderName, cfg.UCPBaseURL)
		ucpHandler := ucppkg.NewHandler(ucpSvc)
		handle("GET /.well-known/ucp", ucpHandler.WellKnown)
		handle("POST /api/v1/ucp/checkouts", ucpHandler.CreateCheckout)
		handle("GET /api/v1/ucp/checkouts/{checkout_id}", ucpHandler.GetCheckout)
		handle("PATCH /api/v1/ucp/checkouts/{checkout_id}", ucpHandler.UpdateCheckout)
		handle("POST /api/v1/ucp/checkouts/{checkout_id}/complete", ucpHandler.CompleteCheckout)
		handle("POST /api/v1/ucp/checkouts/{checkout_id}/cancel", ucpHandler.CancelCheckout)
	}

	// Outermost first: request ID, recovery, logging, CORS, then the mux
	// and its per-route policy (rate limit, body cap, timeout). Requests
	// with no route are charged to the public limiter by edge.unmatched. Request ID
	// sits outside recovery so a recovered panic still logs and returns the
	// id; recovery wraps everything else. CORS precedes the per-route 429/413
	// guards so an allowed browser origin can read those errors and their
	// headers. The rate limit precedes the body cap so throttled requests are
	// rejected before their body is buffered.
	var handler = edge.unmatched(mux)
	handler = corsMiddleware(cfg.CORSOrigins, handler)
	handler = loggingMiddleware(handler)
	handler = recoveryMiddleware(handler)
	handler = requestIDMiddleware(handler)

	return handler
}
