package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/authz"
)

func TestNewHTTPServerConfig(t *testing.T) {
	t.Parallel()

	handler := http.NewServeMux()
	srv := newHTTPServer(9876, handler)

	if srv.Addr != ":9876" {
		t.Fatalf("expected addr :9876, got %s", srv.Addr)
	}
	if srv.Handler != handler {
		t.Fatal("expected handler to be preserved")
	}
	if srv.ReadTimeout != 15*time.Second {
		t.Fatalf("expected read timeout 15s, got %s", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 60*time.Second {
		t.Fatalf("expected write timeout 60s, got %s", srv.WriteTimeout)
	}
	if srv.IdleTimeout != 120*time.Second {
		t.Fatalf("expected idle timeout 120s, got %s", srv.IdleTimeout)
	}
}

func TestNewLoggerAddsRequestID(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newLogger(&buf)
	logger.InfoContext(authz.WithRequestID(context.Background(), "main-logger-1"), "handled")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("decode log line %q: %v", buf.String(), err)
	}
	if rec["request_id"] != "main-logger-1" {
		t.Fatalf("expected request_id on the production logger, got %v", rec)
	}
}
