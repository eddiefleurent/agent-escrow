package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/ethereum/go-ethereum/common"
)

const (
	signatureHeader    = "X-Hook0-Signature"
	maxWebhookBodySize = 1 << 20 // 1 MiB
	maxTimestampAge    = 5 * time.Minute
)

// cdpWebhookPayload represents the top-level CDP webhook JSON structure.
// CDP delivers decoded event parameters as flat fields in the data object.
type cdpWebhookPayload struct {
	ID        string              `json:"id"`
	Type      string              `json:"type"`
	CreatedAt string              `json:"createdAt"`
	Data      cdpWebhookEventData `json:"data"`
}

// cdpWebhookEventData holds the decoded event data from CDP.
// Known parameter fields are extracted explicitly; unknown events are ignored.
type cdpWebhookEventData struct {
	SubscriptionID  string      `json:"subscriptionId"`
	NetworkID       string      `json:"networkId"`
	BlockNumber     json.Number `json:"blockNumber"`
	BlockHash       string      `json:"blockHash"`
	TransactionHash string      `json:"transactionHash"`
	LogIndex        json.Number `json:"logIndex"`
	ContractAddress string      `json:"contractAddress"`
	EventName       string      `json:"eventName"`

	// Decoded parameters — CDP flattens indexed + non-indexed params into the
	// data object. Field names match the Solidity event parameter names.
	// EscrowCreated params:
	EscrowID     string `json:"escrowId,omitempty"`
	Escrow       string `json:"escrow,omitempty"`
	Buyer        string `json:"buyer,omitempty"`
	Worker       string `json:"worker,omitempty"`
	Verifier     string `json:"verifier,omitempty"`
	Arbitrator   string `json:"arbitrator,omitempty"`
	TaskSpecHash string `json:"taskSpecHash,omitempty"`
	Token        string `json:"token,omitempty"`

	// OutcomeRecorded params:
	Participant string `json:"participant,omitempty"`
	Role        string `json:"role,omitempty"`
	Outcome     string `json:"outcome,omitempty"`
}

// WebhookHandler handles incoming CDP webhook events for factory contracts.
// Escrow-level events are still handled by the polling indexer since each
// TaskEscrow is a dynamically-deployed contract that can't be pre-subscribed.
type WebhookHandler struct {
	idx    *indexer.Indexer
	secret string
}

func NewWebhookHandler(idx *indexer.Indexer, secret string) *WebhookHandler {
	return &WebhookHandler{idx: idx, secret: secret}
}

// HandleCDPWebhook is the HTTP handler for POST /webhooks/cdp.
func (wh *WebhookHandler) HandleCDPWebhook(w http.ResponseWriter, r *http.Request) {
	if wh.secret == "" {
		slog.Error("webhook: secret is not configured — rejecting request")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "webhook secret not configured"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodySize))
	if err != nil {
		slog.Error("webhook: failed to read body", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
		return
	}

	sigHeader := r.Header.Get(signatureHeader)
	if sigHeader == "" {
		slog.Warn("webhook: missing signature header")
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing signature"})
		return
	}

	if !verifyCDPSignature(body, sigHeader, wh.secret, r.Header) {
		slog.Warn("webhook: invalid signature")
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid signature"})
		return
	}

	var payload cdpWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		slog.Error("webhook: invalid JSON payload", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	if payload.Type != "onchain.activity.detected" {
		slog.Info("webhook: ignoring unknown event type", "type", payload.Type)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	if err := wh.processWebhookEvent(payload); err != nil {
		slog.Error("webhook: event processing failed",
			"event_id", payload.ID,
			"event_name", payload.Data.EventName,
			"tx_hash", payload.Data.TransactionHash,
			"error", err,
		)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "processing failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// processWebhookEvent handles a CDP webhook payload by dispatching on eventName.
// Only factory events (EscrowCreated, OutcomeRecorded) are handled here.
func (wh *WebhookHandler) processWebhookEvent(payload cdpWebhookPayload) error {
	data := payload.Data
	db := wh.idx.DB()

	if !common.IsHexAddress(data.ContractAddress) {
		return fmt.Errorf("invalid contract address: %q", data.ContractAddress)
	}
	expectedFactory := wh.idx.FactoryAddress()
	if common.HexToAddress(data.ContractAddress) != expectedFactory {
		return fmt.Errorf("contract address %s does not match factory %s", data.ContractAddress, expectedFactory.Hex())
	}

	logIdx, err := strconv.Atoi(data.LogIndex.String())
	if err != nil {
		return fmt.Errorf("invalid logIndex %q: %w", data.LogIndex.String(), err)
	}

	blockNum, err := strconv.ParseInt(data.BlockNumber.String(), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid blockNumber %q: %w", data.BlockNumber.String(), err)
	}

	// Deduplicate: use the same chain_log mechanism as the polling indexer.
	exists, err := db.ChainLogExists(data.TransactionHash, logIdx)
	if err != nil {
		return fmt.Errorf("check chain log: %w", err)
	}
	if exists {
		return nil
	}

	// Run the handler before recording the chain log so that a handler failure
	// doesn't permanently mark the event as processed (which would drop it on retry).
	var handlerErr error
	switch data.EventName {
	case "EscrowCreated":
		handlerErr = wh.handleEscrowCreated(data)
	case "OutcomeRecorded":
		handlerErr = wh.handleOutcomeRecorded(data)
	default:
		slog.Info("webhook: ignoring unhandled factory event", "event_name", data.EventName)
	}
	if handlerErr != nil {
		return handlerErr
	}

	if err := db.CreateChainLog(data.TransactionHash, logIdx, blockNum, data.EventName, data.ContractAddress, ""); err != nil {
		return fmt.Errorf("create chain log: %w", err)
	}

	return nil
}

// handleEscrowCreated processes an EscrowCreated event from the CDP webhook.
// Mirrors the logic in indexer.handleEscrowCreated but reads from decoded params.
func (wh *WebhookHandler) handleEscrowCreated(data cdpWebhookEventData) error {
	db := wh.idx.DB()

	if !common.IsHexAddress(data.Escrow) {
		return fmt.Errorf("EscrowCreated: invalid escrow address: %q", data.Escrow)
	}
	escrowAddr := common.HexToAddress(data.Escrow).Hex()

	// Check if escrow already exists (e.g. created via API/MCP handler)
	_, err := db.GetEscrowByAddress(escrowAddr)
	if err == nil {
		return nil // Already indexed
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check existing escrow %s: %w", escrowAddr, err)
	}

	for _, pair := range []struct{ name, val string }{
		{"buyer", data.Buyer},
		{"worker", data.Worker},
		{"verifier", data.Verifier},
		{"arbitrator", data.Arbitrator},
	} {
		if !common.IsHexAddress(pair.val) {
			return fmt.Errorf("EscrowCreated: invalid %s address: %q", pair.name, pair.val)
		}
	}

	var escrowID int64
	if data.EscrowID != "" {
		var err error
		escrowID, err = strconv.ParseInt(data.EscrowID, 10, 64)
		if err != nil {
			return fmt.Errorf("EscrowCreated: malformed escrowId %q: %w", data.EscrowID, err)
		}
	}

	taskSpecHash := data.TaskSpecHash
	if taskSpecHash == "" {
		taskSpecHash = "0x0"
	}

	tokenAddr := common.Address{}
	if data.Token != "" {
		if !common.IsHexAddress(data.Token) {
			return fmt.Errorf("EscrowCreated: invalid token address: %q", data.Token)
		}
		tokenAddr = common.HexToAddress(data.Token)
	}

	task, err := db.CreateTask("Indexed task", "", taskSpecHash)
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}

	factoryAddr := wh.idx.FactoryAddress()
	chainID := wh.idx.ChainID()

	_, err = db.CreateEscrow(&storage.Escrow{
		TaskID:             task.ID,
		ChainID:            chainID,
		FactoryAddress:     factoryAddr.Hex(),
		EscrowAddress:      escrowAddr,
		EscrowID:           escrowID,
		Buyer:              common.HexToAddress(data.Buyer).Hex(),
		Worker:             common.HexToAddress(data.Worker).Hex(),
		Verifier:           common.HexToAddress(data.Verifier).Hex(),
		Arbitrator:         common.HexToAddress(data.Arbitrator).Hex(),
		Amount:             "0",
		Token:              tokenAddr.Hex(),
		Status:             "created",
		SubmissionDeadline: 0,
	})
	if err != nil {
		return fmt.Errorf("create escrow: %w", err)
	}

	slog.Info("webhook: escrow created",
		"escrow_address", escrowAddr,
		"escrow_id", escrowID,
		"buyer", data.Buyer,
	)

	return nil
}

// handleOutcomeRecorded processes an OutcomeRecorded event from the CDP webhook.
// Mirrors the logic in indexer.handleOutcomeRecorded.
func (wh *WebhookHandler) handleOutcomeRecorded(data cdpWebhookEventData) error {
	if !common.IsHexAddress(data.Participant) {
		return fmt.Errorf("OutcomeRecorded: invalid participant address: %q", data.Participant)
	}

	participant := strings.ToLower(common.HexToAddress(data.Participant).Hex())

	slog.Info("webhook: outcome recorded",
		"participant", participant,
		"role", data.Role,
		"outcome", data.Outcome,
	)

	return wh.idx.DB().UpsertReputation(participant, data.Role, data.Outcome)
}

// verifyCDPSignature verifies the HMAC-SHA256 signature from a CDP webhook.
//
// The X-Hook0-Signature header format is:
//
//	t=<timestamp>,h=<space-separated header names>,v1=<hex HMAC-SHA256>
//
// The signed payload is: timestamp.headerNames.headerValues.rawBody
func verifyCDPSignature(body []byte, sigHeader, secret string, headers http.Header) bool {
	parts := strings.Split(sigHeader, ",")
	if len(parts) < 3 {
		return false
	}

	var timestamp, headerNames, providedSig string
	for _, p := range parts {
		switch {
		case strings.HasPrefix(p, "t="):
			timestamp = strings.TrimPrefix(p, "t=")
		case strings.HasPrefix(p, "h="):
			headerNames = strings.TrimPrefix(p, "h=")
		case strings.HasPrefix(p, "v1="):
			providedSig = strings.TrimPrefix(p, "v1=")
		}
	}

	if timestamp == "" || providedSig == "" {
		return false
	}

	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	webhookTime := time.Unix(ts, 0)
	drift := time.Since(webhookTime)
	if drift > maxTimestampAge || drift < -maxTimestampAge {
		slog.Warn("webhook: timestamp out of range",
			"webhook_time", webhookTime,
			"drift", drift,
		)
		return false
	}

	nameList := strings.Split(headerNames, " ")
	var headerValues []string
	for _, name := range nameList {
		headerValues = append(headerValues, headers.Get(name))
	}

	signedPayload := fmt.Sprintf("%s.%s.%s.%s",
		timestamp,
		headerNames,
		strings.Join(headerValues, "."),
		string(body),
	)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	providedBytes, err := hex.DecodeString(providedSig)
	if err != nil {
		return false
	}
	expectedBytes, err := hex.DecodeString(expectedSig)
	if err != nil {
		return false
	}

	return hmac.Equal(providedBytes, expectedBytes)
}
