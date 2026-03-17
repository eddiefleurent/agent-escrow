package cli

import (
	"testing"
	"time"
)

func TestFirstNonEmpty(t *testing.T) {
	t.Parallel()

	if got := firstNonEmpty("", "x", "y"); got != "x" {
		t.Fatalf("expected first non-empty value x, got %q", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Fatalf("expected empty result when all values empty, got %q", got)
	}
}

func TestServerURLDefaultUsesEnv(t *testing.T) {
	t.Setenv("ESCROW_SERVER_URL", "http://env-server:9999")
	if got := serverURLDefault(); got != "http://env-server:9999" {
		t.Fatalf("expected env server URL, got %q", got)
	}
}

func TestCommandContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := commandContext(&Options{Timeout: 5 * time.Millisecond})
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("expected timeout context to carry deadline")
	}

	select {
	case <-ctx.Done():
		// success
	case <-time.After(50 * time.Millisecond):
		t.Fatal("expected timeout context to expire")
	}

	bg, bgCancel := commandContext(&Options{Timeout: 0})
	defer bgCancel()
	if _, ok := bg.Deadline(); ok {
		t.Fatal("expected non-timeout context to have no deadline")
	}
	if bg.Err() != nil {
		t.Fatalf("expected non-timeout context to have no error, got %v", bg.Err())
	}
}

func TestNewClientFallsBackToDefaultServer(t *testing.T) {
	t.Setenv("ESCROW_SERVER_URL", "")

	c := newClient(&Options{})
	if c.baseURL != defaultServerURL {
		t.Fatalf("expected default server URL %q, got %q", defaultServerURL, c.baseURL)
	}
}
