package cli

import (
	"testing"
)

func TestBidCommandStructure(t *testing.T) {
	t.Parallel()

	cmd := newBidCmd(testOptions())
	for _, name := range []string{"commit", "reveal", "list", "accept"} {
		if !hasSubcommand(cmd, name) {
			t.Fatalf("expected bid subcommand %q", name)
		}
	}
}
