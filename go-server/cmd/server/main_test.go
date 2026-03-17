package main

import (
	"net/http"
	"testing"
	"time"
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
