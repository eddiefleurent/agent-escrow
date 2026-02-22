package api

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/events"
	"github.com/gorilla/websocket"
)

func TestSSEHandler_ReceivesEvents(t *testing.T) {
	bus := events.NewEventBus(16)
	sh := NewStreamHandler(bus)

	srv := httptest.NewServer(http.HandlerFunc(sh.HandleSSE))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "?granularity=L1")
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %q", ct)
	}

	// Publish an event after the client connects
	go func() {
		time.Sleep(50 * time.Millisecond)
		bus.Publish(events.Event{
			Name:   events.EventEscrowFunded,
			Escrow: "0xABC",
			Level:  events.L1,
			ID:     "test-sse-1",
			Block:  12345,
		})
	}()

	lineCh := make(chan string, 64)
	go func() {
		defer close(lineCh)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
	}()

	var gotEvent, gotID, gotData bool
	deadline := time.After(3 * time.Second)

	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for SSE event")
		case line, ok := <-lineCh:
			if !ok {
				goto done
			}
			if strings.HasPrefix(line, "event: ") && strings.TrimPrefix(line, "event: ") == events.EventEscrowFunded {
				gotEvent = true
			}
			if strings.HasPrefix(line, "id: ") && strings.TrimPrefix(line, "id: ") == "test-sse-1" {
				gotID = true
			}
			if strings.HasPrefix(line, "data: ") {
				gotData = true
			}
			if gotEvent && gotID && gotData {
				goto done
			}
		}
	}

done:
	if !gotEvent {
		t.Error("did not receive event line")
	}
	if !gotID {
		t.Error("did not receive id line")
	}
	if !gotData {
		t.Error("did not receive data line")
	}
}

func TestSSEHandler_EscrowFiltered(t *testing.T) {
	bus := events.NewEventBus(16)

	mux := http.NewServeMux()
	sh := NewStreamHandler(bus)
	mux.HandleFunc("GET /api/v1/escrows/{id}/events", sh.HandleSSE)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/escrows/0xABC/events?granularity=L1")
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer resp.Body.Close()

	go func() {
		time.Sleep(50 * time.Millisecond)
		// This event should NOT be delivered (wrong escrow)
		bus.Publish(events.Event{
			Name:   events.EventEscrowFunded,
			Escrow: "0xDEF",
			Level:  events.L1,
			ID:     "wrong-escrow",
		})
		time.Sleep(20 * time.Millisecond)
		// This event SHOULD be delivered
		bus.Publish(events.Event{
			Name:   events.EventEscrowApproved,
			Escrow: "0xABC",
			Level:  events.L1,
			ID:     "right-escrow",
		})
	}()

	lineCh := make(chan string, 64)
	go func() {
		defer close(lineCh)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
	}()

	deadline := time.After(3 * time.Second)
	var receivedID string

	for {
		select {
		case <-deadline:
			if receivedID == "" {
				t.Fatal("timed out waiting for filtered SSE event")
			}
			goto check
		case line, ok := <-lineCh:
			if !ok {
				goto check
			}
			if strings.HasPrefix(line, "id: ") {
				receivedID = strings.TrimPrefix(line, "id: ")
				goto check
			}
		}
	}

check:
	if receivedID != "right-escrow" {
		t.Errorf("expected event id 'right-escrow', got %q", receivedID)
	}
}

func TestWebSocketHandler_SubscribeAndReceive(t *testing.T) {
	bus := events.NewEventBus(16)
	sh := NewStreamHandler(bus)

	srv := httptest.NewServer(http.HandlerFunc(sh.HandleWebSocket))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close()

	// Send subscription message
	subMsg := map[string]string{
		"action":      "subscribe",
		"escrow":      "*",
		"granularity": "L1",
	}
	if err := conn.WriteJSON(subMsg); err != nil {
		t.Fatalf("ws write subscribe: %v", err)
	}

	// Read subscription confirmation
	var confirmation map[string]string
	if err := conn.ReadJSON(&confirmation); err != nil {
		t.Fatalf("ws read confirmation: %v", err)
	}
	if confirmation["status"] != "subscribed" {
		t.Errorf("expected status=subscribed, got %v", confirmation)
	}

	// Publish an event
	bus.Publish(events.Event{
		Name:   events.EventEscrowSubmitted,
		Escrow: "0xABC",
		Level:  events.L1,
		ID:     "ws-test-1",
		Block:  99999,
	})

	// Read the event
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var wsEvent map[string]any
	if err := conn.ReadJSON(&wsEvent); err != nil {
		t.Fatalf("ws read event: %v", err)
	}

	if wsEvent["event"] != events.EventEscrowSubmitted {
		t.Errorf("expected event=%q, got %v", events.EventEscrowSubmitted, wsEvent["event"])
	}
	if wsEvent["id"] != "ws-test-1" {
		t.Errorf("expected id=ws-test-1, got %v", wsEvent["id"])
	}
}

func TestWebSocketHandler_InvalidFirstMessage(t *testing.T) {
	bus := events.NewEventBus(16)
	sh := NewStreamHandler(bus)

	srv := httptest.NewServer(http.HandlerFunc(sh.HandleWebSocket))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close()

	// Send invalid action
	if err := conn.WriteJSON(map[string]string{"action": "invalid"}); err != nil {
		t.Fatalf("ws write: %v", err)
	}

	var resp map[string]string
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("ws read: %v", err)
	}
	if resp["error"] == "" {
		t.Error("expected error response for invalid action")
	}
}

func TestSSEHandler_IntegrationWithRouter(t *testing.T) {
	bus := events.NewEventBus(16)
	env := setup(t)
	env.cfg.EventsEnabled = true
	env.mux = NewRouter(env.db, env.mock, env.idx, env.cfg, bus)

	srv := httptest.NewServer(env.mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/events?granularity=L1")
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %q", ct)
	}
}

func TestMCPSubscribeEvents(t *testing.T) {
	bus := events.NewEventBus(16)

	// Publish some events
	for i := 0; i < 5; i++ {
		bus.Publish(events.Event{
			Name:   events.EventEscrowFunded,
			Escrow: "0xABC",
			Level:  events.L1,
			ID:     events.EventEscrowFunded + "-" + itoa(uint64(i)),
			Block:  uint64(100 + i),
		})
	}

	// Query recent events
	recent := bus.RecentEvents("", events.L1, "", 10)
	if len(recent) != 5 {
		t.Fatalf("expected 5 events, got %d", len(recent))
	}

	// Cursor-based pagination
	cursor := recent[2].ID
	page2 := bus.RecentEvents("", events.L1, cursor, 10)
	if len(page2) != 2 {
		t.Fatalf("expected 2 events after cursor, got %d", len(page2))
	}

	// Verify JSON serialization
	data, err := json.Marshal(map[string]any{
		"events": recent,
		"cursor": recent[len(recent)-1].ID,
		"count":  len(recent),
	})
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty JSON output")
	}
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
