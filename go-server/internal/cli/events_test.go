package cli

import (
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestEventsCommandStructure(t *testing.T) {
	t.Parallel()

	cmd := newEventsCmd(testOptions())
	if !hasSubcommand(cmd, "subscribe") {
		t.Fatal("expected events subscribe subcommand")
	}

	var subscribe *cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Name() == "subscribe" {
			subscribe = sub
			break
		}
	}
	if subscribe == nil {
		t.Fatal("expected subscribe command to be found")
	}

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

	select {
	case <-ctx.Done():
		// success
	case <-time.After(100 * time.Millisecond):
		t.Fatal("context did not expire within timeout")
	}

	bg, bgCancel := streamContext(&Options{Timeout: 0})
	defer bgCancel()
	if _, ok := bg.Deadline(); ok {
		t.Fatal("expected non-timeout context to have no deadline")
	}
}
