package cli

import (
	"bytes"
	"testing"
)

func TestCommandConstructors(t *testing.T) {
	t.Parallel()

	opts := &Options{
		ServerURL: "http://localhost:8080",
		Output:    outputJSON,
	}

	cmd := newAP2Cmd(opts)
	if cmd.Use == "" || len(cmd.Commands()) < 3 {
		t.Fatalf("ap2 command should expose subcommands, got use=%q subcommands=%d", cmd.Use, len(cmd.Commands()))
	}

	cmd = newBidCmd(opts)
	if cmd.Use == "" || len(cmd.Commands()) < 4 {
		t.Fatalf("bid command should expose subcommands, got use=%q subcommands=%d", cmd.Use, len(cmd.Commands()))
	}

	cmd = newDCTCmd(opts)
	if cmd.Use == "" || len(cmd.Commands()) < 4 {
		t.Fatalf("dct command should expose subcommands, got use=%q subcommands=%d", cmd.Use, len(cmd.Commands()))
	}

	cmd = newDecompositionCmd(opts)
	if cmd.Use == "" || len(cmd.Commands()) < 4 {
		t.Fatalf("decomposition command should expose subcommands, got use=%q subcommands=%d", cmd.Use, len(cmd.Commands()))
	}

	cmd = newEmergencyCmd(opts)
	if cmd.Use == "" || len(cmd.Commands()) < 6 {
		t.Fatalf("emergency command should expose subcommands, got use=%q subcommands=%d", cmd.Use, len(cmd.Commands()))
	}

	cmd = newEscrowCmd(opts)
	if cmd.Use == "" || len(cmd.Commands()) < 10 {
		t.Fatalf("escrow command should expose subcommands, got use=%q subcommands=%d", cmd.Use, len(cmd.Commands()))
	}

	cmd = newEventsCmd(opts)
	if cmd.Use == "" || len(cmd.Commands()) < 1 {
		t.Fatalf("events command should expose subcommands, got use=%q subcommands=%d", cmd.Use, len(cmd.Commands()))
	}

	if cmd := newHealthCmd(opts); cmd.Use == "" {
		t.Fatal("health command should have use text")
	}
	if cmd := newEscrowVerifierStakeCmd(opts); cmd.Use == "" {
		t.Fatal("quorum verifier-stake command should have use text")
	}
	if cmd := newEscrowWithdrawStakeCmd(opts); cmd.Use == "" {
		t.Fatal("quorum withdraw-stake command should have use text")
	}
	if cmd := newEscrowQuorumVoteCmd(opts); cmd.Use == "" {
		t.Fatal("quorum vote command should have use text")
	}
	if cmd := newRFQCmd(opts); cmd.Use == "" || len(cmd.Commands()) < 4 {
		t.Fatalf("rfq command should expose subcommands, got use=%q subcommands=%d", cmd.Use, len(cmd.Commands()))
	}
	if cmd := newUCPCmd(opts); cmd.Use == "" || len(cmd.Commands()) < 6 {
		t.Fatalf("ucp command should expose subcommands, got use=%q subcommands=%d", cmd.Use, len(cmd.Commands()))
	}
}

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
