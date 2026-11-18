package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/ethereum/go-ethereum/common"
)

const (
	signatureHeader    = "X-Hook0-Signature"
	maxWebhookBodySize = 1 << 20 // 1 MiB
	maxTimestampAge    = 5 * time.Minute
)

// cdpWebhookPayload represents the top-level CDP webhook JSON structure.
type cdpWebhookPayload struct {
	ID        string              `json:"id"`
	Type      string              `json:"type"`
	CreatedAt string              `json:"createdAt"`
	Data      cdpWebhookEventData `json:"data"`
}

// cdpWebhookEventData holds the fields of a CDP event the handler checks or
// logs. The decoded event parameters are deliberately not read: the indexer
// fetches the logs from the chain itself.
type cdpWebhookEventData struct {
	BlockNumber     json.Number `json:"blockNumber"`
	TransactionHash string      `json:"transactionHash"`
	ContractAddress string      `json:"contractAddress"`
	EventName       string      `json:"eventName"`
}

// WebhookHandler turns signed CDP factory-event notifications into an
// immediate indexer pass. It writes nothing itself: the polling indexer is
// the only writer of chain-derived state, so webhook events get the same
// confirmation depth, per-block transaction, and dedup as polled events.
type WebhookHandler struct {
	idx    *indexer.Indexer
	secret string
}

// NewWebhookHandler creates a webhook handler.
func NewWebhookHandler(idx *indexer.Indexer, secret string) *WebhookHandler {
	return &WebhookHandler{idx: idx, secret: secret}
}

// HandleCDPWebhook is the HTTP handler for POST /webhooks/cdp. A verified
// event from the monitored factory triggers an indexer pass and returns 202;
// the pass applies the event once it is confirmed.
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

	data := payload.Data
	if !common.IsHexAddress(data.ContractAddress) ||
		common.HexToAddress(data.ContractAddress) != wh.idx.FactoryAddress() {
		slog.Warn("webhook: event is not from the monitored factory",
			"event_id", payload.ID,
			"contract_address", data.ContractAddress,
			"factory_address", wh.idx.FactoryAddress().Hex(),
		)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unexpected contract address"})
		return
	}

	slog.Info("webhook: factory event received; triggering indexer pass",
		"event_id", payload.ID,
		"event_name", data.EventName,
		"tx_hash", data.TransactionHash,
		"block_number", data.BlockNumber.String(),
	)
	wh.idx.Trigger()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
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
		p = strings.TrimSpace(p)
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
