package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/api"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/authz"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
)

// cliTestAuthMiddleware simulates auth middleware for CLI integration tests.
// It authenticates callers identified in the JSON request body's "caller",
// "owner", or "caller_address" fields, mirroring what real auth middleware
// would do after verifying a credential (signature, API key, etc.).
func cliTestAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Method == http.MethodPost {
			body, err := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			if err == nil {
				var payload map[string]any
				if json.Unmarshal(body, &payload) == nil {
					for _, field := range []string{"caller", "owner", "caller_address"} {
						if addr, ok := payload[field].(string); ok && strings.TrimSpace(addr) != "" {
							ctx := authz.WithCaller(r.Context(), authz.Principal{
								Address:       strings.ToLower(strings.TrimSpace(addr)),
								Authenticated: true,
							})
							r = r.WithContext(ctx)
							break
						}
					}
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

type cliTestEnv struct {
	db     *storage.DB
	server *httptest.Server
}

func setupCLITestEnv(t *testing.T) *cliTestEnv {
	t.Helper()

	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mock := chain.NewMockClient()
	cfg := &config.Config{
		ChainID:          84532,
		FactoryAddress:   "0xFactoryAddr",
		RequestTimeout:   10 * time.Second,
		TxTimeout:        90 * time.Second,
		EmergencyEnabled: true,
	}
	idx := indexer.New(db, mock, cfg.FactoryAddress)
	router := cliTestAuthMiddleware(api.NewRouter(db, mock, idx, cfg, nil))
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return &cliTestEnv{db: db, server: server}
}

func runCLI(t *testing.T, serverURL string, args ...string) (string, string, error) {
	t.Helper()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd := NewRootCmd(stdout, stderr)
	fullArgs := []string{"--server", serverURL, "--output", "json"}
	fullArgs = append(fullArgs, args...)
	cmd.SetArgs(fullArgs)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func TestCLIHealth(t *testing.T) {
	env := setupCLITestEnv(t)
	stdout, stderr, err := runCLI(t, env.server.URL, "health")
	if err != nil {
		t.Fatalf("execute health: %v stderr=%s", err, stderr)
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal health output: %v\n%s", err, stdout)
	}
	if got, ok := resp["status"].(string); !ok || got == "" {
		t.Fatalf("expected status in output, got: %v", resp)
	}
}

func TestCLIEscrowListAndGet(t *testing.T) {
	env := setupCLITestEnv(t)
	ctx := context.Background()

	task, err := env.db.CreateTask(ctx, "Task", "desc", "0xabc")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	escrow, err := env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  84532,
		FactoryAddress:           "0xF",
		EscrowAddress:            "0xE1",
		Buyer:                    "0xB",
		Worker:                   "0xW",
		Verifier:                 "0xV",
		Arbitrator:               "0xA",
		Amount:                   "100",
		Status:                   "created",
		SubmissionDeadline:       1700000000,
		ReviewPeriodSeconds:      60,
		DisputePeriodSeconds:     60,
		ArbitratorTimeoutSeconds: 60,
	})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}

	listOut, listErr, err := runCLI(t, env.server.URL, "escrow", "list")
	if err != nil {
		t.Fatalf("execute escrow list: %v stderr=%s", err, listErr)
	}
	var escrows []map[string]any
	if err := json.Unmarshal([]byte(listOut), &escrows); err != nil {
		t.Fatalf("unmarshal escrow list: %v\n%s", err, listOut)
	}
	if len(escrows) != 1 {
		t.Fatalf("expected 1 escrow, got %d", len(escrows))
	}

	idStr := strconv.FormatInt(escrow.ID, 10)
	getOut, getErr, err := runCLI(t, env.server.URL, "escrow", "get", idStr)
	if err != nil {
		t.Fatalf("execute escrow get: %v stderr=%s", err, getErr)
	}
	var getResp map[string]any
	if err := json.Unmarshal([]byte(getOut), &getResp); err != nil {
		t.Fatalf("unmarshal escrow get: %v\n%s", err, getOut)
	}
	if _, ok := getResp["escrow"]; !ok {
		t.Fatalf("expected escrow object in response: %v", getResp)
	}
}

func TestCLIClientErrorExitCode(t *testing.T) {
	env := setupCLITestEnv(t)
	_, _, err := runCLI(t, env.server.URL, "escrow", "get", "abc")
	if err == nil {
		t.Fatal("expected error")
	}
	if code := ExitCode(err); code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
}

func TestExitCodeLocalInputError(t *testing.T) {
	err := errors.New("request body required (--data or --data-file)")
	if code := ExitCode(err); code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
}

func TestPayloadFromFlagsRejectsTrailingJSON(t *testing.T) {
	_, err := payloadFromFlags(payloadFlags{
		inline: `{"role":"buyer"}{"role":"worker"}`,
	}, true)
	if err == nil {
		t.Fatal("expected trailing JSON error")
	}
}

func TestExitCodeTransportError(t *testing.T) {
	err := &url.Error{
		Op:  "Get",
		URL: "http://localhost:8080/health",
		Err: &net.DNSError{Err: "lookup localhost: no such host"},
	}
	if code := ExitCode(err); code != 2 {
		t.Fatalf("expected exit code 2, got %d", code)
	}
}

func TestCLIDCTMintSmoke(t *testing.T) {
	env := setupCLITestEnv(t)
	ctx := context.Background()
	task, err := env.db.CreateTask(ctx, "Task", "desc", "0xabc")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	escrow, err := env.db.CreateEscrow(ctx, &storage.Escrow{TaskID: task.ID, ChainID: 84532, FactoryAddress: "0xF", EscrowAddress: "0xE2", Buyer: "0xB", Worker: "0xW", Verifier: "0xV", Arbitrator: "0xA", Amount: "100", Status: "funded", SubmissionDeadline: 1700000000, ReviewPeriodSeconds: 60, DisputePeriodSeconds: 60, ArbitratorTimeoutSeconds: 60})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}

	payload := `{"escrow_id":` + strconv.FormatInt(escrow.ID, 10) + `,"subject":"agent-b","operations":["submit_work"],"resources":["escrow:1"],"expires_at":1999999999,"caller":"0xB"}`
	stdout, stderr, err := runCLI(t, env.server.URL, "dct", "mint", "--data", payload)
	if err != nil {
		t.Fatalf("execute dct mint: %v stderr=%s", err, stderr)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal dct mint output: %v\n%s", err, stdout)
	}
	if _, ok := resp["token"]; !ok {
		t.Fatalf("expected token in response: %v", resp)
	}
}

func TestCLIEmergencyFrozenAddresses(t *testing.T) {
	env := setupCLITestEnv(t)
	stdout, stderr, err := runCLI(t, env.server.URL, "emergency", "frozen-addresses")
	if err != nil {
		t.Fatalf("execute emergency frozen-addresses: %v stderr=%s", err, stderr)
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal emergency output: %v\n%s", err, stdout)
	}
	if _, ok := resp["frozen_addresses"]; !ok {
		t.Fatalf("expected frozen_addresses in response: %v", resp)
	}
}

// Checkpoint CLI tests (paper §6.1)

func createCLICheckpointEscrow(t *testing.T, env *cliTestEnv) *storage.Escrow {
	t.Helper()
	ctx := context.Background()
	task, err := env.db.CreateTask(ctx, "CP Task", "desc", "0xspec")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	escrow, err := env.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  84532,
		FactoryAddress:           "0xF",
		EscrowAddress:            "0xECP",
		Buyer:                    "0xB",
		Worker:                   "0xW",
		Verifier:                 "0xV",
		Arbitrator:               "0xA",
		Amount:                   "1000",
		Status:                   "funded",
		SubmissionDeadline:       1700000000,
		ReviewPeriodSeconds:      60,
		DisputePeriodSeconds:     60,
		ArbitratorTimeoutSeconds: 60,
		MilestoneCount:           2,
	})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}
	return escrow
}

func TestCLICheckpointCommit(t *testing.T) {
	env := setupCLITestEnv(t)
	escrow := createCLICheckpointEscrow(t, env)
	id := strconv.FormatInt(escrow.ID, 10)

	payload := `{"state_snapshot_uri":"ipfs://snap1","committed_by":"0xW","milestone_index":0}`
	stdout, stderr, err := runCLI(t, env.server.URL, "escrow", "checkpoint-commit", id, "--data", payload)
	if err != nil {
		t.Fatalf("execute checkpoint-commit: %v stderr=%s", err, stderr)
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal checkpoint-commit output: %v\n%s", err, stdout)
	}
	if resp["state_snapshot_uri"] != "ipfs://snap1" {
		t.Fatalf("expected state_snapshot_uri 'ipfs://snap1', got %v", resp["state_snapshot_uri"])
	}
}

func TestCLICheckpointList(t *testing.T) {
	env := setupCLITestEnv(t)
	escrow := createCLICheckpointEscrow(t, env)
	id := strconv.FormatInt(escrow.ID, 10)

	_, setupStderr, setupErr := runCLI(t, env.server.URL, "escrow", "checkpoint-commit", id,
		"--data", `{"state_snapshot_uri":"ipfs://snap1","committed_by":"0xW"}`)
	if setupErr != nil {
		t.Fatalf("setup checkpoint-commit: %v stderr=%s", setupErr, setupStderr)
	}

	stdout, stderr, err := runCLI(t, env.server.URL, "escrow", "checkpoints", id)
	if err != nil {
		t.Fatalf("execute checkpoints: %v stderr=%s", err, stderr)
	}

	var checkpoints []map[string]any
	if err := json.Unmarshal([]byte(stdout), &checkpoints); err != nil {
		t.Fatalf("unmarshal checkpoints output: %v\n%s", err, stdout)
	}
	if len(checkpoints) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(checkpoints))
	}
}

func TestCLICheckpointLatest(t *testing.T) {
	env := setupCLITestEnv(t)
	escrow := createCLICheckpointEscrow(t, env)
	id := strconv.FormatInt(escrow.ID, 10)

	_, setupStderr1, setupErr1 := runCLI(t, env.server.URL, "escrow", "checkpoint-commit", id,
		"--data", `{"state_snapshot_uri":"ipfs://old","committed_by":"0xW"}`)
	if setupErr1 != nil {
		t.Fatalf("setup checkpoint-commit old: %v stderr=%s", setupErr1, setupStderr1)
	}
	_, setupStderr2, setupErr2 := runCLI(t, env.server.URL, "escrow", "checkpoint-commit", id,
		"--data", `{"state_snapshot_uri":"ipfs://latest","committed_by":"0xW"}`)
	if setupErr2 != nil {
		t.Fatalf("setup checkpoint-commit latest: %v stderr=%s", setupErr2, setupStderr2)
	}

	stdout, stderr, err := runCLI(t, env.server.URL, "escrow", "checkpoint-latest", id)
	if err != nil {
		t.Fatalf("execute checkpoint-latest: %v stderr=%s", err, stderr)
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal checkpoint-latest output: %v\n%s", err, stdout)
	}
	if resp["state_snapshot_uri"] != "ipfs://latest" {
		t.Fatalf("expected latest URI, got %v", resp["state_snapshot_uri"])
	}
}
