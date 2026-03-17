package cli

import (
	"testing"
	"time"
)

func TestEventsCommandStructure(t *testing.T) {
	t.Parallel()

	cmd := newEventsCmd(testOptions())
	if !hasSubcommand(cmd, "subscribe") {
		t.Fatal("expected events subscribe subcommand")
	}
	subscribe := cmd.Commands()[0]
	if subscribe.Flags().Lookup("escrow-id") == nil {
		t.Fatal("expected subscribe command to expose --escrow-id")
	}
	granularity, err := subscribe.Flags().GetString("granularity")
	if err != nil {
		t.Fatalf("read granularity flag: %v", err)
	}
	if granularity != "L1" {
		t.Fatalf("expected default granularity L1, got %q", granularity)
	}
}

func TestStreamContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := streamContext(&Options{Timeout: 5 * time.Millisecond})
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("expected timeout context to include deadline")
	}

	<-time.After(8 * time.Millisecond)
	if ctx.Err() == nil {
		t.Fatal("expected timeout context to expire")
	}

	bg, bgCancel := streamContext(&Options{Timeout: 0})
	defer bgCancel()
	if _, ok := bg.Deadline(); ok {
		t.Fatal("expected non-timeout context to have no deadline")
	}
}
