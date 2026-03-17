package cli

import (
	"testing"

	"github.com/spf13/cobra"
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

func TestBidSubcommandArgValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cmd  func(*Options) *cobra.Command
	}{
		{name: "commit", cmd: newBidCommitCmd},
		{name: "reveal", cmd: newBidRevealCmd},
		{name: "list", cmd: newBidListCmd},
		{name: "accept", cmd: newBidAcceptCmd},
	}

	for _, tc := range cases {
		command := tc.cmd(testOptions())
		if err := command.Args(command, []string{}); err == nil {
			t.Fatalf("%s: expected exact-args validation failure", tc.name)
		}
		if err := command.Args(command, []string{"rfq-1"}); err != nil {
			t.Fatalf("%s: expected one arg to pass validation, got %v", tc.name, err)
		}
	}
}
