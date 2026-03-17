package cli

import "testing"

func TestQuorumEscrowActionCommands(t *testing.T) {
	t.Parallel()

	verifierStake := newEscrowVerifierStakeCmd(testOptions())
	if verifierStake.Use != "verifier-stake <id>" {
		t.Fatalf("unexpected verifier-stake usage: %q", verifierStake.Use)
	}

	withdrawStake := newEscrowWithdrawStakeCmd(testOptions())
	if withdrawStake.Use != "withdraw-stake <id>" {
		t.Fatalf("unexpected withdraw-stake usage: %q", withdrawStake.Use)
	}

	vote := newEscrowQuorumVoteCmd(testOptions())
	if vote.Use != "quorum-vote <id>" {
		t.Fatalf("unexpected quorum-vote usage: %q", vote.Use)
	}
	if vote.Flags().Lookup("data") == nil {
		t.Fatal("expected quorum-vote to expose payload flag --data")
	}
}
