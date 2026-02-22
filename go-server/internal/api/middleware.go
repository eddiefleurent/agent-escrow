package api

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
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
		slog.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.statusCode,
			"duration", time.Since(start),
		)
	})
}

func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &responseWriter{ResponseWriter: w, statusCode: 200}
		defer func() {
			if err := recover(); err != nil {
				slog.Error("panic recovered", "error", err, "method", r.Method, "path", r.URL.Path)
				if !rw.written {
					http.Error(rw, "internal server error", http.StatusInternalServerError)
				}
			}
		}()
		next.ServeHTTP(rw, r)
	})
}

// corsMiddleware checks the request Origin against allowedOrigins.
// If allowedOrigins is empty, all origins are permitted ("*").
func corsMiddleware(allowedOrigins []string, next http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		if len(allowed) == 0 {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

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
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}

// timeoutMiddleware applies request timeouts based on the HTTP method and path.
// POST endpoints that interact with the chain use txTimeout (default 90s).
// GET/read endpoints use requestTimeout (default 10s).
func timeoutMiddleware(requestTimeout, txTimeout time.Duration, next http.Handler) http.Handler {
	timeoutBody := `{"error":"request timeout"}`

	readHandler := http.TimeoutHandler(next, requestTimeout, timeoutBody)
	txHandler := http.TimeoutHandler(next, txTimeout, timeoutBody)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// SSE and WebSocket endpoints are long-lived; skip timeout wrapping.
		if isStreamingEndpoint(r) {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/") {
			txHandler.ServeHTTP(w, r)
			return
		}
		readHandler.ServeHTTP(w, r)
	})
}
