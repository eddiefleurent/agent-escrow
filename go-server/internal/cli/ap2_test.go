package cli

import "testing"

func TestAP2CommandStructure(t *testing.T) {
	t.Parallel()

	cmd := newAP2Cmd(testOptions())
	for _, name := range []string{"fund", "validate", "mandate"} {
		if !hasSubcommand(cmd, name) {
			t.Fatalf("expected ap2 subcommand %q", name)
		}
	}
}
