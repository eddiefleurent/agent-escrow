package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPayloadFromFlagsModes(t *testing.T) {
	t.Parallel()

	_, err := payloadFromFlags(payloadFlags{inline: "{}", file: "x.json"}, true)
	if err == nil {
		t.Fatal("expected mutual exclusivity error")
	}

	got, err := payloadFromFlags(payloadFlags{}, false)
	if err != nil {
		t.Fatalf("expected optional empty payload to succeed, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil payload for optional empty input, got %#v", got)
	}

	_, err = payloadFromFlags(payloadFlags{}, true)
	if err == nil {
		t.Fatal("expected required payload error")
	}
}

func TestPayloadFromFlagsFileAndBuildQuery(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "payload.json")
	if err := os.WriteFile(path, []byte(`{"name":"alice"}`), 0o600); err != nil {
		t.Fatalf("write payload file: %v", err)
	}

	got, err := payloadFromFlags(payloadFlags{file: path}, true)
	if err != nil {
		t.Fatalf("expected file payload to parse, got %v", err)
	}
	obj, ok := got.(map[string]any)
	if !ok || obj["name"] != "alice" {
		t.Fatalf("expected payload object with name=alice, got %#v", got)
	}

	query := buildQuery(map[string]string{
		"status": " open ",
		"empty":  "   ",
		"buyer":  "0xabc",
	})
	if query.Get("status") != "open" {
		t.Fatalf("expected trimmed status=open, got %q", query.Get("status"))
	}
	if query.Get("buyer") != "0xabc" {
		t.Fatalf("expected buyer=0xabc, got %q", query.Get("buyer"))
	}
	if query.Has("empty") {
		t.Fatalf("expected empty key to be omitted, got %q", query.Get("empty"))
	}
}
