package cli

import "github.com/spf13/cobra"

// Quorum-specific escrow actions.

func newEscrowVerifierStakeCmd(opts *Options) *cobra.Command {
	return postByEscrowIDCmd(opts, "verifier-stake", "Deposit verifier quorum stake", "/deposit-verifier-stake", false)
}

func newEscrowWithdrawStakeCmd(opts *Options) *cobra.Command {
	return postByEscrowIDCmd(opts, "withdraw-stake", "Withdraw owed verifier stake after quorum settlement or refund", "/withdraw-stake", false)
}

func newEscrowQuorumVoteCmd(opts *Options) *cobra.Command {
	return postByEscrowIDCmd(opts, "quorum-vote", "Cast verifier quorum vote", "/quorum-vote", true)
}
