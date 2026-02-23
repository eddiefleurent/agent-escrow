package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/events"
	"github.com/gorilla/websocket"
)

// StreamHandler serves SSE and WebSocket event streams.
type StreamHandler struct {
	bus             *events.EventBus
	allowedOrigins  map[string]bool
	allowAllOrigins bool
}

// NewStreamHandler creates a handler wired to the given event bus.
// allowedOrigins controls which origins are permitted for WebSocket upgrades;
// an empty slice or a slice containing "*" means all origins are allowed.
func NewStreamHandler(bus *events.EventBus, allowedOrigins ...[]string) *StreamHandler {
	allowed := make(map[string]bool)
	allowAll := true
	if len(allowedOrigins) > 0 && len(allowedOrigins[0]) > 0 {
		allowAll = false
		for _, o := range allowedOrigins[0] {
			if o == "*" {
				allowAll = true
			}
			allowed[o] = true
		}
	}
	return &StreamHandler{bus: bus, allowedOrigins: allowed, allowAllOrigins: allowAll}
}

// HandleSSE serves Server-Sent Events for a specific escrow or all escrows.
// Escrow address is taken from the path parameter "id" if present; otherwise
// all escrows are streamed. Granularity is controlled via ?granularity=L0|L1.
func (sh *StreamHandler) HandleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	escrow := r.PathValue("id")
	granularity := events.ParseGranularity(r.URL.Query().Get("granularity"))

	sub := sh.bus.Subscribe(granularity, escrow)
	defer sh.bus.Unsubscribe(sub.ID)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Send a connected event immediately so the client knows the subscription is alive.
	connData, _ := json.Marshal(map[string]any{
		"status":      "connected",
		"sub_id":      sub.ID,
		"escrow":      escrow,
		"granularity": granularity.String(),
	})
	if _, err := fmt.Fprintf(w, "event: connected\ndata: %s\n\n", connData); err != nil {
		slog.Error("sse: write connected event failed", "error", err)
		return
	}
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-sub.Ch:
			if !ok {
				return
			}
			data, err := json.Marshal(ssePayload(event))
			if err != nil {
				slog.Error("sse: marshal event", "error", err)
				continue
			}
			if _, err = fmt.Fprintf(w, "event: %s\nid: %s\ndata: %s\n\n", event.Name, event.ID, data); err != nil {
				slog.Error("sse: write failed", "error", err)
				return
			}
			flusher.Flush()
		}
	}
}

// wsSubscribeMsg is the client-to-server subscription message for WebSocket.
type wsSubscribeMsg struct {
	Action      string `json:"action"`
	Escrow      string `json:"escrow"`
	Granularity string `json:"granularity"`
}

// HandleWebSocket upgrades the connection and streams events based on a
// subscription message from the client.
func (sh *StreamHandler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			if sh.allowAllOrigins {
				return true
			}
			return sh.allowedOrigins[r.Header.Get("Origin")]
		},
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("ws: upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	// Read subscription message
	var msg wsSubscribeMsg
	if err := conn.ReadJSON(&msg); err != nil {
		slog.Warn("ws: failed to read subscription message", "error", err)
		return
	}
	if msg.Action != "subscribe" {
		if err := conn.WriteJSON(map[string]string{"error": "first message must be a subscribe action"}); err != nil {
			slog.Warn("ws: failed to send invalid-action response", "error", err)
		}
		return
	}

	granularity := events.ParseGranularity(msg.Granularity)
	sub := sh.bus.Subscribe(granularity, msg.Escrow)
	defer sh.bus.Unsubscribe(sub.ID)

	// Send confirmation; abort if the client can't receive it.
	if err := conn.WriteJSON(map[string]string{"status": "subscribed", "sub_id": sub.ID}); err != nil {
		slog.Warn("ws: failed to send subscription confirmation", "sub_id", sub.ID, "error", err)
		return
	}

	ctx := r.Context()

	// Read goroutine to detect client disconnect
	closeCh := make(chan struct{})
	go func() {
		defer close(closeCh)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-closeCh:
			return
		case event, ok := <-sub.Ch:
			if !ok {
				return
			}
			payload := wsPayload(event)
			if err := conn.WriteJSON(payload); err != nil {
				slog.Debug("ws: write failed", "error", err)
				return
			}
		}
	}
}

func ssePayload(e events.Event) map[string]any {
	m := map[string]any{
		"escrow":    e.Escrow,
		"timestamp": e.Timestamp.Unix(),
	}
	if e.Block > 0 {
		m["block"] = e.Block
	}
	if e.Payload != nil {
		m["payload"] = e.Payload
	}
	return m
}

func wsPayload(e events.Event) map[string]any {
	m := map[string]any{
		"event":     e.Name,
		"escrow":    e.Escrow,
		"id":        e.ID,
		"timestamp": e.Timestamp.Unix(),
	}
	if e.Block > 0 {
		m["block"] = e.Block
	}
	if e.Payload != nil {
		m["payload"] = e.Payload
	}
	return m
}
