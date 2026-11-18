package cli

import (
	"bytes"
	"testing"
)

func TestNewRootCmdRegistersTopLevelCommands(t *testing.T) {
	t.Parallel()

	root := NewRootCmd(&bytes.Buffer{}, &bytes.Buffer{})
	want := []string{
		"health",
		"escrow",
		"rfq",
		"bid",
		"reputation",
		"events",
		"emergency",
		"ap2",
		"dct",
		"decomposition",
		"ucp",
	}

	seen := map[string]bool{}
	for _, c := range root.Commands() {
		seen[c.Name()] = true
	}
	for _, name := range want {
		if !seen[name] {
			t.Fatalf("expected root command %q to be registered", name)
		}
	}
}
