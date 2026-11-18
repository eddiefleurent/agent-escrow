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
	"github.com/gorilla/websocket"
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

	// A client can prepend any address it likes before the proxy appends the
	// real one. The prefix must not buy a fresh bucket.
	spoofed := doForwarded(trustedRouter, remote, "192.0.2.77, 203.0.113.10")
	if spoofed.Code != http.StatusTooManyRequests {
		t.Fatalf("attacker-supplied left prefix chose a new bucket, got %d", spoofed.Code)
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

func mustCIDRs(t *testing.T, cidrs ...string) []*net.IPNet {
	t.Helper()
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			t.Fatal(err)
		}
		nets = append(nets, n)
	}
	return nets
}

func TestClientIPWalksForwardedForRightToLeft(t *testing.T) {
	t.Parallel()

	trusted := mustCIDRs(t, "10.0.0.0/8", "198.51.100.8/32")
	cases := []struct {
		name    string
		remote  string
		headers []string
		want    string
	}{
		{"attacker prefix through a proxy chain", "10.0.0.2:443", []string{"1.1.1.1, 203.0.113.10, 10.0.0.9"}, "203.0.113.10"},
		{"multiple header lines", "10.0.0.2:443", []string{"1.1.1.1", "203.0.113.10", "10.0.0.9, 198.51.100.8"}, "203.0.113.10"},
		{"hop with port", "10.0.0.2:443", []string{"203.0.113.10:5555"}, "203.0.113.10"},
		{"malformed hop falls back to the peer", "10.0.0.2:443", []string{"203.0.113.10, not-an-ip"}, "10.0.0.2"},
		{"empty hop falls back to the peer", "10.0.0.2:443", []string{"203.0.113.10, "}, "10.0.0.2"},
		{"only trusted hops fall back to the peer", "10.0.0.2:443", []string{"10.0.0.7, 10.0.0.9"}, "10.0.0.2"},
		{"untrusted peer ignores the header", "192.0.2.1:443", []string{"203.0.113.10"}, "192.0.2.1"},
		{"garbage left of the client is ignored", "10.0.0.2:443", []string{"???, 203.0.113.10"}, "203.0.113.10"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
		req.RemoteAddr = tc.remote
		for _, h := range tc.headers {
			req.Header.Add("X-Forwarded-For", h)
		}
		if got := clientIP(req, trusted); got != tc.want {
			t.Errorf("%s: expected %s, got %s", tc.name, tc.want, got)
		}
	}
}

func TestClientIPBucketsIPv6By64(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req.RemoteAddr = "[2001:db8:1:2:aaaa::1]:443"
	first := clientIP(req, nil)
	req.RemoteAddr = "[2001:db8:1:2:bbbb::2]:443"
	second := clientIP(req, nil)
	if first != "2001:db8:1:2::/64" || first != second {
		t.Fatalf("expected both hosts in one /64 bucket, got %q and %q", first, second)
	}
	req.RemoteAddr = "[2001:db8:1:3::1]:443"
	if other := clientIP(req, nil); other == first {
		t.Fatal("a different /64 shared the bucket")
	}
	req.RemoteAddr = "[::ffff:192.0.2.1]:443"
	if v4 := clientIP(req, nil); v4 != "192.0.2.1" {
		t.Fatalf("expected IPv4-mapped peers to key on the IPv4 address, got %q", v4)
	}

	// Forwarded IPv6 clients are resolved first, then bucketed.
	req = httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req.RemoteAddr = "10.0.0.2:443"
	req.Header.Set("X-Forwarded-For", "2001:db8:9:9::5")
	if got := clientIP(req, mustCIDRs(t, "10.0.0.0/8")); got != "2001:db8:9:9::/64" {
		t.Fatalf("expected forwarded IPv6 client bucketed by /64, got %q", got)
	}
}

func TestLimitErrorsCarryCORSHeaders(t *testing.T) {
	t.Parallel()

	const origin = "https://app.example"
	cfg := baseRouterConfig()
	cfg.CORSOrigins = []string{origin}
	cfg.MaxBodyBytes = 16
	cfg.RateLimitRPS = 0.001
	cfg.RateLimitBurst = 1
	router := NewRouter(nil, nil, nil, cfg, nil)

	send := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/escrows", strings.NewReader(body))
		req.RemoteAddr = "198.51.100.90:4000"
		req.Header.Set("Origin", origin)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr
	}

	tooLarge := send(strings.Repeat("x", 17))
	if tooLarge.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", tooLarge.Code)
	}
	limited := send(`{}`)
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", limited.Code)
	}
	for name, rr := range map[string]*httptest.ResponseRecorder{"413": tooLarge, "429": limited} {
		if rr.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Fatalf("%s lacks Access-Control-Allow-Origin", name)
		}
		if !strings.Contains(rr.Header().Get("Access-Control-Expose-Headers"), "Retry-After") {
			t.Fatalf("%s does not expose Retry-After", name)
		}
	}
}

type countingBody struct {
	r    io.Reader
	read int
}

func (c *countingBody) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += n
	return n, err
}

func (c *countingBody) Close() error { return nil }

func TestRateLimitRejectsBeforeReadingBody(t *testing.T) {
	t.Parallel()

	cfg := baseRouterConfig()
	cfg.MaxBodyBytes = 64
	cfg.RateLimitRPS = 0.001
	cfg.RateLimitBurst = 1
	router := NewRouter(nil, nil, nil, cfg, nil)
	const remote = "198.51.100.91:4000"

	if first := doLimited(router, http.MethodPost, "/api/v1/escrows", remote, ""); first.Code == http.StatusTooManyRequests {
		t.Fatal("first request should pass")
	}

	body := &countingBody{r: strings.NewReader(strings.Repeat("y", 1024))}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/escrows", nil)
	req.Body = body
	req.ContentLength = -1
	req.RemoteAddr = remote
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("throttled client should get 429 before the body cap, got %d", rr.Code)
	}
	if body.read != 0 {
		t.Fatalf("throttled request body was read (%d bytes)", body.read)
	}
}

func TestAdminGetIsThrottledBeforeAuth(t *testing.T) {
	t.Parallel()

	cfg := baseRouterConfig()
	cfg.AdminToken = "correct-admin-token"
	cfg.RateLimitRPS = 0.001
	cfg.RateLimitBurst = 2
	router := NewRouter(nil, nil, nil, cfg, nil)

	guess := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/dcts/audit", http.NoBody)
		req.RemoteAddr = "198.51.100.92:4000"
		req.Header.Set("Authorization", "Bearer wrong-guess")
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr
	}
	for range 2 {
		if rr := guess(); rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 inside the burst, got %d", rr.Code)
		}
	}
	limited := guess()
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("expected admin GET token guessing to be throttled, got %d", limited.Code)
	}
	if limited.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After on the admin 429")
	}

	// Public GETs from the same client are unaffected.
	health := doLimited(router, http.MethodGet, "/api/v1/health", "198.51.100.92:4000", "")
	if health.Code != http.StatusOK {
		t.Fatalf("expected public GET to stay unthrottled, got %d", health.Code)
	}
}

func TestStreamingEndpointMatchesOnlyRegisteredRoutes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		method, path string
		want         bool
	}{
		{http.MethodGet, "/api/v1/events", true},
		{http.MethodGet, "/api/v1/events/ws", true},
		{http.MethodGet, "/api/v1/escrows/42/events", true},
		{http.MethodPost, "/api/v1/events", false},
		{http.MethodPost, "/api/v1/escrows/42/events", false},
		{http.MethodGet, "/api/v1/escrows/42/checkpoints/events", false},
		{http.MethodGet, "/api/v1/escrows//events", false},
		{http.MethodGet, "/api/v1/escrows/events", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, http.NoBody)
		if got := isStreamingEndpoint(req); got != tc.want {
			t.Errorf("%s %s: expected %v, got %v", tc.method, tc.path, tc.want, got)
		}
	}
}

func adapterRouter(t *testing.T, maxBody int64) http.Handler {
	t.Helper()
	cfg := baseRouterConfig()
	cfg.A2AEnabled = true
	cfg.UCPEnabled = true
	cfg.MaxBodyBytes = maxBody
	return NewRouter(nil, nil, nil, cfg, nil)
}

func adapterPost(router http.Handler, path, body, requestID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", requestID)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

func TestAdapterTooLargeResponses(t *testing.T) {
	t.Parallel()

	router := adapterRouter(t, 32)
	oversize := strings.Repeat("z", 64)

	for _, path := range []string{"/api/v1/ap2/fund", "/api/v1/ap2/validate", "/api/v1/ucp/checkouts"} {
		rr := adapterPost(router, path, oversize, "adapter-413")
		assertTooLarge(t, rr)
		if decodeError(t, rr)["request_id"] != "adapter-413" {
			t.Fatalf("%s: 413 body lost the request id", path)
		}
	}

	rr := adapterPost(router, "/a2a", oversize, "a2a-413")
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected A2A 413, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decodeJSONRPCError(t, rr)
	if resp.JSONRPC != "2.0" || resp.Error.Code != -32600 || resp.Error.Message != "request body too large" {
		t.Fatalf("expected a JSON-RPC invalid-request envelope, got %s", rr.Body.String())
	}
	if resp.Error.Data["request_id"] != "a2a-413" {
		t.Fatalf("expected request_id in JSON-RPC error data, got %s", rr.Body.String())
	}
}

func TestAdapterErrorsCarryRequestID(t *testing.T) {
	t.Parallel()

	router := adapterRouter(t, 0)

	for _, path := range []string{"/api/v1/ap2/validate", "/api/v1/ap2/fund", "/api/v1/ucp/checkouts"} {
		rr := adapterPost(router, path, `{not json`, "adapter-400")
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", path, rr.Code)
		}
		if got := decodeError(t, rr)["request_id"]; got != "adapter-400" {
			t.Fatalf("%s: expected request_id in the error body, got %q", path, got)
		}
	}

	rr := adapterPost(router, "/a2a", `{not json`, "a2a-parse")
	resp := decodeJSONRPCError(t, rr)
	if resp.Error == nil || resp.Error.Code != -32700 || resp.Error.Data["request_id"] != "a2a-parse" {
		t.Fatalf("expected JSON-RPC parse error with request_id data, got %s", rr.Body.String())
	}
}

type jsonRPCErrorEnvelope struct {
	JSONRPC string `json:"jsonrpc"`
	Error   *struct {
		Code    int               `json:"code"`
		Message string            `json:"message"`
		Data    map[string]string `json:"data"`
	} `json:"error"`
}

func decodeJSONRPCError(t *testing.T, rr *httptest.ResponseRecorder) jsonRPCErrorEnvelope {
	t.Helper()
	var resp jsonRPCErrorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || resp.Error == nil {
		t.Fatalf("decode JSON-RPC error %q: %v", rr.Body.String(), err)
	}
	return resp
}

func TestWebSocketUpgradeCarriesRequestID(t *testing.T) {
	t.Parallel()

	cfg := baseRouterConfig()
	cfg.EventsEnabled = true
	srv := httptest.NewServer(NewRouter(nil, nil, nil, cfg, events.NewEventBus(4)))
	t.Cleanup(srv.Close)

	header := http.Header{}
	header.Set("X-Request-ID", "ws-upgrade-1")
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/v1/events/ws", header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected 101, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Request-ID"); got != "ws-upgrade-1" {
		t.Fatalf("expected the upgrade response to echo the request id, got %q", got)
	}
}
