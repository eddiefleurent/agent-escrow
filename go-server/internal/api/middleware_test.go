package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCORSMiddlewareAllowAllAndPreflight(t *testing.T) {
	t.Parallel()

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})
	handler := corsMiddleware([]string{"*"}, next)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/tasks", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
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
