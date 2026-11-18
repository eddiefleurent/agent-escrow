// Package logging wires request correlation into structured logs.
package logging

import (
	"context"
	"log/slog"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/authz"
)

// RequestIDKey is the log attribute that carries the request correlation id.
const RequestIDKey = "request_id"

// ContextHandler adds request_id from the context to every record logged
// through a *Context call (slog.InfoContext and friends). Records that
// already carry a top-level request_id are left alone so call sites that set
// it explicitly do not log it twice.
//
// Attributes are added to the record, so a logger built with WithGroup nests
// request_id inside that group like any other record attribute.
type ContextHandler struct {
	slog.Handler
}

// NewContextHandler wraps next so records pick up the context request id.
func NewContextHandler(next slog.Handler) *ContextHandler {
	return &ContextHandler{Handler: next}
}

// Handle implements slog.Handler.
func (h *ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := authz.RequestIDFrom(ctx); id != "" && !hasRequestID(r) {
		r = r.Clone()
		r.AddAttrs(slog.String(RequestIDKey, id))
	}
	return h.Handler.Handle(ctx, r)
}

// WithAttrs implements slog.Handler.
func (h *ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ContextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

// WithGroup implements slog.Handler.
func (h *ContextHandler) WithGroup(name string) slog.Handler {
	return &ContextHandler{Handler: h.Handler.WithGroup(name)}
}

func hasRequestID(r slog.Record) bool {
	found := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == RequestIDKey {
			found = true
			return false
		}
		return true
	})
	return found
}
