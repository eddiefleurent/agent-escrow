package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestUCPCommandStructure(t *testing.T) {
	t.Parallel()

	cmd := newUCPCmd(testOptions())
	for _, name := range []string{"profile", "create", "get", "update", "complete", "cancel"} {
		if !hasSubcommand(cmd, name) {
			t.Fatalf("expected ucp subcommand %q", name)
		}
	}
}

func TestUCPArgValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cmd  func(*Options) *cobra.Command
	}{
		{name: "get", cmd: newUCPGetCmd},
		{name: "update", cmd: newUCPUpdateCmd},
		{name: "complete", cmd: newUCPCompleteCmd},
		{name: "cancel", cmd: newUCPCancelCmd},
	}

	for _, tc := range cases {
		command := tc.cmd(testOptions())
		if err := command.Args(command, []string{}); err == nil {
			t.Fatalf("%s: expected exact-args validation failure", tc.name)
		}
		if err := command.Args(command, []string{"checkout-1"}); err != nil {
			t.Fatalf("%s: expected one arg to pass validation, got %v", tc.name, err)
		}
	}
}
