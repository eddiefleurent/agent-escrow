package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsStreamingEndpoint(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path string
		want bool
	}{
		{path: "/api/v1/events", want: true},
		{path: "/api/v1/events/ws", want: true},
		{path: "/api/v1/escrows/42/events", want: true},
		{path: "/api/v1/escrows/42", want: false},
		{path: "/api/v1/tasks", want: false},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		got := isStreamingEndpoint(req)
		if got != tc.want {
			t.Fatalf("path %q: expected %v, got %v", tc.path, tc.want, got)
		}
	}
}

func TestCORSMiddlewareAllowAllAndPreflight(t *testing.T) {
	t.Parallel()

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})
	handler := corsMiddleware(nil, next)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/tasks", nil)
	req.Header.Set("Origin", "https://example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for preflight, got %d", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("expected wildcard CORS origin, got %q", rr.Header().Get("Access-Control-Allow-Origin"))
	}
	if nextCalled {
		t.Fatal("expected preflight to short-circuit before next handler")
	}
}

func TestCORSMiddlewareAllowListedOrigin(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	handler := corsMiddleware([]string{"https://allowed.test"}, next)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	req.Header.Set("Origin", "https://allowed.test")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected next handler status, got %d", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "https://allowed.test" {
		t.Fatalf("expected reflected allowed origin, got %q", rr.Header().Get("Access-Control-Allow-Origin"))
	}
	if rr.Header().Get("Vary") != "Origin" {
		t.Fatalf("expected Vary: Origin, got %q", rr.Header().Get("Vary"))
	}
}

func TestTimeoutMiddlewareReadAndTxPaths(t *testing.T) {
	t.Parallel()

	slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	})
	handler := timeoutMiddleware(5*time.Millisecond, 100*time.Millisecond, slow)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	getRR := httptest.NewRecorder()
	handler.ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected GET timeout status 503, got %d", getRR.Code)
	}
	if !strings.Contains(getRR.Body.String(), "request timeout") {
		t.Fatalf("expected timeout body, got %q", getRR.Body.String())
	}

	postReq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", nil)
	postRR := httptest.NewRecorder()
	handler.ServeHTTP(postRR, postReq)
	if postRR.Code != http.StatusNoContent {
		t.Fatalf("expected POST to use tx timeout path and succeed, got %d", postRR.Code)
	}
}

func TestTimeoutMiddlewareSkipsStreamingEndpoints(t *testing.T) {
	t.Parallel()

	slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	})
	handler := timeoutMiddleware(5*time.Millisecond, 5*time.Millisecond, slow)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected streaming endpoint to bypass timeout middleware, got %d", rr.Code)
	}
}
