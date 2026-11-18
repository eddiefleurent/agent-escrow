package mcpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/api"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/ethereum/go-ethereum/common"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCreateEscrowTransportEquivalence(t *testing.T) {
	const (
		buyer      = "0x1000000000000000000000000000000000000001"
		worker     = "0x2000000000000000000000000000000000000002"
		verifier   = "0x3000000000000000000000000000000000000003"
		arbitrator = "0x4000000000000000000000000000000000000004"
		extra      = "0x5000000000000000000000000000000000000005"
		zero       = "0x0000000000000000000000000000000000000000"
		overflow   = "9223372036854775808"
	)
	milestones := func(amount, deadline string) []map[string]string {
		return []map[string]string{{"amount": amount, "submission_deadline": deadline}}
	}
	cases := []struct {
		name          string
		fields        map[string]any
		parentAddress string
		wantError     string
		noFloor       bool
		foreignParent bool
	}{
		{name: "defaults"},
		{name: "all optional fields", parentAddress: extra, fields: map[string]any{
			"worker_stake": "23", "verifier_stake_per_verifier": "45", "token": extra,
			"service_tier": 1, "milestones": milestones("100", "1800000000"), "backup_worker": extra,
			"backup_deadline_extension": "3600", "zk_verifier": extra, "circuit_id": "0x" + strings.Repeat("ab", 32),
			"verifier_panel": []string{verifier, "unused"}, "parent_escrow_id": 1,
		}},
		{name: "explicit zero token and stakes", fields: map[string]any{"token": zero, "worker_stake": "0", "verifier_stake_per_verifier": "0"}},
		{name: "negative amount without floor", noFloor: true, fields: map[string]any{"amount": "-1"}, wantError: "amount must not be negative"},
		{name: "negative amount", fields: map[string]any{"amount": "-1"}, wantError: "below complexity floor"},
		{name: "invalid amount", fields: map[string]any{"amount": "x"}, wantError: "invalid amount"},
		{name: "below floor", fields: map[string]any{"amount": "9"}, wantError: "below complexity floor"},
		{name: "invalid deadline", fields: map[string]any{"submission_deadline": "-1"}, wantError: "invalid submission_deadline"},
		{name: "invalid review", fields: map[string]any{"review_period_seconds": "1e3"}, wantError: "invalid review_period_seconds"},
		{name: "invalid dispute", fields: map[string]any{"dispute_period_seconds": ""}, wantError: "invalid dispute_period_seconds"},
		{name: "invalid arbitrator timeout", fields: map[string]any{"arbitrator_timeout_seconds": " 5"}, wantError: "invalid arbitrator_timeout_seconds"},
		{name: "deadline overflow", fields: map[string]any{"submission_deadline": overflow}, wantError: "submission_deadline out of range"},
		{name: "review overflow", fields: map[string]any{"review_period_seconds": overflow}, wantError: "review_period_seconds out of range"},
		{name: "dispute overflow", fields: map[string]any{"dispute_period_seconds": overflow}, wantError: "dispute_period_seconds out of range"},
		{name: "arbitrator timeout overflow", fields: map[string]any{"arbitrator_timeout_seconds": overflow}, wantError: "arbitrator_timeout_seconds out of range"},
		{name: "invalid buyer", fields: map[string]any{"buyer": "x"}, wantError: "buyer address"},
		{name: "zero buyer", fields: map[string]any{"buyer": zero}, wantError: "buyer address"},
		{name: "invalid worker", fields: map[string]any{"worker": "x"}, wantError: "worker address"},
		{name: "zero worker", fields: map[string]any{"worker": zero[2:]}, wantError: "zero worker address"},
		{name: "invalid arbitrator", fields: map[string]any{"arbitrator": "x"}, wantError: "arbitrator address"},
		{name: "zero arbitrator", fields: map[string]any{"arbitrator": zero}, wantError: "arbitrator address"},
		{name: "duplicate buyer and worker", fields: map[string]any{"worker": buyer}, wantError: "buyer and worker must be distinct"},
		{name: "duplicate buyer and arbitrator", fields: map[string]any{"arbitrator": buyer}, wantError: "buyer and arbitrator must be distinct"},
		{name: "duplicate worker and arbitrator", fields: map[string]any{"arbitrator": worker}, wantError: "worker and arbitrator must be distinct"},
		{name: "empty panel", fields: map[string]any{"verifier_panel": []string{}}, wantError: "verifier_panel must include between 1 and 7"},
		{name: "oversized panel", fields: map[string]any{"verifier_panel": strings.Fields(strings.Repeat(verifier+" ", 8))}, wantError: "verifier_panel must include between 1 and 7"},
		{name: "invalid active verifier", fields: map[string]any{"verifier_panel": []string{"x"}}, wantError: "verifier_panel[0]"},
		{name: "zero active verifier", fields: map[string]any{"verifier_panel": []string{zero}}, wantError: "verifier_panel[0]"},
		{name: "duplicate panel entry", fields: map[string]any{"quorum_verifier_count": 2, "verifier_panel": []string{verifier, verifier}}, wantError: "duplicate"},
		{name: "verifier overlaps role", fields: map[string]any{"verifier_panel": []string{buyer}}, wantError: "overlap"},
		{name: "short panel", fields: map[string]any{"quorum_verifier_count": 2}, wantError: "verifier_panel length"},
		{name: "quorum count range", fields: map[string]any{"quorum_verifier_count": 8}, wantError: "quorum_verifier_count"},
		{name: "negative quorum count", fields: map[string]any{"quorum_verifier_count": -1}, wantError: "quorum_verifier_count"},
		{name: "quorum threshold range", fields: map[string]any{"quorum_threshold": 2}, wantError: "quorum_threshold"},
		{name: "quorum threshold overflow", fields: map[string]any{"quorum_threshold": 256}, wantError: "quorum_threshold"},
		{name: "invalid worker stake", fields: map[string]any{"worker_stake": "x"}, wantError: "worker_stake"},
		{name: "negative worker stake", fields: map[string]any{"worker_stake": "-1"}, wantError: "worker_stake"},
		{name: "invalid verifier stake", fields: map[string]any{"verifier_stake_per_verifier": "x"}, wantError: "verifier_stake_per_verifier"},
		{name: "negative verifier stake", fields: map[string]any{"verifier_stake_per_verifier": "-1"}, wantError: "verifier_stake_per_verifier"},
		{name: "invalid token", fields: map[string]any{"token": "x"}, wantError: "invalid token address"},
		{name: "invalid milestone amount", fields: map[string]any{"milestones": milestones("x", "1")}, wantError: "milestone amount"},
		{name: "negative milestone amount", fields: map[string]any{"milestones": milestones("-1", "1")}, wantError: "milestone amount"},
		{name: "invalid milestone deadline", fields: map[string]any{"milestones": milestones("100", "x")}, wantError: "milestone submission_deadline"},
		{name: "milestone deadline overflow", fields: map[string]any{"milestones": milestones("100", overflow)}, wantError: "milestones[0].submission_deadline out of range"},
		{name: "invalid backup worker", fields: map[string]any{"backup_worker": "x"}, wantError: "backup_worker address"},
		{name: "unprefixed zero backup worker", fields: map[string]any{"backup_worker": zero[2:]}},
		{name: "zero backup worker", fields: map[string]any{"backup_worker": zero}, wantError: "backup_worker address"},
		{name: "zero extension without backup worker", fields: map[string]any{"backup_deadline_extension": "0"}, wantError: "backup_deadline_extension set without backup_worker"},
		{name: "positive extension without backup worker", fields: map[string]any{"backup_deadline_extension": "1"}, wantError: "backup_deadline_extension set"},
		{name: "invalid backup extension", fields: map[string]any{"backup_worker": extra, "backup_deadline_extension": "x"}, wantError: "invalid backup_deadline_extension"},
		{name: "backup extension overflow", fields: map[string]any{"backup_worker": extra, "backup_deadline_extension": overflow}, wantError: "backup_deadline_extension out of range"},
		{name: "invalid ZK verifier", fields: map[string]any{"zk_verifier": "x"}, wantError: "invalid zk_verifier address"},
		{name: "invalid circuit ID", fields: map[string]any{"zk_verifier": extra, "circuit_id": "0x12"}, wantError: "invalid circuit_id"},
		{name: "unpaired ZK verifier", fields: map[string]any{"zk_verifier": extra}, wantError: "zk_verifier and circuit_id"},
		{name: "unpaired circuit", fields: map[string]any{"circuit_id": "0x" + strings.Repeat("ab", 32)}, wantError: "zk_verifier and circuit_id"},
		{name: "invalid service tier", fields: map[string]any{"service_tier": 2}, wantError: "invalid service_tier"},
		{name: "unknown parent", fields: map[string]any{"parent_escrow_id": 1}, wantError: "invalid parent_escrow_id"},
		{name: "zero parent address", parentAddress: zero, fields: map[string]any{"parent_escrow_id": 1}, wantError: "invalid on-chain address"},
		{name: "malformed parent address", parentAddress: "x", fields: map[string]any{"parent_escrow_id": 1}, wantError: "invalid on-chain address"},
		{name: "parent chain mismatch", foreignParent: true, parentAddress: extra, fields: map[string]any{"parent_escrow_id": 1}, wantError: "different chain/factory"},
		{name: "parent buyer mismatch", parentAddress: extra, fields: map[string]any{"parent_escrow_id": 1, "buyer": "0x6000000000000000000000000000000000000006"}, wantError: "must be active worker"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{
				"title": "Create parity", "description": "same task", "buyer": buyer, "worker": worker,
				"arbitrator": arbitrator, "verifier_panel": []string{verifier}, "quorum_threshold": 1,
				"quorum_verifier_count": 1, "amount": "100", "submission_deadline": "1800000000",
				"review_period_seconds": "600", "dispute_period_seconds": "700", "arbitrator_timeout_seconds": "800",
			}
			for key, value := range tc.fields {
				args[key] = value
			}
			httpEnv := newCreateTransportTestEnv(t, tc.parentAddress, buyer)
			mcpEnv := newCreateTransportTestEnv(t, tc.parentAddress, buyer)
			ucpEnv := newCreateTransportTestEnv(t, tc.parentAddress, buyer)
			ucpEnv.cfg.UCPEnabled = true
			for _, env := range []*createTransportTestEnv{httpEnv, mcpEnv, ucpEnv} {
				if tc.noFloor {
					env.cfg.ComplexityFloor = ""
				}
				if tc.foreignParent {
					env.cfg.ChainID++
				}
			}
			httpError := httpEnv.callHTTP(t, "/api/v1/escrows", args)
			ucpError := ucpEnv.callHTTP(t, "/api/v1/ucp/checkouts", map[string]any{"create_escrow": args})
			if tc.wantError == "" {
				if ucpError != "" {
					t.Fatalf("expected successful inline UCP create, got %q", ucpError)
				}
				if params := ucpEnv.mock.LastCreateEscrowParams; params == nil || !reflect.DeepEqual(httpEnv.mock.LastCreateEscrowParams, params) {
					t.Errorf("UCP chain create params differ: HTTP %+v, UCP %+v", httpEnv.mock.LastCreateEscrowParams, params)
				}
			} else {
				// UCP wraps parser errors, so assert the validation class rather than exact text.
				if !strings.Contains(ucpError, tc.wantError) {
					t.Errorf("want UCP validation class %q, got %q", tc.wantError, ucpError)
				}
				if ucpEnv.mock.LastCreateEscrowParams != nil {
					t.Error("invalid UCP request reached chain")
				}
			}
			for _, key := range []string{"quorum_threshold", "quorum_verifier_count", "service_tier", "parent_escrow_id"} {
				if value, ok := args[key].(int); ok {
					args[key] = strconv.Itoa(value)
				}
			}
			mcpError := mcpEnv.callMCP(t, args)
			if tc.wantError != "" {
				if httpError != mcpError {
					t.Errorf("validation messages differ: HTTP %q, MCP %q", httpError, mcpError)
				}
				if !strings.Contains(httpError, tc.wantError) || !strings.Contains(mcpError, tc.wantError) {
					t.Errorf("want validation class %q, got HTTP %q, MCP %q", tc.wantError, httpError, mcpError)
				}
				if httpEnv.mock.LastCreateEscrowParams != nil || mcpEnv.mock.LastCreateEscrowParams != nil {
					t.Error("invalid request reached chain")
				}
				return
			}
			if httpError != "" || mcpError != "" {
				t.Fatalf("expected success, got HTTP %q, MCP %q", httpError, mcpError)
			}
			httpParams, mcpParams := httpEnv.mock.LastCreateEscrowParams, mcpEnv.mock.LastCreateEscrowParams
			if httpParams == nil || !reflect.DeepEqual(httpParams, mcpParams) {
				t.Errorf("chain create params differ: HTTP %+v, MCP %+v", httpParams, mcpParams)
			}
			httpRec, err := httpEnv.db.GetEscrow(t.Context(), httpEnv.escrowID)
			if err != nil {
				t.Fatal(err)
			}
			mcpRec, err := mcpEnv.db.GetEscrow(t.Context(), mcpEnv.escrowID)
			if err != nil {
				t.Fatal(err)
			}
			// Compare the derived values independently of transaction identity and timestamps.
			for _, rec := range []*storage.Escrow{httpRec, mcpRec} {
				rec.CreateTxHash = ""
				rec.CreatedAt, rec.UpdatedAt = time.Time{}, time.Time{}
			}
			if !reflect.DeepEqual(httpRec, mcpRec) {
				t.Errorf("persisted escrow differs: HTTP %+v, MCP %+v", httpRec, mcpRec)
			}
		})
	}
}

type createTransportTestEnv struct {
	*Server
	mock     *chain.MockClient
	escrowID int64
}

func newCreateTransportTestEnv(t *testing.T, parentAddress, parentWorker string) *createTransportTestEnv {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{
		ChainID: 84532, FactoryAddress: "0x0000000000000000000000000000000000000001",
		RequestTimeout: 10 * time.Second, TxTimeout: 5 * time.Second, ComplexityFloor: "10",
	}
	mock := chain.NewMockClient()
	mock.Receipt = chain.MakeEscrowCreatedReceipt(7, common.HexToAddress("0x123456"), common.HexToAddress(parentWorker))
	if parentAddress != "" {
		task, err := db.CreateTask(t.Context(), "Parent", "", "parent-spec")
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.CreateEscrow(t.Context(), &storage.Escrow{
			TaskID: task.ID, ChainID: cfg.ChainID, FactoryAddress: cfg.FactoryAddress,
			EscrowAddress: parentAddress, Worker: parentWorker, Amount: "100", Status: "funded",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return &createTransportTestEnv{mock: mock, Server: &Server{db: db, chain: mock, cfg: cfg, idx: indexer.New(db, mock, cfg.FactoryAddress)}}
}

func (e *createTransportTestEnv) callHTTP(t *testing.T, path string, args map[string]any) string {
	t.Helper()
	body, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.NewRouter(e.db, e.chain, e.idx, e.cfg, nil).ServeHTTP(rec, req)
	var result struct {
		Error    string `json:"error"`
		EscrowID int64  `json:"escrow_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	switch rec.Code {
	case http.StatusCreated:
		e.escrowID = result.EscrowID
		return ""
	case http.StatusBadRequest:
		return result.Error
	default:
		t.Fatalf("unexpected HTTP status %d: %s", rec.Code, rec.Body.String())
		return ""
	}
}

func (e *createTransportTestEnv) callMCP(t *testing.T, args map[string]any) string {
	t.Helper()
	ctx := t.Context()
	server := mcp.NewServer(&mcp.Implementation{Name: "escrow-test"}, nil)
	e.registerTools(server)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "escrow-test-client"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "create_escrow", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	message, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected MCP content %+v", result.Content)
	}
	var created struct {
		EscrowID int64 `json:"escrow_id"`
	}
	if json.Unmarshal([]byte(message.Text), &created) == nil && created.EscrowID != 0 {
		e.escrowID = created.EscrowID
		return ""
	}
	return message.Text
}
