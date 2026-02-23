package cli

import "github.com/spf13/cobra"

func newEmergencyCmd(opts *Options) *cobra.Command {
	emergencyCmd := &cobra.Command{
		Use:   "emergency",
		Short: "Emergency protocol commands",
	}

	emergencyCmd.AddCommand(newEmergencyPostCmd(opts, "freeze-address", "Freeze an address", "/api/v1/emergency/freeze-address", true))
	emergencyCmd.AddCommand(newEmergencyPostCmd(opts, "unfreeze-address", "Unfreeze an address", "/api/v1/emergency/unfreeze-address", true))
	emergencyCmd.AddCommand(newEmergencyPostCmd(opts, "freeze-escrow", "Freeze an escrow", "/api/v1/emergency/freeze-escrow", true))
	emergencyCmd.AddCommand(newEmergencyPostCmd(opts, "unfreeze-escrow", "Unfreeze an escrow", "/api/v1/emergency/unfreeze-escrow", true))
	emergencyCmd.AddCommand(newEmergencyPostCmd(opts, "resolve", "Emergency resolve an escrow", "/api/v1/emergency/resolve", true))
	emergencyCmd.AddCommand(newFrozenAddressesCmd(opts))
	emergencyCmd.AddCommand(newEmergencyActionsCmd(opts))

	return emergencyCmd
}

func newEmergencyPostCmd(opts *Options, use, short, path string, payloadRequired bool) *cobra.Command {
	pf := payloadFlags{}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body, err := payloadFromFlags(pf, payloadRequired)
			if err != nil {
				return err
			}
			return runPost(cmd, opts, path, body)
		},
	}
	if payloadRequired {
		attachPayloadFlags(cmd, &pf)
	}
	return cmd
}

func newFrozenAddressesCmd(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "frozen-addresses",
		Short: "List frozen addresses",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGet(cmd, opts, "/api/v1/emergency/frozen-addresses", nil)
		},
	}
}

func newEmergencyActionsCmd(opts *Options) *cobra.Command {
	var limit string
	var offset string
	cmd := &cobra.Command{
		Use:   "actions",
		Short: "List emergency actions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGet(cmd, opts, "/api/v1/emergency/actions", map[string]string{
				"limit":  limit,
				"offset": offset,
			})
		},
	}
	cmd.Flags().StringVar(&limit, "limit", "", "Result limit")
	cmd.Flags().StringVar(&offset, "offset", "", "Result offset")
	return cmd
}
