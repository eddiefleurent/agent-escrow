package cli

import "testing"

func TestEscrowCommandStructure(t *testing.T) {
	t.Parallel()

	cmd := newEscrowCmd(testOptions())
	for _, name := range []string{
		"create",
		"get",
		"list",
		"fund",
		"stake",
		"verifier-stake",
		"withdraw-stake",
		"submit",
		"verify-approve",
		"quorum-vote",
		"approve",
		"dispute",
		"resolve",
		"abort",
		"backup",
		"attestation-chain",
		"children",
		"checkpoint-commit",
		"checkpoints",
		"checkpoint-latest",
	} {
		if !hasSubcommand(cmd, name) {
			t.Fatalf("expected escrow subcommand %q", name)
		}
	}
}

func TestPostByEscrowIDCommandValidationAndFlags(t *testing.T) {
	t.Parallel()

	requiresPayload := postByEscrowIDCmd(testOptions(), "submit", "Submit", "/submit", true)
	if requiresPayload.Flags().Lookup("data") == nil {
		t.Fatal("expected payload flag on command requiring payload")
	}
	if err := requiresPayload.Args(requiresPayload, []string{}); err == nil {
		t.Fatal("expected missing escrow id to fail args validation")
	}

	noPayload := postByEscrowIDCmd(testOptions(), "fund", "Fund", "/fund", false)
	if noPayload.Flags().Lookup("data") != nil {
		t.Fatal("expected no payload flag on command without payload")
	}
	if err := noPayload.Args(noPayload, []string{"1"}); err != nil {
		t.Fatalf("expected one escrow id arg to pass, got %v", err)
	}
}
