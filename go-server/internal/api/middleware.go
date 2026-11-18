package api

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/authz"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/google/uuid"
)

// isStreamingEndpoint returns true for SSE and WebSocket event stream paths
// that should bypass request timeout middleware.
func isStreamingEndpoint(r *http.Request) bool {
	p := r.URL.Path
	if p == "/api/v1/events" || p == "/api/v1/events/ws" {
		return true
	}
	if strings.HasPrefix(p, "/api/v1/escrows/") && strings.HasSuffix(p, "/events") {
		return true
	}
	return false
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: 200}
		next.ServeHTTP(rw, r)
		slog.InfoContext(r.Context(), "http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.statusCode,
			"duration", time.Since(start),
			"request_id", authz.RequestIDFrom(r.Context()),
		)
	})
}

func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &responseWriter{ResponseWriter: w, statusCode: 200}
		defer func() {
			if err := recover(); err != nil {
				slog.Error("panic recovered",
					"error", err,
					"method", r.Method,
					"path", r.URL.Path,
					"request_id", rw.Header().Get("X-Request-ID"),
				)
				if !rw.written {
					http.Error(rw, "internal server error", http.StatusInternalServerError)
					return
				}
			}
		}()
		next.ServeHTTP(rw, r)
	})
}

// requireAdmin authenticates the operator before invoking a privileged handler.
func requireAdmin(adminToken, ownerAddress string, next http.HandlerFunc) http.HandlerFunc {
	expected := sha256.Sum256([]byte(adminToken))
	return func(w http.ResponseWriter, r *http.Request) {
		scheme, token, separated := strings.Cut(r.Header.Get("Authorization"), " ")
		provided := sha256.Sum256([]byte(token))
		if adminToken == "" || !separated || !strings.EqualFold(scheme, "Bearer") ||
			subtle.ConstantTimeCompare(provided[:], expected[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		// DCT emergency overrides authorize against the configured factory owner.
		ctx := authz.WithCaller(r.Context(), authz.Principal{
			Address:       ownerAddress,
			Authenticated: ownerAddress != "",
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// corsMiddleware checks the request Origin against allowedOrigins.
// Only an explicit "*" permits all origins.
func corsMiddleware(allowedOrigins []string, next http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	allowAll := false
	for _, o := range allowedOrigins {
		if o == "*" {
			allowAll = true
		}
		allowed[o] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		if allowAll {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if len(allowed) > 0 {
			w.Header().Add("Vary", "Origin")
			if allowed[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID, Retry-After")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if rw.written {
		return
	}
	rw.written = true
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(b)
}

// Flush delegates to the underlying ResponseWriter if it implements http.Flusher.
// This preserves SSE streaming through the middleware chain.
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack delegates to the underlying ResponseWriter if it implements http.Hijacker.
// This is required for gorilla/websocket's Upgrader to take over the connection.
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("underlying ResponseWriter does not implement http.Hijacker")
}

// timeoutMiddleware applies request timeouts based on the HTTP method and path.
// POST endpoints that interact with the chain use txTimeout (default 90s).
// GET/read endpoints use requestTimeout (default 10s).
func timeoutMiddleware(requestTimeout, txTimeout time.Duration, next http.Handler) http.Handler {
	// TimeoutHandler serves the inner handler on its own ResponseWriter.
	// Copy the request id onto that writer so JSON error bodies can see it.
	propagated := propagateRequestID(next)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// SSE and WebSocket endpoints are long-lived; skip timeout wrapping.
		if isStreamingEndpoint(r) {
			propagated.ServeHTTP(w, r)
			return
		}
		// Built per request so the timeout JSON can carry this request's id.
		// The id alphabet cannot break the JSON string.
		body := `{"error":"request timeout"}`
		if id := authz.RequestIDFrom(r.Context()); id != "" {
			body = `{"error":"request timeout","request_id":"` + id + `"}`
		}
		handler := http.TimeoutHandler(propagated, requestTimeout, body)
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/") {
			handler = http.TimeoutHandler(propagated, txTimeout, body)
		}
		handler.ServeHTTP(w, r)
	})
}

const maxRequestIDLen = 128

// propagateRequestID copies the context request id onto the writer the
// handler actually uses. http.TimeoutHandler does not share the outer header
// map, and error bodies are built from the inner writer.
func propagateRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := authz.RequestIDFrom(r.Context()); id != "" && w.Header().Get("X-Request-ID") == "" {
			w.Header().Set("X-Request-ID", id)
		}
		next.ServeHTTP(w, r)
	})
}

// requestIDMiddleware echoes a valid inbound X-Request-ID or generates one.
// The id is stored with authz.WithRequestID for the rest of the request.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validRequestID(id) {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := authz.WithRequestID(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// validRequestID reports whether id is at most 128 characters and drawn from
// the printable set [A-Za-z0-9-_.]. Empty and unsafe values are rejected so
// they are never copied into the response header.
func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}

// bodyLimitMiddleware caps the request body. Oversize bodies, including
// chunked bodies with no Content-Length, return 413 before the handler runs.
// A non-positive maxBytes selects config.DefaultMaxBodyBytes.
func bodyLimitMiddleware(maxBytes int64, next http.Handler) http.Handler {
	if maxBytes <= 0 {
		maxBytes = config.DefaultMaxBodyBytes
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			next.ServeHTTP(w, r)
			return
		}
		if r.ContentLength > maxBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
			return
		}
		limited := http.MaxBytesReader(w, r.Body, maxBytes)
		body, err := io.ReadAll(limited)
		closeErr := limited.Close()
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
			return
		}
		if err != nil || closeErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		r.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimitMiddleware applies a per-client-IP token bucket to mutating
// methods. SSE and WebSocket paths are exempt. A non-positive rps or burst
// disables the limiter. X-Forwarded-For is consulted only for trusted peers.
func rateLimitMiddleware(rps float64, burst int, trusted []*net.IPNet, next http.Handler) http.Handler {
	if rps <= 0 || burst <= 0 {
		return next
	}
	limiter := newIPRateLimiter(rps, burst, rateLimitIdleTTL, rateLimitMaxBuckets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isStreamingEndpoint(r) || !isMutatingMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		allowed, retryAfter := limiter.allow(clientIP(r, trusted))
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// clientIP returns the address the rate limiter keys on.
// X-Forwarded-For is used only when RemoteAddr is inside a trusted proxy
// CIDR, so a client cannot choose its own bucket by setting the header.
// The leftmost address is the client. A trusted proxy must overwrite the
// header rather than append an untrusted prefix.
func clientIP(r *http.Request, trusted []*net.IPNet) string {
	remote := parseRemoteIP(r.RemoteAddr)
	if remote != nil && ipInNets(remote, trusted) {
		if forwarded := forwardedClientIP(r.Header.Get("X-Forwarded-For")); forwarded != nil {
			return forwarded.String()
		}
	}
	if remote != nil {
		return remote.String()
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func parseRemoteIP(remoteAddr string) net.IP {
	host := remoteAddr
	if parsed, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = parsed
	}
	return net.ParseIP(strings.Trim(host, "[]"))
}

func forwardedClientIP(header string) net.IP {
	first, _, _ := strings.Cut(header, ",")
	first = strings.TrimSpace(first)
	if first == "" {
		return nil
	}
	if host, _, err := net.SplitHostPort(first); err == nil {
		first = host
	}
	return net.ParseIP(strings.Trim(first, "[]"))
}

func ipInNets(ip net.IP, nets []*net.IPNet) bool {
	for _, network := range nets {
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}
