package cli

import (
	"net/url"

	"github.com/spf13/cobra"
)

func newUCPCmd(opts *Options) *cobra.Command {
	ucpCmd := &cobra.Command{
		Use:   "ucp",
		Short: "UCP checkout adapter commands",
	}

	ucpCmd.AddCommand(newUCPProfileCmd(opts))
	ucpCmd.AddCommand(newUCPCreateCmd(opts))
	ucpCmd.AddCommand(newUCPGetCmd(opts))
	ucpCmd.AddCommand(newUCPUpdateCmd(opts))
	ucpCmd.AddCommand(newUCPCompleteCmd(opts))
	ucpCmd.AddCommand(newUCPCancelCmd(opts))

	return ucpCmd
}

func newUCPProfileCmd(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "profile",
		Short: "Fetch /.well-known/ucp profile",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGet(cmd, opts, "/.well-known/ucp", nil)
		},
	}
}

func newUCPCreateCmd(opts *Options) *cobra.Command {
	pf := payloadFlags{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a UCP checkout",
		RunE: func(cmd *cobra.Command, _ []string) error {
			body, err := payloadFromFlags(pf, true)
			if err != nil {
				return err
			}
			return runPost(cmd, opts, "/api/v1/ucp/checkouts", body)
		},
	}
	attachPayloadFlags(cmd, &pf)
	return cmd
}

func newUCPGetCmd(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "get <checkout-id>",
		Short: "Get a UCP checkout by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/api/v1/ucp/checkouts/" + url.PathEscape(args[0])
			return runGet(cmd, opts, path, nil)
		},
	}
}

func newUCPUpdateCmd(opts *Options) *cobra.Command {
	pf := payloadFlags{}
	cmd := &cobra.Command{
		Use:   "update <checkout-id>",
		Short: "Update a UCP checkout with mapped operation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := payloadFromFlags(pf, true)
			if err != nil {
				return err
			}
			path := "/api/v1/ucp/checkouts/" + url.PathEscape(args[0])
			return runPatch(cmd, opts, path, body)
		},
	}
	attachPayloadFlags(cmd, &pf)
	return cmd
}

func newUCPCompleteCmd(opts *Options) *cobra.Command {
	pf := payloadFlags{}
	cmd := &cobra.Command{
		Use:   "complete <checkout-id>",
		Short: "Attempt UCP completion for a checkout",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := payloadFromFlags(pf, false)
			if err != nil {
				return err
			}
			path := "/api/v1/ucp/checkouts/" + url.PathEscape(args[0]) + "/complete"
			return runPost(cmd, opts, path, body)
		},
	}
	attachPayloadFlags(cmd, &pf)
	return cmd
}

func newUCPCancelCmd(opts *Options) *cobra.Command {
	pf := payloadFlags{}
	cmd := &cobra.Command{
		Use:   "cancel <checkout-id>",
		Short: "Cancel a checkout via escrow cancellation/refund paths",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := payloadFromFlags(pf, false)
			if err != nil {
				return err
			}
			path := "/api/v1/ucp/checkouts/" + url.PathEscape(args[0]) + "/cancel"
			return runPost(cmd, opts, path, body)
		},
	}
	attachPayloadFlags(cmd, &pf)
	return cmd
}
