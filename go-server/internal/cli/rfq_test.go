package cli

import "testing"

func TestRFQCommandStructure(t *testing.T) {
	t.Parallel()

	cmd := newRFQCmd(testOptions())
	for _, name := range []string{"create", "list", "get", "cancel"} {
		if !hasSubcommand(cmd, name) {
			t.Fatalf("expected rfq subcommand %q", name)
		}
	}
}
