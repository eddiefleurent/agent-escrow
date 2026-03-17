package mcpserver

import (
	"strings"
	"testing"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestParseOptionalMilestoneIndex(t *testing.T) {
	t.Parallel()

	got, err := parseOptionalMilestoneIndex("", 3)
	if err != nil || got != nil {
		t.Fatalf("expected empty input to return nil,nil, got %v err=%v", got, err)
	}

	got, err = parseOptionalMilestoneIndex("1", 3)
	if err != nil || got == nil || *got != 1 {
		t.Fatalf("expected parsed index 1, got %v err=%v", got, err)
	}

	if _, err := parseOptionalMilestoneIndex("-1", 3); err == nil {
		t.Fatal("expected negative index error")
	}
	if _, err := parseOptionalMilestoneIndex("9", 3); err == nil {
		t.Fatal("expected out-of-range index error")
	}
}

func TestParseEscrowMilestoneIndex(t *testing.T) {
	t.Parallel()

	escrow := &storage.Escrow{MilestoneCount: 2}
	got, err := parseEscrowMilestoneIndex(" 1 ", escrow)
	if err != nil || got == nil || *got != 1 {
		t.Fatalf("expected escrow milestone index 1, got %v err=%v", got, err)
	}
}

func TestNormalizeToken(t *testing.T) {
	t.Parallel()

	if got := normalizeToken(""); got != "" {
		t.Fatalf("expected empty token to stay empty, got %q", got)
	}
	if got := normalizeToken("0x0000000000000000000000000000000000000000"); got != "" {
		t.Fatalf("expected zero-address token normalization to empty, got %q", got)
	}
	addr := "0x1111111111111111111111111111111111111111"
	if got := normalizeToken(addr); got != addr {
		t.Fatalf("expected non-zero token to pass through, got %q", got)
	}
}

func TestTextAndJSONResult(t *testing.T) {
	t.Parallel()

	txt := textResult("hello")
	if len(txt.Content) != 1 {
		t.Fatalf("expected one text content entry, got %d", len(txt.Content))
	}
	textContent, ok := txt.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", txt.Content[0])
	}
	if textContent.Text != "hello" {
		t.Fatalf("expected text payload 'hello', got %q", textContent.Text)
	}

	js, _, err := jsonResult(map[string]any{"ok": true})
	if err != nil {
		t.Fatalf("expected jsonResult to succeed, got %v", err)
	}
	if len(js.Content) != 1 {
		t.Fatalf("expected one JSON text content entry, got %d", len(js.Content))
	}
	jsonContent, ok := js.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", js.Content[0])
	}
	if !strings.Contains(jsonContent.Text, `"ok": true`) {
		t.Fatalf("expected formatted JSON output, got %q", jsonContent.Text)
	}
}
