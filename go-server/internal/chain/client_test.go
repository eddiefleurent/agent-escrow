package chain

import (
	"context"
	"strings"
	"testing"
)

func TestStripHexPrefix(t *testing.T) {
	t.Parallel()

	if got := stripHexPrefix("0xabc123"); got != "abc123" {
		t.Fatalf("expected prefix to be stripped, got %q", got)
	}
	if got := stripHexPrefix("abc123"); got != "abc123" {
		t.Fatalf("expected value without prefix to remain unchanged, got %q", got)
	}
}

func TestWithLogChunkSizeOption(t *testing.T) {
	t.Parallel()

	client := &Client{logChunkSize: defaultLogChunkSize}
	WithLogChunkSize(9)(client)
	if client.logChunkSize != 9 {
		t.Fatalf("expected custom log chunk size 9, got %d", client.logChunkSize)
	}

	defaultClient := &Client{logChunkSize: defaultLogChunkSize}
	WithLogChunkSize(0)(defaultClient)
	if defaultClient.logChunkSize != defaultLogChunkSize {
		t.Fatalf("expected default log chunk size %d, got %d", defaultLogChunkSize, defaultClient.logChunkSize)
	}
}

func TestBlockNumberOfflineMode(t *testing.T) {
	t.Parallel()

	client, err := NewClient("", "", 84532)
	if err != nil {
		t.Fatalf("new offline client: %v", err)
	}

	_, err = client.BlockNumber(context.Background())
	if err == nil || !strings.Contains(err.Error(), "offline mode") {
		t.Fatalf("expected offline mode error, got %v", err)
	}
}
