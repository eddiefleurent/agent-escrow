package cli

import (
	"github.com/spf13/cobra"
)

func newRFQCmd(opts *Options) *cobra.Command {
	rfqCmd := &cobra.Command{
		Use:   "rfq",
		Short: "RFQ commands",
	}

	rfqCmd.AddCommand(newRFQCreateCmd(opts))
	rfqCmd.AddCommand(newRFQListCmd(opts))
	rfqCmd.AddCommand(newRFQGetCmd(opts))
	rfqCmd.AddCommand(newRFQCancelCmd(opts))

	return rfqCmd
}

func newRFQCreateCmd(opts *Options) *cobra.Command {
	pf := payloadFlags{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a request for quote",
		RunE: func(cmd *cobra.Command, _ []string) error {
			body, err := payloadFromFlags(pf, true)
			if err != nil {
				return err
			}
			return runPost(cmd, opts, "/api/v1/rfqs", body)
		},
	}
	attachPayloadFlags(cmd, &pf)
	return cmd
}

func newRFQListCmd(opts *Options) *cobra.Command {
	var status string
	var buyer string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List RFQs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGet(cmd, opts, "/api/v1/rfqs", map[string]string{
				"status": status,
				"buyer":  buyer,
			})
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "Filter by status")
	cmd.Flags().StringVar(&buyer, "buyer", "", "Filter by buyer address")
	return cmd
}

func newRFQGetCmd(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get RFQ by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGet(cmd, opts, "/api/v1/rfqs/"+args[0], nil)
		},
	}
}

func newRFQCancelCmd(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <id>",
		Short: "Cancel RFQ",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPost(cmd, opts, "/api/v1/rfqs/"+args[0]+"/cancel", nil)
		},
	}
}
