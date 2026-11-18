package mcpserver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/cli"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/events"
	"github.com/spf13/cobra"
)

type transportFeature struct {
	http, mcp, cli string
}

var parityFeatures = []transportFeature{
	{"POST /api/v1/escrows", "create_escrow", "escrow create"},
	{"GET /api/v1/escrows", "list_escrows", "escrow list"},
	{"GET /api/v1/escrows/{id}", "get_escrow", "escrow get"},
	{"POST /api/v1/escrows/{id}/fund", "fund_escrow", "escrow fund"},
	{"POST /api/v1/escrows/{id}/deposit-stake", "deposit_stake", "escrow stake"},
	{"POST /api/v1/escrows/{id}/deposit-verifier-stake", "deposit_verifier_stake", "escrow verifier-stake"},
	{"POST /api/v1/escrows/{id}/withdraw-stake", "withdraw_stake", "escrow withdraw-stake"},
	{"POST /api/v1/escrows/{id}/submit", "submit_work", "escrow submit"},
	{"POST /api/v1/escrows/{id}/approve", "approve_work", "escrow approve"},
	{"POST /api/v1/escrows/{id}/verify-approve", "verify_and_approve", "escrow verify-approve"},
	{"POST /api/v1/escrows/{id}/quorum-vote", "cast_verifier_vote", "escrow quorum-vote"},
	{"POST /api/v1/escrows/{id}/dispute", "dispute_work", "escrow dispute"},
	{"POST /api/v1/escrows/{id}/resolve", "resolve_dispute", "escrow resolve"},
	{"POST /api/v1/escrows/{id}/cancel", "cancel_before_funding", "escrow cancel"},
	{"POST /api/v1/escrows/{id}/claim-timeout", "claim_timeout_refund", "escrow claim-timeout"},
	{"POST /api/v1/escrows/{id}/claim-arbitrator-timeout", "claim_arbitrator_timeout", "escrow claim-arbitrator-timeout"},
	{"POST /api/v1/escrows/{id}/expire-no-quorum", "expire_no_quorum", "escrow expire-no-quorum"},
	{"POST /api/v1/escrows/{id}/claim-milestone-timeout", "claim_milestone_timeout_refund", "escrow claim-milestone-timeout"},
	{"POST /api/v1/escrows/{id}/claim-milestone-arbitrator-timeout", "claim_milestone_arbitrator_timeout", "escrow claim-milestone-arbitrator-timeout"},
	{"POST /api/v1/escrows/{id}/expire-milestone-no-quorum", "expire_milestone_no_quorum", "escrow expire-milestone-no-quorum"},
	{"POST /api/v1/escrows/{id}/abort-milestones", "abort_remaining_milestones", "escrow abort"},
	{"POST /api/v1/escrows/{id}/activate-backup", "activate_backup", "escrow backup"},
	{"GET /api/v1/escrows/{id}/attestation-chain", "get_attestation_chain", "escrow attestation-chain"},
	{"POST /api/v1/escrows/{id}/checkpoints", "commit_checkpoint", "escrow checkpoint-commit"},
	{"GET /api/v1/escrows/{id}/checkpoints", "list_checkpoints", "escrow checkpoints"},
	{"GET /api/v1/escrows/{id}/checkpoints/latest", "get_latest_checkpoint", "escrow checkpoint-latest"},
	{"GET /api/v1/reputation/{address}", "get_reputation", "reputation get"},
	{"POST /api/v1/dcts/mint", "mint_dct", "dct mint"},
	{"POST /api/v1/dcts/delegate", "delegate_dct", "dct delegate"},
	{"POST /api/v1/dcts/introspect", "introspect_dct", "dct introspect"},
	{"POST /api/v1/dcts/revoke", "revoke_dct", "dct revoke"},
	{"GET /api/v1/dcts/audit", "list_dct_audit", "dct audit"},
	{"POST /api/v1/dcts/emergency-override", "emergency_override_dct", "dct emergency-override"},
	{"POST /api/v1/rfqs", "create_rfq", "rfq create"},
	{"POST /api/v1/rfqs/{id}/bids/commit", "commit_bid", "bid commit"},
	{"POST /api/v1/rfqs/{id}/bids/reveal", "reveal_bid", "bid reveal"},
	{"GET /api/v1/rfqs/{id}/bids", "list_bids", "bid list"},
	{"POST /api/v1/rfqs/{id}/accept", "accept_bid", "bid accept"},
	{"POST /api/v1/decompositions", "create_decomposition", "decomposition create"},
	{"GET /api/v1/decompositions", "list_decompositions", "decomposition list"},
	{"GET /api/v1/decompositions/{id}", "get_decomposition", "decomposition get"},
	{"POST /api/v1/decompositions/{id}/finalize", "finalize_decomposition", "decomposition finalize"},
	{"POST /api/v1/emergency/freeze-address", "freeze_address", "emergency freeze-address"},
	{"POST /api/v1/emergency/unfreeze-address", "unfreeze_address", "emergency unfreeze-address"},
	{"POST /api/v1/emergency/freeze-escrow", "freeze_escrow", "emergency freeze-escrow"},
	{"POST /api/v1/emergency/unfreeze-escrow", "unfreeze_escrow", "emergency unfreeze-escrow"},
	{"POST /api/v1/emergency/resolve", "emergency_resolve", "emergency resolve"},
	{"GET /api/v1/emergency/frozen-addresses", "list_frozen_addresses", "emergency frozen-addresses"},
	{"GET /api/v1/emergency/actions", "list_emergency_actions", "emergency actions"},
	{"POST /api/v1/ap2/fund", "fund_via_mandate", "ap2 fund"},
	{"POST /api/v1/ucp/checkouts", "ucp_create_checkout", "ucp create"},
	{"GET /api/v1/ucp/checkouts/{checkout_id}", "ucp_get_checkout", "ucp get"},
	{"PATCH /api/v1/ucp/checkouts/{checkout_id}", "ucp_update_checkout", "ucp update"},
	{"POST /api/v1/ucp/checkouts/{checkout_id}/complete", "ucp_complete_checkout", "ucp complete"},
	{"POST /api/v1/ucp/checkouts/{checkout_id}/cancel", "ucp_cancel_checkout", "ucp cancel"},
}

var transportExceptions = []transportFeature{
	// A2A peers speak JSON-RPC over HTTP.
	{http: "POST /a2a"},
	// HTTP serves SSE; the CLI subscription command is an SSE client.
	{http: "GET /api/v1/events", cli: "events subscribe"},
	{http: "GET /api/v1/escrows/{id}/events"},
	// The WebSocket upgrade is a separate HTTP streaming endpoint.
	{http: "GET /api/v1/events/ws"},
	// MCP provides cursor-based event polling through this tool.
	{mcp: "subscribe_events"},
	// CDP sends provider webhooks, and Bazaar crawlers use the discovery URL.
	{http: "POST /webhooks/cdp"},
	{http: "GET /api/v1/bazaar/discovery"},
}

// Existing gaps outside fund recovery. Record them individually so the test
// catches any new skew, and move entries to parityFeatures when gaps close.
var preExistingTransportGaps = []transportFeature{
	{http: "GET /api/v1/health", cli: "health"},
	{http: "GET /api/v1/escrows/{id}/children", cli: "escrow children"},
	{http: "GET /api/v1/escrows/{id}/dcts", cli: "dct list-escrow"},
	{http: "GET /api/v1/rfqs", cli: "rfq list"},
	{http: "GET /api/v1/rfqs/{id}", cli: "rfq get"},
	{http: "POST /api/v1/rfqs/{id}/cancel", cli: "rfq cancel"},
	{http: "POST /api/v1/ap2/validate", cli: "ap2 validate"},
	{http: "GET /api/v1/ap2/mandates/{id}", cli: "ap2 mandate"},
	{http: "GET /.well-known/agent.json", mcp: "get_agent_card"},
	{http: "GET /.well-known/ucp", cli: "ucp profile"},
}

func TestTransportParity(t *testing.T) {
	cfg := &config.Config{
		EmergencyEnabled: true, MCPEmergencyEnabled: true,
		A2AEnabled: true, UCPEnabled: true, EventsEnabled: true,
	}
	session := connectMCP(t, &Server{cfg: cfg, bus: events.NewEventBus(1)})
	mcpTools := map[string]bool{}
	for tool, err := range session.Tools(t.Context(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		mcpTools[tool.Name] = true
	}

	cliCommands := map[string]bool{}
	var visit func(*cobra.Command)
	visit = func(command *cobra.Command) {
		if command.Runnable() {
			cliCommands[strings.TrimPrefix(command.CommandPath(), "escrow-cli ")] = true
		}
		for _, child := range command.Commands() {
			visit(child)
		}
	}
	visit(cli.NewRootCmd(io.Discard, io.Discard))

	sets := []struct {
		name       string
		registered map[string]bool
		read       func(transportFeature) string
	}{
		{"HTTP route", registeredHTTPRoutes(t), func(f transportFeature) string { return f.http }},
		{"MCP tool", mcpTools, func(f transportFeature) string { return f.mcp }},
		{"CLI command", cliCommands, func(f transportFeature) string { return f.cli }},
	}
	for _, set := range sets {
		declared := map[string]bool{}
		for _, feature := range parityFeatures {
			name := set.read(feature)
			if name == "" {
				t.Errorf("feature %+v lacks %s parity", feature, set.name)
			}
		}
		for _, group := range [][]transportFeature{parityFeatures, transportExceptions, preExistingTransportGaps} {
			for _, feature := range group {
				name := set.read(feature)
				if name == "" {
					continue
				}
				if declared[name] {
					t.Errorf("%s %q appears twice in the parity inventory", set.name, name)
				}
				declared[name] = true
				if !set.registered[name] {
					t.Errorf("declared %s %q is missing", set.name, name)
				}
			}
		}
		for name := range set.registered {
			if !declared[name] {
				t.Errorf("undeclared %s %q", set.name, name)
			}
		}
	}
}

func registeredHTTPRoutes(t *testing.T) map[string]bool {
	t.Helper()
	// ServeMux has no enumeration API. Read router.go's actual registrations
	// so that new routes are detectable even when they are feature-gated.
	file, err := parser.ParseFile(token.NewFileSet(), "../api/router.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	routes := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch f := call.Fun.(type) {
		case *ast.SelectorExpr:
			mux, ok := f.X.(*ast.Ident)
			if !ok || mux.Name != "mux" || (f.Sel.Name != "HandleFunc" && f.Sel.Name != "Handle") {
				return true
			}
		case *ast.Ident:
			if f.Name != "handleAdmin" {
				return true
			}
		default:
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok {
			// The admin helper forwards the patterns declared at its call sites.
			if id, ok := call.Args[0].(*ast.Ident); ok && id.Name == "pattern" {
				return true
			}
			t.Fatal("HTTP route registration needs a literal pattern for parity coverage")
		}
		pattern, err := strconv.Unquote(literal.Value)
		if err != nil {
			t.Fatal(err)
		}
		if routes[pattern] {
			t.Errorf("HTTP route %q is registered twice", pattern)
		}
		routes[pattern] = true
		return true
	})
	return routes
}
