package mcpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/api"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/cli"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func connectMCP(t *testing.T, s *Server) *mcp.ClientSession {
	t.Helper()
	ctx := t.Context()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "escrow-test"}, nil)
	s.registerTools(server)
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
	return session
}

func TestRecoveryTransports(t *testing.T) {
	cases := []struct {
		command, tool, method, stateError string
		escrowStatus, milestoneStatus     uint8
		milestone, reason                 bool
	}{
		{"cancel", "cancel_before_funding", "cancelBeforeFunding", "escrow must be created", 0, 0, false, false},
		{"claim-timeout", "claim_timeout_refund", "claimTimeoutRefund", "escrow must be funded", 1, 0, false, false},
		{"claim-arbitrator-timeout", "claim_arbitrator_timeout", "claimArbitratorTimeout", "escrow must be disputed", 4, 0, false, false},
		{"expire-no-quorum", "expire_no_quorum", "expireNoQuorum", "escrow must be submitted", 2, 0, false, true},
		{"claim-milestone-timeout", "claim_milestone_timeout_refund", "claimMilestoneTimeoutRefund", "milestone 1 must be pending", 1, 0, true, false},
		{"claim-milestone-arbitrator-timeout", "claim_milestone_arbitrator_timeout", "claimMilestoneArbitratorTimeout", "milestone 1 must be disputed", 1, 3, true, false},
		{"expire-milestone-no-quorum", "expire_milestone_no_quorum", "expireMilestoneNoQuorum", "milestone 1 must be submitted", 1, 1, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			for _, transport := range []string{"http", "mcp", "cli"} {
				t.Run(transport, func(t *testing.T) {
					for _, scenario := range []string{"confirmed", "wrong-state", "reverted"} {
						// All actions share the same receipt handling.
						if scenario == "reverted" && tc.command != "cancel" {
							continue
						}
						t.Run(scenario, func(t *testing.T) {
							ctx := t.Context()
							db, err := storage.Open(":memory:")
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() { _ = db.Close() })
							task, err := db.CreateTask(ctx, "Recover funds", "", "recovery-spec")
							if err != nil {
								t.Fatal(err)
							}
							address := common.HexToAddress("0x123456")
							count := 1
							if tc.milestone {
								count = 2
							}
							cfg := &config.Config{ChainID: 84532, RequestTimeout: time.Second, TxTimeout: 5 * time.Second}
							rec, err := db.CreateEscrow(ctx, &storage.Escrow{
								TaskID: task.ID, ChainID: cfg.ChainID, EscrowAddress: address.Hex(),
								Amount: "100", Status: "created", MilestoneCount: count,
							})
							if err != nil {
								t.Fatal(err)
							}

							mock := chain.NewMockClient()
							mock.BlockNum = 123
							mock.MilestoneCountVal = uint8(count)
							mock.StatusVal = tc.escrowStatus
							mock.MilestoneStatusVal = tc.milestoneStatus
							mock.Receipt = &types.Receipt{Status: types.ReceiptStatusSuccessful}
							if scenario == "wrong-state" {
								if tc.milestone {
									mock.MilestoneStatusVal = 5 // Cancelled
								} else {
									mock.StatusVal = 8 // Cancelled
								}
							}
							if scenario == "reverted" {
								mock.Receipt.Status = types.ReceiptStatusFailed
							}
							wantStatus := http.StatusOK
							if scenario == "wrong-state" {
								wantStatus = http.StatusBadRequest
							}
							if scenario == "reverted" {
								wantStatus = http.StatusInternalServerError
							}

							wantTx := chain.MockTxRecord{Method: tc.method, To: address}
							input := make(map[string]any)
							if tc.milestone {
								input["milestone_index"] = 1
								wantTx.MilestoneIndex = 1
							}
							if tc.reason {
								reason := strings.Repeat("é", 1024)
								input["reason_uri"] = reason
								wantTx.ReasonURI = reason
							}
							idx := indexer.New(db, mock, cfg.FactoryAddress)
							output := callRecoveryTransport(t, &Server{db: db, chain: mock, cfg: cfg, idx: idx}, transport, tc.command, tc.tool, rec, input, wantStatus)
							cursor, err := db.GetCursor(ctx, cfg.ChainID, "indexer")
							if err != nil {
								t.Fatal(err)
							}
							var wantCursor int64
							if scenario == "confirmed" {
								wantCursor = 123
							}
							if cursor != wantCursor {
								t.Fatalf("indexer cursor = %d, want %d", cursor, wantCursor)
							}

							switch scenario {
							case "wrong-state":
								if !strings.Contains(output, tc.stateError) || len(mock.SentTxs) != 0 {
									t.Fatalf("wrong-state response %q; sent transactions %+v", output, mock.SentTxs)
								}
								return
							case "confirmed":
								var result struct {
									TxHash string `json:"tx_hash"`
								}
								if err := json.Unmarshal([]byte(output), &result); err != nil || len(result.TxHash) != 66 || !strings.HasPrefix(result.TxHash, "0x") {
									t.Fatalf("missing transaction hash in %q", output)
								}
							case "reverted":
								if transport == "mcp" && !strings.Contains(output, "reverted") {
									t.Fatalf("missing revert error in %q", output)
								}
							}
							if len(mock.SentTxs) != 1 || mock.SentTxs[0] != wantTx {
								t.Fatalf("sent transactions %+v, want %+v", mock.SentTxs, wantTx)
							}
						})
					}
				})
			}
		})
	}
}

func TestRecoveryTransportValidation(t *testing.T) {
	cases := []struct {
		name, command, tool, wantError string
		dbCount                        int
		chainCount, state              uint8
		input                          map[string]any
	}{
		{"timeout", "claim-timeout", "claim_timeout_refund", "milestone_index required for multi-milestone escrow", 2, 2, 1, nil},
		{"timeout-stale-db", "claim-timeout", "claim_timeout_refund", "milestone_index required for multi-milestone escrow", 1, 2, 1, nil},
		{"arbitrator-timeout", "claim-arbitrator-timeout", "claim_arbitrator_timeout", "milestone_index required for multi-milestone escrow", 1, 2, 4, nil},
		{"no-quorum", "expire-no-quorum", "expire_no_quorum", "milestone_index required for multi-milestone escrow", 1, 2, 2, map[string]any{"reason_uri": "ipfs://reason"}},
		{"single-shot", "claim-milestone-timeout", "claim_milestone_timeout_refund", "milestone_index is not valid for single-milestone escrows", 2, 1, 1, map[string]any{"milestone_index": 0}},
		{"negative-index", "claim-milestone-timeout", "claim_milestone_timeout_refund", "milestone_index -1 out of range [0, 2)", 1, 2, 1, map[string]any{"milestone_index": -1}},
		{"index-at-count", "claim-milestone-timeout", "claim_milestone_timeout_refund", "milestone_index 2 out of range [0, 2)", 3, 2, 1, map[string]any{"milestone_index": 2}},
		{"reason-too-long", "expire-no-quorum", "expire_no_quorum", "reason_uri must be at most 2048 bytes", 1, 1, 2, map[string]any{"reason_uri": strings.Repeat("é", 1024) + "x"}},
		{"milestone-reason-too-long", "expire-milestone-no-quorum", "expire_milestone_no_quorum", "reason_uri must be at most 2048 bytes", 2, 2, 1, map[string]any{"milestone_index": 0, "reason_uri": strings.Repeat("é", 1024) + "x"}},
	}
	for _, tc := range cases {
		for _, transport := range []string{"http", "mcp", "cli"} {
			t.Run(tc.name+"/"+transport, func(t *testing.T) {
				ctx := t.Context()
				db, err := storage.Open(":memory:")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				task, err := db.CreateTask(ctx, "Recover funds", "", "recovery-spec")
				if err != nil {
					t.Fatal(err)
				}
				cfg := &config.Config{ChainID: 84532, RequestTimeout: time.Second, TxTimeout: 5 * time.Second}
				rec, err := db.CreateEscrow(ctx, &storage.Escrow{
					TaskID: task.ID, ChainID: cfg.ChainID, EscrowAddress: common.HexToAddress("0x123456").Hex(),
					Amount: "100", Status: "created", MilestoneCount: tc.dbCount,
				})
				if err != nil {
					t.Fatal(err)
				}
				mock := chain.NewMockClient()
				mock.StatusVal = tc.state
				mock.MilestoneCountVal = tc.chainCount
				input := make(map[string]any, len(tc.input))
				for key, value := range tc.input {
					input[key] = value
				}
				output := callRecoveryTransport(t, &Server{db: db, chain: mock, cfg: cfg}, transport, tc.command, tc.tool, rec, input, http.StatusBadRequest)
				if !strings.Contains(output, tc.wantError) || len(mock.SentTxs) != 0 {
					t.Fatalf("response %q, want %q; sent transactions %+v", output, tc.wantError, mock.SentTxs)
				}
			})
		}
	}
}

func callRecoveryTransport(t *testing.T, s *Server, transport, command, tool string, escrow *storage.Escrow, input map[string]any, wantStatus int) string {
	t.Helper()
	id := strconv.FormatInt(escrow.ID, 10)
	if transport == "mcp" {
		// MCP also accepts an on-chain address. Existing FlexibleString schemas accept string inputs.
		input["escrow_id"] = escrow.EscrowAddress
		if index, ok := input["milestone_index"].(int); ok {
			input["milestone_index"] = strconv.Itoa(index)
		}
		result, err := connectMCP(t, s).CallTool(t.Context(), &mcp.CallToolParams{Name: tool, Arguments: input})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError != (wantStatus != http.StatusOK) || len(result.Content) != 1 {
			t.Fatalf("unexpected tool result %+v", result)
		}
		content, ok := result.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatalf("unexpected tool content %+v", result.Content)
		}
		return content.Text
	}

	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	router := api.NewRouter(s.db, s.chain, s.idx, s.cfg, s.bus)
	if transport == "http" {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/escrows/"+id+"/"+command, bytes.NewReader(body))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != wantStatus {
			t.Fatalf("HTTP status %d, want %d; response %s", response.Code, wantStatus, response.Body)
		}
		return response.Body.String()
	}

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	var stdout, stderr bytes.Buffer
	root := cli.NewRootCmd(&stdout, &stderr)
	args := []string{"--server", server.URL, "--output", "json", "escrow", command, id}
	if len(input) != 0 {
		args = append(args, "--data", string(body))
	}
	root.SetArgs(args)
	err = root.ExecuteContext(t.Context())
	if wantStatus != http.StatusOK {
		var apiErr *cli.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != wantStatus {
			t.Fatalf("CLI error %v, want HTTP status %d", err, wantStatus)
		}
		return apiErr.Message
	}
	if err != nil {
		t.Fatalf("CLI failed: %v; stderr %q", err, stderr.String())
	}
	return stdout.String()
}
