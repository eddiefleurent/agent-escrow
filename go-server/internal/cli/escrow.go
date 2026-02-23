package cli

import (
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
)

func newEscrowCmd(opts *Options) *cobra.Command {
	escrowCmd := &cobra.Command{
		Use:   "escrow",
		Short: "Escrow lifecycle commands",
	}

	escrowCmd.AddCommand(newEscrowCreateCmd(opts))
	escrowCmd.AddCommand(newEscrowGetCmd(opts))
	escrowCmd.AddCommand(newEscrowListCmd(opts))
	escrowCmd.AddCommand(newEscrowFundCmd(opts))
	escrowCmd.AddCommand(newEscrowStakeCmd(opts))
	escrowCmd.AddCommand(newEscrowSubmitCmd(opts))
	escrowCmd.AddCommand(newEscrowApproveCmd(opts))
	escrowCmd.AddCommand(newEscrowDisputeCmd(opts))
	escrowCmd.AddCommand(newEscrowResolveCmd(opts))
	escrowCmd.AddCommand(newEscrowAbortCmd(opts))
	escrowCmd.AddCommand(newEscrowBackupCmd(opts))

	return escrowCmd
}

func newEscrowCreateCmd(opts *Options) *cobra.Command {
	pf := payloadFlags{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an escrow",
		RunE: func(cmd *cobra.Command, _ []string) error {
			body, err := payloadFromFlags(pf, true)
			if err != nil {
				return err
			}
			return runPost(cmd, opts, "/api/v1/escrows", body)
		},
	}
	attachPayloadFlags(cmd, &pf)
	return cmd
}

func newEscrowGetCmd(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get escrow by local database id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGet(cmd, opts, "/api/v1/escrows/"+url.PathEscape(args[0]), nil)
		},
	}
}

func newEscrowListCmd(opts *Options) *cobra.Command {
	var role string
	var address string
	var status string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List escrows",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGet(cmd, opts, "/api/v1/escrows", map[string]string{
				"role":    role,
				"address": address,
				"status":  status,
			})
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "Filter by role: buyer|worker|verifier|arbitrator")
	cmd.Flags().StringVar(&address, "address", "", "Filter by participant address")
	cmd.Flags().StringVar(&status, "status", "", "Filter by status")
	return cmd
}

func newEscrowFundCmd(opts *Options) *cobra.Command {
	return postByEscrowIDCmd(opts, "fund", "Fund escrow", "/fund", false)
}

func newEscrowStakeCmd(opts *Options) *cobra.Command {
	return postByEscrowIDCmd(opts, "stake", "Deposit worker stake", "/deposit-stake", false)
}

func newEscrowSubmitCmd(opts *Options) *cobra.Command {
	return postByEscrowIDCmd(opts, "submit", "Submit work for escrow", "/submit", true)
}

func newEscrowApproveCmd(opts *Options) *cobra.Command {
	return postByEscrowIDCmd(opts, "approve", "Approve work for escrow", "/approve", true)
}

func newEscrowDisputeCmd(opts *Options) *cobra.Command {
	return postByEscrowIDCmd(opts, "dispute", "Dispute work for escrow", "/dispute", true)
}

func newEscrowResolveCmd(opts *Options) *cobra.Command {
	return postByEscrowIDCmd(opts, "resolve", "Resolve escrow dispute", "/resolve", true)
}

func newEscrowAbortCmd(opts *Options) *cobra.Command {
	return postByEscrowIDCmd(opts, "abort", "Abort remaining milestones", "/abort-milestones", false)
}

func newEscrowBackupCmd(opts *Options) *cobra.Command {
	return postByEscrowIDCmd(opts, "backup", "Activate backup worker", "/activate-backup", false)
}

func postByEscrowIDCmd(opts *Options, use, short, actionPath string, requiresPayload bool) *cobra.Command {
	pf := payloadFlags{}
	cmd := &cobra.Command{
		Use:   use + " <id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := payloadFromFlags(pf, requiresPayload)
			if err != nil {
				return err
			}
			escrowID := url.PathEscape(args[0])
			path := fmt.Sprintf("/api/v1/escrows/%s%s", escrowID, actionPath)
			return runPost(cmd, opts, path, body)
		},
	}
	if requiresPayload {
		attachPayloadFlags(cmd, &pf)
	}
	return cmd
}
