package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/events"
)

func baseRouterConfig() *config.Config {
	return &config.Config{
		CORSOrigins:      nil,
		RequestTimeout:   1 * time.Second,
		TxTimeout:        1 * time.Second,
		EmergencyEnabled: false,
		EventsEnabled:    false,
		A2AEnabled:       false,
		UCPEnabled:       false,
		X402Enabled:      false,
	}
}

func TestNewRouterHealthRoute(t *testing.T) {
	t.Parallel()

	cfg := baseRouterConfig()
	router := NewRouter(nil, nil, nil, cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected health route 200, got %d", rr.Code)
	}
}

func TestNewRouterEmergencyRoutesConditional(t *testing.T) {
	t.Parallel()

	disabledCfg := baseRouterConfig()
	disabledRouter := NewRouter(nil, nil, nil, disabledCfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/emergency/freeze-address", nil)
	rr := httptest.NewRecorder()
	disabledRouter.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected emergency route 404 when disabled, got %d", rr.Code)
	}

	enabledCfg := baseRouterConfig()
	enabledCfg.EmergencyEnabled = true
	enabledRouter := NewRouter(nil, nil, nil, enabledCfg, nil)

	req = httptest.NewRequest(http.MethodPost, "/api/v1/emergency/freeze-address", nil)
	rr = httptest.NewRecorder()
	enabledRouter.ServeHTTP(rr, req)
	if rr.Code == http.StatusNotFound {
		t.Fatal("expected emergency route to be registered when enabled")
	}
}

func TestNewRouterEventRoutesConditional(t *testing.T) {
	t.Parallel()

	disabledCfg := baseRouterConfig()
	disabledRouter := NewRouter(nil, nil, nil, disabledCfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/ws", nil)
	rr := httptest.NewRecorder()
	disabledRouter.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected event websocket route 404 when disabled, got %d", rr.Code)
	}

	enabledCfg := baseRouterConfig()
	enabledCfg.EventsEnabled = true
	bus := events.NewEventBus(4)
	enabledRouter := NewRouter(nil, nil, nil, enabledCfg, bus)

	req = httptest.NewRequest(http.MethodGet, "/api/v1/events/ws", nil)
	rr = httptest.NewRecorder()
	enabledRouter.ServeHTTP(rr, req)
	if rr.Code == http.StatusNotFound {
		t.Fatal("expected event websocket route to be registered when enabled")
	}
}
