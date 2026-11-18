package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/events"
	"github.com/google/uuid"
)

func TestBodyLimitAllowsAtCapAndRejectsAbove(t *testing.T) {
	t.Parallel()

	cfg := baseRouterConfig()
	cfg.MaxBodyBytes = 32
	router := NewRouter(nil, nil, nil, cfg, nil)

	under := httptest.NewRequest(http.MethodGet, "/api/v1/health", strings.NewReader(strings.Repeat("a", 32)))
	underRR := httptest.NewRecorder()
	router.ServeHTTP(underRR, under)
	if underRR.Code != http.StatusOK {
		t.Fatalf("expected body at the cap to reach the handler, got %d: %s", underRR.Code, underRR.Body.String())
	}

	over := httptest.NewRequest(http.MethodPost, "/api/v1/escrows", strings.NewReader(strings.Repeat("b", 33)))
	overRR := httptest.NewRecorder()
	router.ServeHTTP(overRR, over)
	assertTooLarge(t, overRR)

	// Chunked bodies have no usable Content-Length, so the cap has to trip
	// inside http.MaxBytesReader rather than the header check.
	chunked := httptest.NewRequest(http.MethodPost, "/api/v1/escrows", nil)
	chunked.Body = io.NopCloser(strings.NewReader(strings.Repeat("c", 33)))
	chunked.ContentLength = -1
	chunkedRR := httptest.NewRecorder()
	router.ServeHTTP(chunkedRR, chunked)
	assertTooLarge(t, chunkedRR)
}

func TestBodyLimitDefaultIsOneMiB(t *testing.T) {
	t.Parallel()

	router := NewRouter(nil, nil, nil, baseRouterConfig(), nil)

	under := httptest.NewRequest(http.MethodGet, "/api/v1/health", strings.NewReader(strings.Repeat("a", 64)))
	underRR := httptest.NewRecorder()
	router.ServeHTTP(underRR, under)
	if underRR.Code != http.StatusOK {
		t.Fatalf("expected a small body to pass the default cap, got %d", underRR.Code)
	}

	over := httptest.NewRequest(http.MethodPost, "/api/v1/escrows", strings.NewReader(strings.Repeat("b", int(config.DefaultMaxBodyBytes)+1)))
	overRR := httptest.NewRecorder()
	router.ServeHTTP(overRR, over)
	assertTooLarge(t, overRR)
}

func TestBodyUnderCapReachesHandler(t *testing.T) {
	t.Parallel()

	cfg := baseRouterConfig()
	cfg.MaxBodyBytes = 128
	router := NewRouter(nil, nil, nil, cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/escrows", strings.NewReader(`{"amount":"1"}`))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected the handler to validate the body, got %d: %s", rr.Code, rr.Body.String())
	}
	body := decodeError(t, rr)
	if body["error"] == "request body too large" || body["error"] == "invalid json" {
		t.Fatalf("handler did not see the request body, got %q", body["error"])
	}
}

func TestRequestIDRoundTripGeneratedAndReplaced(t *testing.T) {
	t.Parallel()

	router := NewRouter(nil, nil, nil, baseRouterConfig(), nil)
	const validID = "trace_01.ABC-xyz"

	echo := postEscrow(t, router, validID, `{}`)
	if echo.Header().Get("X-Request-ID") != validID {
		t.Fatalf("expected request id to round-trip, got %q", echo.Header().Get("X-Request-ID"))
	}
	echoBody := decodeError(t, echo)
	if echoBody["request_id"] != validID {
		t.Fatalf("expected error body request_id %q, got %q", validID, echoBody["request_id"])
	}
	if echoBody["error"] == "" {
		t.Fatal("expected a JSON error body")
	}

	generated := postEscrow(t, router, "", `{}`)
	generatedID := generated.Header().Get("X-Request-ID")
	parsed, err := uuid.Parse(generatedID)
	if err != nil || parsed.Version() != 4 {
		t.Fatalf("expected a generated UUID v4, got %q (%v)", generatedID, err)
	}
	if decodeError(t, generated)["request_id"] != generatedID {
		t.Fatal("generated request id missing from the error body")
	}

	for _, invalid := range []string{"bad id", strings.Repeat("a", 129), "id/slash", "line\nbreak", "café"} {
		rr := postEscrow(t, router, invalid, `{}`)
		got := rr.Header().Get("X-Request-ID")
		if got == invalid || !validRequestID(got) {
			t.Fatalf("invalid id %q was not replaced, got %q", invalid, got)
		}
		replaced, parseErr := uuid.Parse(got)
		if parseErr != nil || replaced.Version() != 4 {
			t.Fatalf("expected replacement UUID for %q, got %q", invalid, got)
		}
		if decodeError(t, rr)["request_id"] != got {
			t.Fatalf("error body did not use the replacement id for %q", invalid)
		}
	}
}

func TestRequestIDLogged(t *testing.T) {
	// A private handler avoids the std log bridge, which deadlocks if the
	// capture forwards into the process-wide slog logger.
	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(orig) })

	router := NewRouter(nil, nil, nil, baseRouterConfig(), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", http.NoBody)
	req.Header.Set("X-Request-ID", "log-field-test-1")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	logged := buf.String()
	if !strings.Contains(logged, "request_id=log-field-test-1") || !strings.Contains(logged, "path=/api/v1/health") {
		t.Fatalf("request log line missing request_id: %s", logged)
	}
}

func TestRateLimitBurstRetryAfterAndRefill(t *testing.T) {
	t.Parallel()

	cfg := baseRouterConfig()
	cfg.RateLimitRPS = 5
	cfg.RateLimitBurst = 1
	router := NewRouter(nil, nil, nil, cfg, nil)

	first := doLimited(router, http.MethodPost, "/api/v1/escrows", "198.51.100.8:4000", "")
	if first.Code == http.StatusTooManyRequests {
		t.Fatalf("first request should consume the burst, got %d", first.Code)
	}

	limited := doLimited(router, http.MethodPost, "/api/v1/escrows", "198.51.100.8:4000", "")
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after the burst, got %d", limited.Code)
	}
	retryAfter, err := strconv.Atoi(limited.Header().Get("Retry-After"))
	if err != nil || retryAfter < 1 {
		t.Fatalf("expected Retry-After seconds, got %q", limited.Header().Get("Retry-After"))
	}
	body := decodeError(t, limited)
	if body["error"] != "rate limit exceeded" || body["request_id"] == "" {
		t.Fatalf("unexpected 429 body: %#v", body)
	}
	if limited.Header().Get("X-Request-ID") != body["request_id"] {
		t.Fatal("429 body request_id does not match the response header")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		refilled := doLimited(router, http.MethodPost, "/api/v1/escrows", "198.51.100.8:4000", "")
		if refilled.Code != http.StatusTooManyRequests {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("token bucket did not refill")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestRateLimitMutatingMethodsAndGetExempt(t *testing.T) {
	t.Parallel()

	cfg := baseRouterConfig()
	cfg.RateLimitRPS = 0.001
	cfg.RateLimitBurst = 3
	router := NewRouter(nil, nil, nil, cfg, nil)
	const remote = "198.51.100.20:4000"

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch} {
		rr := doLimited(router, method, "/api/v1/escrows", remote, "")
		if rr.Code == http.StatusTooManyRequests {
			t.Fatalf("%s should have been inside the burst", method)
		}
	}
	denied := doLimited(router, http.MethodDelete, "/api/v1/escrows", remote, "")
	if denied.Code != http.StatusTooManyRequests {
		t.Fatalf("expected DELETE past the burst to be 429, got %d", denied.Code)
	}
	health := doLimited(router, http.MethodGet, "/api/v1/health", remote, "")
	if health.Code != http.StatusOK {
		t.Fatalf("GET should be exempt from the limiter, got %d", health.Code)
	}
}

func TestRateLimitSSEExempt(t *testing.T) {
	t.Parallel()

	cfg := baseRouterConfig()
	cfg.EventsEnabled = true
	cfg.RateLimitRPS = 0.001
	cfg.RateLimitBurst = 1
	router := NewRouter(nil, nil, nil, cfg, events.NewEventBus(4))
	const remote = "198.51.100.50:4000"

	first := doLimited(router, http.MethodPost, "/api/v1/escrows", remote, "")
	if first.Code == http.StatusTooManyRequests {
		t.Fatal("first post should pass the limiter")
	}
	second := doLimited(router, http.MethodPost, "/api/v1/escrows", remote, "")
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("expected the limiter to be exhausted, got %d", second.Code)
	}

	for range 3 {
		code, contentType := serveSSE(t, router, remote)
		if code != http.StatusOK {
			t.Fatalf("expected SSE to stay exempt, got %d", code)
		}
		if !strings.Contains(contentType, "text/event-stream") {
			t.Fatalf("expected an SSE stream, got %q", contentType)
		}
	}
}

// serveSSE drives the real SSE route until the handler flushes its greeting,
// then cancels the request so the long-lived stream returns.
func serveSSE(t *testing.T, router http.Handler, remote string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", http.NoBody).WithContext(ctx)
	req.RemoteAddr = remote
	rr := &cancelOnFlush{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	done := make(chan struct{})
	go func() {
		router.ServeHTTP(rr, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		cancel()
		<-done
		t.Fatal("SSE handler did not return")
	}
	return rr.Code, rr.Header().Get("Content-Type")
}

type cancelOnFlush struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
	once   sync.Once
}

func (c *cancelOnFlush) Flush() {
	c.once.Do(func() { c.cancel() })
	c.ResponseRecorder.Flush()
}

func TestForwardedForIgnoredUnlessProxyIsTrusted(t *testing.T) {
	t.Parallel()

	const remote = "198.51.100.8:4000"
	untrusted := baseRouterConfig()
	untrusted.RateLimitRPS = 0.001
	untrusted.RateLimitBurst = 1
	untrustedRouter := NewRouter(nil, nil, nil, untrusted, nil)

	first := doForwarded(untrustedRouter, remote, "203.0.113.10")
	second := doForwarded(untrustedRouter, remote, "203.0.113.11")
	if first.Code == http.StatusTooManyRequests {
		t.Fatal("first untrusted request should pass")
	}
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("X-Forwarded-For spoof should share the RemoteAddr bucket, got %d", second.Code)
	}

	trusted := baseRouterConfig()
	trusted.RateLimitRPS = 0.001
	trusted.RateLimitBurst = 1
	_, network, err := net.ParseCIDR("198.51.100.8/32")
	if err != nil {
		t.Fatal(err)
	}
	trusted.TrustedProxies = []*net.IPNet{network}
	trustedRouter := NewRouter(nil, nil, nil, trusted, nil)

	left := doForwarded(trustedRouter, remote, "203.0.113.10")
	right := doForwarded(trustedRouter, remote, "203.0.113.11, 198.51.100.8")
	again := doForwarded(trustedRouter, remote, "203.0.113.10")
	if left.Code == http.StatusTooManyRequests || right.Code == http.StatusTooManyRequests {
		t.Fatalf("trusted proxy should key on X-Forwarded-For, got %d and %d", left.Code, right.Code)
	}
	if again.Code != http.StatusTooManyRequests {
		t.Fatalf("expected the forwarded client to be limited, got %d", again.Code)
	}
}

func assertTooLarge(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("expected JSON error, got %q", rr.Header().Get("Content-Type"))
	}
	body := decodeError(t, rr)
	if body["error"] != "request body too large" {
		t.Fatalf("unexpected error %q", body["error"])
	}
	if body["request_id"] == "" || body["request_id"] != rr.Header().Get("X-Request-ID") {
		t.Fatalf("413 body missing request id: %#v header %q", body, rr.Header().Get("X-Request-ID"))
	}
}

func postEscrow(t *testing.T, router http.Handler, requestID, payload string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/escrows", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

func doLimited(router http.Handler, method, path, remote, forwarded string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
	req.RemoteAddr = remote
	req.Header.Set("Content-Type", "application/json")
	if forwarded != "" {
		req.Header.Set("X-Forwarded-For", forwarded)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

func doForwarded(router http.Handler, remote, forwarded string) *httptest.ResponseRecorder {
	return doLimited(router, http.MethodPost, "/api/v1/escrows", remote, forwarded)
}

func decodeError(t *testing.T, rr *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rr.Body.String(), err)
	}
	return body
}
