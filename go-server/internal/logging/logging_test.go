package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/authz"
)

func TestContextHandlerAddsRequestID(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(NewContextHandler(slog.NewJSONHandler(&buf, nil)))
	ctx := authz.WithRequestID(context.Background(), "ctx-id-1")

	logger.InfoContext(ctx, "from context", "k", "v")
	logger.With("component", "test").WarnContext(ctx, "derived logger")
	logger.InfoContext(ctx, "explicit", RequestIDKey, "ctx-id-1")
	logger.InfoContext(context.Background(), "no id")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 log lines, got %d: %s", len(lines), buf.String())
	}
	for i, want := range []string{"ctx-id-1", "ctx-id-1", "ctx-id-1", ""} {
		var rec map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &rec); err != nil {
			t.Fatalf("decode line %d: %v", i, err)
		}
		got, _ := rec[RequestIDKey].(string)
		if got != want {
			t.Fatalf("line %d: expected request_id %q, got %q (%s)", i, want, got, lines[i])
		}
	}
	if strings.Count(lines[2], RequestIDKey) != 1 {
		t.Fatalf("explicit request_id was duplicated: %s", lines[2])
	}
}
