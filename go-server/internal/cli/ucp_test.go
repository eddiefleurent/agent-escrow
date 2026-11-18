package cli

import (
	"testing"
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
