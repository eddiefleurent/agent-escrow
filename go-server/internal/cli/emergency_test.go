package cli

import "testing"

func TestEmergencyCommandStructure(t *testing.T) {
	t.Parallel()

	cmd := newEmergencyCmd(testOptions())
	for _, name := range []string{
		"freeze-address",
		"unfreeze-address",
		"freeze-escrow",
		"unfreeze-escrow",
		"resolve",
		"frozen-addresses",
		"actions",
	} {
		if !hasSubcommand(cmd, name) {
			t.Fatalf("expected emergency subcommand %q", name)
		}
	}
}

func TestEmergencyPostCommandPayloadFlagsOnlyWhenRequired(t *testing.T) {
	t.Parallel()

	required := newEmergencyPostCmd(testOptions(), "freeze-address", "Freeze", "/api/v1/emergency/freeze-address", true)
	if required.Flags().Lookup("data") == nil {
		t.Fatal("expected required emergency post command to expose payload flags")
	}

	optional := newEmergencyPostCmd(testOptions(), "noop", "No-op", "/api/v1/emergency/noop", false)
	if optional.Flags().Lookup("data") != nil {
		t.Fatal("expected optional emergency post command to avoid payload flags")
	}
}
