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

func TestRFQGetAndCancelArgValidation(t *testing.T) {
	t.Parallel()

	getCmd := newRFQGetCmd(testOptions())
	if err := getCmd.Args(getCmd, []string{}); err == nil {
		t.Fatal("expected get command to require one arg")
	}
	if err := getCmd.Args(getCmd, []string{"id-1"}); err != nil {
		t.Fatalf("expected get command arg validation to pass, got %v", err)
	}

	cancelCmd := newRFQCancelCmd(testOptions())
	if err := cancelCmd.Args(cancelCmd, []string{}); err == nil {
		t.Fatal("expected cancel command to require one arg")
	}
	if err := cancelCmd.Args(cancelCmd, []string{"id-1"}); err != nil {
		t.Fatalf("expected cancel command arg validation to pass, got %v", err)
	}
}
