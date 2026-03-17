package cli

import (
	"strings"
	"testing"
)

func TestDCTCommandStructure(t *testing.T) {
	t.Parallel()

	cmd := newDCTCmd(testOptions())
	for _, name := range []string{"mint", "delegate", "introspect", "revoke", "emergency-override", "list-escrow", "audit"} {
		if !hasSubcommand(cmd, name) {
			t.Fatalf("expected dct subcommand %q", name)
		}
	}
}

func TestDCTEscrowListRejectsInvalidEscrowID(t *testing.T) {
	t.Parallel()

	cmd := newDCTEscrowListCmd(testOptions())
	err := cmd.RunE(cmd, []string{"not-an-int"})
	if err == nil || !strings.Contains(err.Error(), "positive integer") {
		t.Fatalf("expected positive integer validation error, got %v", err)
	}
}

func TestDCTAuditRejectsInvalidEscrowID(t *testing.T) {
	t.Parallel()

	cmd := newDCTAuditCmd(testOptions())
	err := cmd.RunE(cmd, []string{"-1"})
	if err == nil || !strings.Contains(err.Error(), "positive integer") {
		t.Fatalf("expected positive integer validation error, got %v", err)
	}
}
