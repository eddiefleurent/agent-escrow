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

func TestAP2MandateCommandArgs(t *testing.T) {
	t.Parallel()

	cmd := newAP2MandateCmd(testOptions())
	if err := cmd.Args(cmd, []string{}); err == nil {
		t.Fatal("expected mandate command to require one arg")
	}
	if err := cmd.Args(cmd, []string{"id-1"}); err != nil {
		t.Fatalf("expected exactly one arg to pass validation, got %v", err)
	}
}
