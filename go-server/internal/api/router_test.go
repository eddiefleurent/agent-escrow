package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
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
	enabledCfg.AdminToken = strings.Repeat("x", 32)
	enabledRouter := NewRouter(nil, nil, nil, enabledCfg, nil)

	req = httptest.NewRequest(http.MethodPost, "/api/v1/emergency/freeze-address", nil)
	req.Header.Set("Authorization", "Bearer "+enabledCfg.AdminToken)
	rr = httptest.NewRecorder()
	enabledRouter.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected authenticated request to reach the handler and reject empty body, got %d", rr.Code)
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

func TestNewRouterAdminRoutesRequireBearer(t *testing.T) {
	t.Parallel()

	cfg := baseRouterConfig()
	cfg.EmergencyEnabled = true
	cfg.AdminToken = strings.Repeat("x", 32)
	router := NewRouter(nil, nil, nil, cfg, nil)

	for path, method := range map[string]string{
		"/api/v1/emergency/freeze-address":   http.MethodPost,
		"/api/v1/emergency/unfreeze-address": http.MethodPost,
		"/api/v1/emergency/freeze-escrow":    http.MethodPost,
		"/api/v1/emergency/unfreeze-escrow":  http.MethodPost,
		"/api/v1/emergency/resolve":          http.MethodPost,
		"/api/v1/emergency/frozen-addresses": http.MethodGet,
		"/api/v1/emergency/actions":          http.MethodGet,
		"/api/v1/dcts/emergency-override":    http.MethodPost,
		"/api/v1/dcts/audit":                 http.MethodGet,
	} {
		for _, header := range []string{"", "Bearer " + strings.Repeat("y", 32), "Basic " + cfg.AdminToken} {
			req := httptest.NewRequest(method, path, http.NoBody)
			if header != "" {
				req.Header.Set("Authorization", header)
			}
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized || rr.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("expected bearer challenge for %s, got %d", path, rr.Code)
			}
		}
	}
}

func TestDCTAuditRejectsEmptyConfiguredToken(t *testing.T) {
	t.Parallel()

	router := NewRouter(nil, nil, nil, baseRouterConfig(), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dcts/audit", http.NoBody)
	req.Header.Set("Authorization", "Bearer ")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without a configured token, got %d", rr.Code)
	}
}
